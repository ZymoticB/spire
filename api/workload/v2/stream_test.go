package workload

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestStreamManagerValidatesPath(t *testing.T) {
	_, err := newStreamManager(context.Background(), logrus.StandardLogger(), "badpath", false /* forceStart */)
	require.Error(t, err)
	require.Contains(t, err.Error(), `spiffe/workload: agent address "badpath" is not a unix address`)
}

func TestStreamManagerStartStop(t *testing.T) {
	sockPath, handler, cleanup := newStubbedAPI(t)
	defer cleanup()

	streamCtx, streamCtxCancel := context.WithCancel(context.Background())
	sm, err := newStreamManager(streamCtx, logrus.StandardLogger(), "unix:///"+sockPath, false /* forceStart */)
	require.NoError(t, err)
	require.NotNil(t, sm)

	dialCtx, dialCtxCancel := context.WithCancel(context.Background())
	err = sm.Start(dialCtx)
	require.NoError(t, err)

	stream := <-sm.StreamChan
	require.NotNil(t, stream)
	require.True(t, <-sm.ConnectionChan)

	// cancel the dial context and ensure we can read from the stream even if the dial context is canceled
	dialCtxCancel()
	go handler.SendX509Response("spiffe://example.org/foo")
	_, err = stream.Recv(context.Background())
	require.NoError(t, err)
	handler.WaitForCall()

	err = stream.Close()
	require.NoError(t, err)
	streamCtxCancel()
	sm.Stop()
}
func TestStreamManagerForceStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sm, err := newStreamManager(ctx, logrus.StandardLogger(), "unix:///nonexistent-path-foober", true /* forceStart */)
	require.NoError(t, err)

	// start with a cancelled context succeeds
	cancel()
	require.NoError(t, sm.Start(ctx))

	sm.Stop()
}
