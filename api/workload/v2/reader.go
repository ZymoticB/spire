package workload

import (
	"context"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/spiffe/spire/proto/api/workload"
)

type streamReader struct {
	SVIDChan      chan *workload.X509SVIDResponse
	logger        *logrus.Logger
	streamManager *streamManager

	// atomic stream
	mu     sync.RWMutex
	stream *managedStream
}

func newStreamReader(ctx context.Context, logger *logrus.Logger, streamManager *streamManager) *streamReader {
	r := &streamReader{
		logger:        logger,
		streamManager: streamManager,
		SVIDChan:      make(chan *workload.X509SVIDResponse),
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
		defer close(c.SVIDChan)

		for {

			select {
			case stream, ok := <-c.streamManager.StreamChan:
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
					c.logger.WithError(err).Info("Stream reader failed.")
					c.closeStream()
					c.streamManager.Reconnect()
					break
				}
				c.SVIDChan <- resp
			}
		}
	}()
}

func (c *streamReader) closeStream() {
	if stream := c.getStream(); stream != nil {
		if err := stream.Close(); err != nil {
			c.logger.WithError(err).Info("Stream close failed.")
		}
		c.setStream(nil)
	}
}

func (c *streamReader) Stop() {
	c.closeStream()
	if c.SVIDChan != nil {
		c.logger.WithField("queued", len(c.SVIDChan)).Debug("Emptying reader chan.")
		for range c.SVIDChan {
		}
		c.SVIDChan = nil
	}
}
