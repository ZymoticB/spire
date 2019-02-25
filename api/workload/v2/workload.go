package workload

import (
	"context"
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/spiffe/spire/proto/api/workload"
)

const (
	// DefaultAgentAddress is the default GRPC address to contact the spire agent at.
	DefaultAgentAddress = "unix:///tmp/agent.sock"
)

// X509SVIDs is an X.509 SVID response from the SPIFFE Workload API.
type X509SVIDs struct {
	// SVIDs is a list of X509SVID messages, each of which includes a single
	// SPIFFE Verifiable Identity Document, along with its private key and bundle.
	SVIDs []*X509SVID

	// CRL is a list of revoked certificates.
	// Unimplemented.
	CRL *pkix.CertificateList

	// FederatedBundles are CA certificate bundles belonging to foreign Trust Domains
	// that the workload should trust, keyed by the SPIFFE ID of the foreign domain.
	// Unimplemented.
	FederatedBundles map[string]*x509.CertPool
}

// Default returns the default SVID (the first in the list).
//
// See the SPIFFE Workload API standard Section 5.3
// (https://github.com/spiffe/spiffe/blob/master/standards/SPIFFE_Workload_API.md#53-default-identity)
func (x *X509SVIDs) Default() *X509SVID {
	return x.SVIDs[0]
}

// SVID is an X.509 SPIFFE Verifiable Identity Document.
//
// See https://github.com/spiffe/spiffe/blob/master/standards/X509-SVID.md
type X509SVID struct {
	SPIFFEID     string
	PrivateKey   crypto.Signer
	Certificates []*x509.Certificate
	TrustBundle  *x509.CertPool
}

// WorkloadIdentityWatcher is implemented by consumers who wish to be updated on SVID changes.
type WorkloadIdentityWatcher interface {
	// UpdateX509SVIDs indicates to the Watcher that the SVID has been updated
	UpdateX509SVIDs(*X509SVIDs)

	// OnError indicates an error occurred.
	OnError(err error)

	// OnConnection indicates a change in the connection state to the SPIFFE agent.
	OnConnection(connected bool)
}

// Client interacts with the SPIFFE Workload API.
type Client struct {
	logger        *logrus.Logger
	watcher       WorkloadIdentityWatcher
	addr          string
	wg            sync.WaitGroup
	reader        *streamReader
	ctx           context.Context
	cancelFn      func()
	streamManager *streamManager
	forceStart    bool
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
func Logger(logger *logrus.Logger) Option {
	return func(w *Client) {
		w.logger = logger
	}
}

// ForceStart means that the client will boot even if it can't connect to the
// SPIFFE Workload API. It will continue to attempt to connect in the background.
func ForceStart() Option {
	return func(w *Client) {
		w.forceStart = true
	}
}

// NewClient returns a new Workload API client.
func NewClient(watcher WorkloadIdentityWatcher, opts ...Option) (*Client, error) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &Client{
		logger:   logrus.StandardLogger(),
		addr:     DefaultAgentAddress,
		watcher:  watcher,
		ctx:      ctx,
		cancelFn: cancel,
	}
	for _, opt := range opts {
		opt(w)
	}
	streamManager, err := newStreamManager(ctx, w.logger, w.addr, w.forceStart)
	if err != nil {
		return nil, err
	}
	w.streamManager = streamManager
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
		c.cancelFn()
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
}

func (c *Client) handleUpdates(ctx context.Context) {
	for {
		select {
		case resp, ok := <-c.reader.SVIDChan:
			if ok {
				c.onReceive(resp)
			}
		case connected, ok := <-c.streamManager.ConnectionChan:
			if ok {
				c.watcher.OnConnection(connected)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (c *Client) onReceive(resp *workload.X509SVIDResponse) {
	res, err := protoToX509SVIDs(resp)
	if err != nil {
		c.watcher.OnError(err)
		return
	}
	c.watcher.UpdateX509SVIDs(res)
}
