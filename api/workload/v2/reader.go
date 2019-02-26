package workload

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/spiffe/spire/proto/api/workload"
)

type streamReader struct {
	SVIDChan      chan *workload.X509SVIDResponse
	logger        *logrus.Logger
	streamManager *streamManager
	ctx           context.Context
	cancelFn      func()
}

func newStreamReader(ctx context.Context, logger *logrus.Logger, streamManager *streamManager) *streamReader {
	ctx, cancel := context.WithCancel(ctx)
	r := &streamReader{
		ctx:           ctx,
		cancelFn:      cancel,
		logger:        logger,
		streamManager: streamManager,
		SVIDChan:      make(chan *workload.X509SVIDResponse),
	}
	r.start()
	return r
}

func (c *streamReader) start() {
	c.logger.Debug("Starting reader.")
	go func() {
		defer c.logger.Debug("Shutting down reader")
		defer close(c.SVIDChan)

		for {
			select {
			case stream, ok := <-c.streamManager.StreamChan:
				if !ok {
					return
				}
				c.recv(stream)
			case <-c.ctx.Done():
				return
			}
		}
	}()
}

func (c *streamReader) recv(stream *managedStream) {
	defer func() {
		if err := stream.Close(); err != nil {
			c.logger.WithError(err).Info("Stream close failed.")
		}
	}()
	for {
		resp, err := stream.Recv(c.ctx)
		if err != nil {
			c.logger.WithError(err).Info("Stream reader failed.")
			c.streamManager.Reconnect()
			return
		}
		c.SVIDChan <- resp
	}
}

func (c *streamReader) Stop() {
	c.cancelFn()
	if c.SVIDChan != nil {
		c.logger.WithField("queued", len(c.SVIDChan)).Debug("Emptying reader chan.")
		for range c.SVIDChan {
		}
		c.SVIDChan = nil
	}
}
