package workload

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spiffe/spire/proto/api/workload"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// streamManager manages connection streams
type streamManager struct {
	ctx            context.Context
	logger         *zap.Logger
	addr           string
	streamChan     chan workload.SpiffeWorkloadAPI_FetchX509SVIDClient
	reconnectChan  chan struct{}
	connectionChan chan bool
}

func newStreamManager(ctx context.Context, logger *zap.Logger, addr string, connectionChan chan bool) *streamManager {
	return &streamManager{
		ctx:            ctx,
		logger:         logger,
		addr:           addr,
		streamChan:     make(chan workload.SpiffeWorkloadAPI_FetchX509SVIDClient, 1),
		reconnectChan:  make(chan struct{}, 1),
		connectionChan: connectionChan,
	}
}

// Chan returns a channel of streams for fetching X509 SVIDs. It is updated whenever a new stream is created.
func (c *streamManager) Chan() chan workload.SpiffeWorkloadAPI_FetchX509SVIDClient {
	return c.streamChan
}

// Reconect informs the stream manager that the current stream is unusable.
func (c *streamManager) Reconnect() {
	c.reconnectChan <- struct{}{}
}

// Start starts the stream manager.
func (c *streamManager) Start(ctx context.Context) error {
	stream, closer, err := c.newStream(ctx, c.addr)
	if err != nil {
		c.logger.Debug("Shutting down stream manager.")
		return err
	}
	c.streamChan <- stream
	c.connectionChan <- true
	c.logger.Debug("Started stream manager.")

	go func() {
		for {
			select {
			case _, ok := <-c.reconnectChan:
				if ok {
					c.connectionChan <- false
					closer.Close()
					stream, closer, err = c.newStream(c.ctx, c.addr)
					if err != nil {
						c.logger.Debug("Shutting down stream manager.")
						return
					}
					c.streamChan <- stream
					c.connectionChan <- true
					c.logger.Debug("Created updated stream")
				}
			case <-c.ctx.Done():
				closer.Close()
				close(c.streamChan)
				c.logger.Debug("Shutting down stream manager.")
				return
			}
		}
	}()
	return nil
}

func (c *streamManager) newStream(ctx context.Context, addr string) (stream workload.SpiffeWorkloadAPI_FetchX509SVIDClient, closer io.Closer, err error) {
	backoff := newBackoff()
	for {
		conn, err := newConn(ctx, addr)
		if err != nil {
			goto retry
		}
		stream, err = newX509SVIDStream(ctx, conn)
		if err == nil {
			return stream, conn, nil
		}
	retry:
		c.logger.Debug("Error creating stream, retrying.", zap.Error(err))
		select {
		case <-ctx.Done():
			c.logger.Debug("Stream creator shutting down.")
			return nil, nil, ctx.Err()
		case <-time.After(backoff.Duration()):
		}
	}
}

func newConn(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	if !strings.HasPrefix(addr, "unix://") {
		return nil, fmt.Errorf("spiffe/workload: agent address %q is not a unix address", addr)
	}
	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("spiffe/workload: failed to dial workload API at %q: %v", addr, err)
	}
	return conn, nil
}

func newX509SVIDStream(ctx context.Context, conn *grpc.ClientConn) (workload.SpiffeWorkloadAPI_FetchX509SVIDClient, error) {
	workloadClient := workload.NewSpiffeWorkloadAPIClient(conn)
	header := metadata.Pairs("workload.spiffe.io", "true")
	grpcCtx := metadata.NewOutgoingContext(ctx, header)
	return workloadClient.FetchX509SVID(grpcCtx, &workload.X509SVIDRequest{})
}
