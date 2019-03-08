package workload

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestStreamManagerValidatesPath(t *testing.T) {
	_, err := newStreamManager(context.Background(), logrus.StandardLogger(), "badpath")
	require.Error(t, err)
	require.Contains(t, err.Error(), `spiffe/workload: agent address "badpath" is not a unix address`)
}

func TestStreamManagerStartStop(t *testing.T) {
	sockPath, handler, cleanup := newStubbedAPI(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	sm, err := newStreamManager(ctx, logrus.StandardLogger(), "unix:///"+sockPath)
	require.NoError(t, err)
	require.NotNil(t, sm)

	sm.Start()

	stream := <-sm.StreamChan
	require.NotNil(t, stream)
	require.True(t, <-sm.ConnectionChan)

	go handler.SendX509Response("spiffe://example.org/foo")
	_, err = stream.Recv(context.Background())
	require.NoError(t, err)
	handler.WaitForCall()

	err = stream.Close()
	require.NoError(t, err)
	cancel()
	sm.Stop()
}
