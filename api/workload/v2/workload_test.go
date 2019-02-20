package workload

import (
	"context"
	"crypto/x509"
	"errors"
	"io/ioutil"
	"net"
	"os"
	"path"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spiffe/spire/proto/api/workload"
	"github.com/spiffe/spire/test/util"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestClientStart(t *testing.T) {
	w := &testWatcher{}
	_, err := NewClient(w, Addr("notexists"), Logger(logrus.StandardLogger()))
	require.EqualError(t, err, `spiffe/workload: agent address "notexists" is not a unix address`)
}

func TestClientUpdate(t *testing.T) {
	sockPath, handler, cleanup := newStubbedAPI(t)
	defer cleanup()

	w := newTestWatcher(t)
	c, err := NewClient(w, Addr("unix:///"+sockPath), Logger(logrus.StandardLogger()))
	require.NoError(t, err)

	err = c.Start(context.Background())
	require.NoError(t, err)

	t.Run("connect and update", func(t *testing.T) {
		handler.SendX509Response("spiffe://example.org/foo")
		handler.WaitForCall()
		w.WaitForUpdates(2)

		require.Len(t, w.Connections, 1)
		require.Len(t, w.Errors, 0)
		require.Equal(t, true, w.Connections[0])
		require.Len(t, w.X509SVIDs, 1)
		require.Equal(t, "spiffe://example.org/foo", w.X509SVIDs[0].Default().SPIFFEID)
		w.X509SVIDs = nil
		w.Connections = nil
	})

	t.Run("new update", func(t *testing.T) {
		handler.SendX509Response("spiffe://example.org/bar")
		handler.WaitForCall()
		w.WaitForUpdates(1)

		require.Len(t, w.Connections, 0)
		require.Len(t, w.X509SVIDs, 1)
		require.Equal(t, "spiffe://example.org/bar", w.X509SVIDs[0].Default().SPIFFEID)
		require.Len(t, w.Errors, 0)
		w.X509SVIDs = nil
	})

	t.Run("stop", func(t *testing.T) {
		err = c.Stop()
		require.NoError(t, err)
		require.Len(t, w.Connections, 0)
		require.Len(t, w.X509SVIDs, 0)
		require.Len(t, w.Errors, 0)
	})
}

type testWatcher struct {
	t            *testing.T
	X509SVIDs    []*X509SVIDs
	Errors       []error
	Connections  []bool
	updateSignal chan struct{}
	n            int
	timeout      time.Duration
}

func newTestWatcher(t *testing.T) *testWatcher {
	return &testWatcher{
		t:            t,
		updateSignal: make(chan struct{}, 100),
		timeout:      10 * time.Second,
	}
}

func (w *testWatcher) UpdateX509SVIDs(u *X509SVIDs) {
	w.X509SVIDs = append(w.X509SVIDs, u)
	w.n++
	w.updateSignal <- struct{}{}
}

func (w *testWatcher) OnError(err error) {
	w.Errors = append(w.Errors, err)
	w.n++
	w.updateSignal <- struct{}{}
}

func (w *testWatcher) OnConnection(connected bool) {
	w.Connections = append(w.Connections, connected)
	w.n++
	w.updateSignal <- struct{}{}
}

func (w *testWatcher) WaitForUpdates(expectedNumUpdates int) {
	numUpdates := 0
	for {
		select {
		case <-w.updateSignal:
			numUpdates++
		case <-time.After(w.timeout):
			require.Fail(w.t, "Timeout exceeding waiting for updates.")
		}
		if numUpdates == expectedNumUpdates {
			return
		}
	}
}

func newStubbedAPI(t *testing.T) (socketPath string, _ *mockHandler, cleanup func()) {
	dir, err := ioutil.TempDir("", "workload-test")
	require.NoError(t, err)

	sockPath := path.Join(dir, "workload_api.sock")
	l, err := net.Listen("unix", sockPath)
	require.NoError(t, err)

	s := grpc.NewServer()
	h := &mockHandler{
		t:                t,
		done:             make(chan struct{}),
		fetchX509Waiter:  make(chan struct{}, 1),
		sendX509Response: make(chan string),
	}
	workload.RegisterSpiffeWorkloadAPIServer(s, h)
	go func() { s.Serve(l) }()

	// Let grpc server initialize
	time.Sleep(1 * time.Millisecond)
	return sockPath, h, func() {
		h.Stop()
		s.Stop()
		os.RemoveAll(path.Dir(sockPath))
	}
}

type mockHandler struct {
	t                *testing.T
	done             chan struct{}
	fetchX509Waiter  chan struct{}
	sendX509Response chan string
}

func (m *mockHandler) Stop() { close(m.done) }

func (m *mockHandler) SendX509Response(name string) {
	m.sendX509Response <- name
}

func (m *mockHandler) WaitForCall() {
	<-m.fetchX509Waiter
}

func (m *mockHandler) FetchX509SVID(_ *workload.X509SVIDRequest, stream workload.SpiffeWorkloadAPI_FetchX509SVIDServer) error {
	m.t.Run("check security header", func(t *testing.T) {
		md, ok := metadata.FromIncomingContext(stream.Context())
		require.True(t, ok, "Request doesn't contain grpc metadata.")
		require.Len(t, md.Get("workload.spiffe.io"), 1)
		require.Equal(t, "true", md.Get("workload.spiffe.io")[0])
	})

	for {
		select {
		case name := <-m.sendX509Response:
			stream.Send(m.resp(name))
			m.fetchX509Waiter <- struct{}{}
		case <-m.done:
			return nil

		}
	}
}

func (m *mockHandler) resp(name string) *workload.X509SVIDResponse {
	svid, key, err := util.LoadSVIDFixture()
	require.NoError(m.t, err)
	ca, _, err := util.LoadCAFixture()
	require.NoError(m.t, err)

	keyData, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(m.t, err)

	svidMsg := &workload.X509SVID{
		SpiffeId:    name,
		X509Svid:    svid.Raw,
		X509SvidKey: keyData,
		Bundle:      ca.Raw,
	}
	return &workload.X509SVIDResponse{
		Svids: []*workload.X509SVID{svidMsg},
	}
}

func (m *mockHandler) FetchJWTSVID(context.Context, *workload.JWTSVIDRequest) (*workload.JWTSVIDResponse, error) {
	return nil, errors.New("unimplemented")
}

func (m *mockHandler) FetchJWTBundles(*workload.JWTBundlesRequest, workload.SpiffeWorkloadAPI_FetchJWTBundlesServer) error {
	return errors.New("unimplemented")
}

func (m *mockHandler) ValidateJWTSVID(context.Context, *workload.ValidateJWTSVIDRequest) (*workload.ValidateJWTSVIDResponse, error) {
	return nil, errors.New("unimplemented")
}
