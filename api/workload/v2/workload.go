package workload

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"sync"

	"github.com/spiffe/spire/proto/api/workload"
	"go.uber.org/zap"
)

const (
	// DefaultAgentAddress is the default GRPC address to contact the spire agent at.
	DefaultAgentAddress = "unix:///tmp/agent.sock"
)

// X509SVIDs is an X.509 SVID response from the SPIFFE Workload API.
type X509SVIDs struct {
	// SVIDs is a list of X.509 SVIDs.
	SVIDs []*X509SVID
}

// Default returns the default SVID (the first in the list).
func (x *X509SVIDs) Default() *X509SVID {
	return x.SVIDs[0]
}

// SVID is an X.509 SPIFFE Verifiable Identity Document.
//
// See https://github.com/spiffe/spiffe/blob/master/standards/X509-SVID.md
type X509SVID struct {
	SPIFFEID       string
	PrivateKey     crypto.Signer
	Certificates   []*x509.Certificate
	TLSCertificate tls.Certificate
	TrustBundle    *x509.CertPool
}

// UpdateType is a type of update that can be sent to a watcher.
type UpdateType int

const (
	UpdateSuccess UpdateType = iota + 1
	UpdateError
	StreamEstablished
	StreamError
)

type X509SVIDsUpdate struct {
	Type  UpdateType
	Res   *X509SVIDs
	Error error
}

// WorkloadIdentityWatcher is implemented by consumers who wish to be updated on SVID changes.
type WorkloadIdentityWatcher interface {
	// UpdateX509SVIDs indicates to the Watcher that the SVID has been updated
	UpdateX509SVIDs(*X509SVIDsUpdate)
}

// Client interacts with the SPIFFE Workload API.
type Client struct {
	logger        *zap.Logger
	watcher       WorkloadIdentityWatcher
	addr          string
	wg            sync.WaitGroup
	updateChan    chan *X509SVIDsUpdate
	reader        *streamReader
	ctx           context.Context
	cancelFn      func()
	streamManager *streamManager
}

// Option configures the workload client.
type Option func(*Client)

// Addr specifies the unix socket address of the SPIFFE agent.
func Addr(addr string) Option {
	return func(w *Client) {
		w.addr = addr
	}
}

// Logger specifies the logger to use.
func Logger(logger *zap.Logger) Option {
	return func(w *Client) {
		w.logger = logger
	}
}

// NewClient returns a new Workload API client.
func NewClient(watcher WorkloadIdentityWatcher, opts ...Option) (*Client, error) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &Client{
		logger:     zap.L(),
		addr:       DefaultAgentAddress,
		watcher:    watcher,
		updateChan: make(chan *X509SVIDsUpdate, 1),
		ctx:        ctx,
		cancelFn:   cancel,
	}
	for _, opt := range opts {
		opt(w)
	}
	w.streamManager = newStreamManager(ctx, w.logger, w.addr, w.updateChan)
	w.reader = newStreamReader(ctx, w.logger, w.streamManager)
	return w, nil
}

// Start starts the client.
//
// This blocks on setting up an initial connection to a SPIFFE Workload API.
// The passed context may be used to set a deadline for this setup.
func (c *Client) Start(ctx context.Context) error {
	c.wg.Add(1)
	if err := c.streamManager.Start(ctx); err != nil {
		return err
	}
	go c.run(c.ctx)
	return nil
}

// Stop stops the client.
func (c *Client) Stop() error {
	c.cancelFn()
	c.wg.Wait()
	return nil
}

func (c *Client) run(ctx context.Context) {
	defer c.wg.Done()

	c.handleUpdates(ctx)

	c.reader.Stop()
	if c.updateChan != nil {
		close(c.updateChan)
		c.logger.Debug("Emptying update chan.", zap.Int("queued", len(c.updateChan)))
		for range c.updateChan {
		}
		c.updateChan = nil
	}
}

func (c *Client) handleUpdates(ctx context.Context) {
	for {
		select {
		case resp, ok := <-c.reader.Chan():
			if ok {
				c.onReceive(resp)
			}
		case res, ok := <-c.updateChan:
			if ok {
				c.watcher.UpdateX509SVIDs(res)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (c *Client) onReceive(resp *workload.X509SVIDResponse) {
	res, err := protoToX509SVIDs(resp)
	if err != nil {
		c.watcher.UpdateX509SVIDs(&X509SVIDsUpdate{
			Type:  UpdateError,
			Error: err,
		})
		return
	}
	c.watcher.UpdateX509SVIDs(&X509SVIDsUpdate{
		Type: UpdateSuccess,
		Res:  res,
	})
}
