package workload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spiffe/spire/proto/api/workload"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const _unixPathPrefix = "unix://"

// streamManager manages connection streams
type streamManager struct {
	// StreamChan is a channel of streams for fetching X509 SVIDs. It is updated whenever a new stream is created.
	StreamChan     chan *managedStream
	ctx            context.Context
	logger         *logrus.Logger
	addr           string
	reconnectChan  chan struct{}
	connectionChan chan bool
}

type managedStream struct {
	workload.SpiffeWorkloadAPI_FetchX509SVIDClient

	closer io.Closer
}

// Close closes the stream and the underlying connection.
func (s *managedStream) Close() error {
	var errs []string
	if err := s.SpiffeWorkloadAPI_FetchX509SVIDClient.CloseSend(); err != nil {
		errs = append(errs, err.Error())
	}
	if err := s.closer.Close(); err != nil {
		errs = append(errs, err.Error())
	}
	return errors.New(strings.Join(errs, "; "))
}

func newStreamManager(ctx context.Context, logger *logrus.Logger, addr string, connectionChan chan bool) (*streamManager, error) {
	if !strings.HasPrefix(addr, _unixPathPrefix) {
		return nil, fmt.Errorf("spiffe/workload: agent address %q is not a unix address", addr)
	}
	return &streamManager{
		StreamChan:     make(chan *managedStream, 1),
		ctx:            ctx,
		logger:         logger,
		addr:           addr,
		reconnectChan:  make(chan struct{}, 1),
		connectionChan: connectionChan,
	}, nil
}

// Reconect informs the stream manager that the current stream is unusable.
func (c *streamManager) Reconnect() {
	c.reconnectChan <- struct{}{}
}

// Start starts the stream manager.
//
// This blocks until a stream is established and will return an error if a stream
// can't be established within the context's deadline.
func (c *streamManager) Start(ctx context.Context) error {
	stream, closer, err := c.newStream(ctx, c.addr)
	if err != nil {
		c.logger.Debug("Stream manager failed to start.")
		return err
	}
	c.StreamChan <- &managedStream{stream, closer}
	c.connectionChan <- true
	c.logger.Debug("Started stream manager.")

	go func() {
		for {
			select {
			case _, ok := <-c.reconnectChan:
				if ok {
					c.connectionChan <- false
					stream, closer, err = c.newStream(c.ctx, c.addr)
					if err != nil {
						c.logger.Debug("Shutting down stream manager.")
						return
					}
					c.StreamChan <- &managedStream{stream, closer}
					c.connectionChan <- true
					c.logger.Debug("Created updated stream")
				}
			case <-c.ctx.Done():
				close(c.StreamChan)
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
		c.logger.WithError(err).Debug("Error creating stream, retrying.")
		select {
		case <-ctx.Done():
			c.logger.Debug("Stream creator shutting down.")
			return nil, nil, ctx.Err()
		case <-time.After(backoff.Duration()):
		}
	}
}

func newConn(ctx context.Context, addr string) (*grpc.ClientConn, error) {
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
