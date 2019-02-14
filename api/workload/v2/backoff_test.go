package workload

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBackoff(t *testing.T) {
	b := newBackoff()
	b.InitialDelay = time.Second
	b.MaxDelay = 30 * time.Second

	t.Run("test max", func(t *testing.T) {
		require.Equal(t, time.Second, b.Duration())
		require.Equal(t, 2*time.Second, b.Duration())
		require.Equal(t, 4*time.Second, b.Duration())
		require.Equal(t, 8*time.Second, b.Duration())
		require.Equal(t, 16*time.Second, b.Duration())
		require.Equal(t, 30*time.Second, b.Duration())
	})

	t.Run("test reset", func(t *testing.T) {
		b.Reset()
		require.Equal(t, time.Second, b.Duration())
		require.Equal(t, 2*time.Second, b.Duration())
		require.Equal(t, 4*time.Second, b.Duration())
		require.Equal(t, 8*time.Second, b.Duration())
		require.Equal(t, 16*time.Second, b.Duration())
		require.Equal(t, 30*time.Second, b.Duration())
	})
}
