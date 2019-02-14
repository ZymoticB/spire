package workload

import (
	"context"

	"github.com/spiffe/spire/proto/api/workload"
	"go.uber.org/zap"
)

type streamReader struct {
	Chan          chan *workload.X509SVIDResponse
	logger        *zap.Logger
	streamManager *streamManager
}

func newStreamReader(ctx context.Context, logger *zap.Logger, streamManager *streamManager) *streamReader {
	r := &streamReader{
		logger:        logger,
		streamManager: streamManager,
		Chan:          make(chan *workload.X509SVIDResponse),
	}
	r.start(ctx)
	return r
}

func (c *streamReader) start(ctx context.Context) {
	c.logger.Debug("Starting reader.")
	go func() {
		for {
			var stream workload.SpiffeWorkloadAPI_FetchX509SVIDClient
			var ok bool

			select {
			case stream, ok = <-c.streamManager.Chan:
				if !ok {
					continue
				}
			case <-ctx.Done():
				c.logger.Debug("Shutting down reader")
				close(c.Chan)
				return
			}
			for {
				resp, err := stream.Recv()
				if err != nil {
					c.logger.Info("Stream reader failed.", zap.Error(err))
					c.streamManager.Reconnect()
					break
				}
				c.Chan <- resp
			}
		}
	}()
}

func (c *streamReader) Stop() {
	if c.Chan != nil {
		c.logger.Debug("Emptying reader chan.", zap.Int("queued", len(c.Chan)))
		for range c.Chan {
		}
		c.Chan = nil
	}
}
