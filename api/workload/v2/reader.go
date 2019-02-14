package workload

import (
	"context"

	"github.com/spiffe/spire/proto/api/workload"
	"go.uber.org/zap"
)

type streamReader struct {
	logger        *zap.Logger
	streamManager *streamManager
	cchan         chan *workload.X509SVIDResponse
}

func newStreamReader(ctx context.Context, logger *zap.Logger, streamManager *streamManager) *streamReader {
	r := &streamReader{
		logger:        logger,
		streamManager: streamManager,
		cchan:         make(chan *workload.X509SVIDResponse),
	}
	r.start(ctx)
	return r
}

func (c *streamReader) Chan() chan *workload.X509SVIDResponse {
	return c.cchan
}

func (c *streamReader) start(ctx context.Context) {
	c.logger.Debug("Starting reader.")
	go func() {
		for {
			var stream workload.SpiffeWorkloadAPI_FetchX509SVIDClient
			var ok bool

			select {
			case stream, ok = <-c.streamManager.Chan():
				if !ok {
					continue
				}
			case <-ctx.Done():
				c.logger.Debug("Shutting down reader")
				close(c.cchan)
				return
			}
			for {
				resp, err := stream.Recv()
				if err != nil {
					c.logger.Info("Stream reader failed.", zap.Error(err))
					c.streamManager.Reconnect()
					break
				}
				c.cchan <- resp
			}
		}
	}()
}

func (c *streamReader) Stop() {
	if c.cchan != nil {
		c.logger.Debug("Emptying reader chan.", zap.Int("queued", len(c.cchan)))
		for range c.cchan {
		}
		c.cchan = nil
	}
}
