package workload

import (
	"context"
	"sync"

	"github.com/spiffe/spire/proto/api/workload"
	"go.uber.org/zap"
)

type streamReader struct {
	Chan          chan *workload.X509SVIDResponse
	logger        *zap.Logger
	streamManager *streamManager

	// atomic stream
	mu     *sync.RWMutex
	stream *managedStream
}

func newStreamReader(ctx context.Context, logger *zap.Logger, streamManager *streamManager) *streamReader {
	r := &streamReader{
		logger:        logger,
		streamManager: streamManager,
		mu:            new(sync.RWMutex),
		Chan:          make(chan *workload.X509SVIDResponse),
	}
	r.start(ctx)
	return r
}

func (c *streamReader) getStream() *managedStream {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stream
}

func (c *streamReader) setStream(stream *managedStream) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stream = stream
}

func (c *streamReader) start(ctx context.Context) {
	c.logger.Debug("Starting reader.")
	go func() {
		defer c.logger.Debug("Shutting down reader")
		defer c.closeStream()
		defer close(c.Chan)

		for {

			select {
			case stream, ok := <-c.streamManager.Chan:
				if !ok {
					return
				}
				c.setStream(stream)
			case <-ctx.Done():
				return
			}

			for {
				resp, err := c.getStream().Recv()
				if err != nil {
					c.logger.Info("Stream reader failed.", zap.Error(err))
					c.closeStream()
					c.streamManager.Reconnect()
					break
				}
				c.Chan <- resp
			}
		}
	}()
}

func (c *streamReader) closeStream() {
	if stream := c.getStream(); stream != nil {
		if err := stream.Close(); err != nil {
			c.logger.Info("Stream close failed.", zap.Error(err))
		}
		c.setStream(nil)
	}
}

func (c *streamReader) Stop() {
	c.closeStream()
	if c.Chan != nil {
		c.logger.Debug("Emptying reader chan.", zap.Int("queued", len(c.Chan)))
		for range c.Chan {
		}
		c.Chan = nil
	}
}
