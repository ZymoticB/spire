package telemetry

import (
	"context"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// TODO: Write tests
func TestNewM3Runner(t *testing.T) {
	config := testM3Config()
	runner, err := newM3Runner(config)
	require.Nil(t, err)
	assert.True(t, runner.isConfigured())

	config.FileConfig.M3 = []M3Config{}
	runner, err = newM3Runner(config)
	require.Nil(t, err)
	assert.False(t, runner.isConfigured())
}

func TestMultipleM3Sinks(t *testing.T) {
	config := testM3Config()
	sink2 := M3Config {
		Address: "localhost:9002",
		Env:     "test2",
	}

	config.FileConfig.M3 = append(config.FileConfig.M3, sink2)
	runner, err := newM3Runner(config)
	require.Nil(t, err)
	assert.Equal(t, 2, len(runner.sinks()))
}

func TestRunM3(t *testing.T) {
	config := testM3Config()

	pr, err := newM3Runner(config)
	require.NoError(t, err)

	errCh := make(chan error)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		errCh <- pr.run(ctx)
	}()

	// It stops when it's supposed to
	cancel()
	select {
	case err := <-errCh:
		assert.Equal(t, context.Canceled, err)
	case <-time.After(time.Minute):
		t.Fatal("timeout waiting for shutdown")
	}

	config.FileConfig.M3 = nil
	pr, err = newM3Runner(config)
	require.NoError(t, err)

	go func() {
		errCh <- pr.run(context.Background())
	}()

	// It doesn't run if it's not configured
	select {
	case err := <-errCh:
		assert.Nil(t, err, "should be nil if not configured")
	case <-time.After(time.Minute):
		t.Fatal("m3 running but not configured")
	}
}

func testM3Config() *MetricsConfig {
	l, _ := test.NewNullLogger()

	return &MetricsConfig{
		Logger:      l,
		ServiceName: "foo",
		FileConfig: FileConfig{
			M3: []M3Config {
				{
					Address: "localhost:9001",
					Env: "test",
				},
			},
		},
	}
}