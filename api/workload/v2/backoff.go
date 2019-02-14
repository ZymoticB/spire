package workload

import (
	"math"
	"time"
)

// backoff defines an exponential backoff policy.
type backoff struct {
	InitialDelay time.Duration
	MaxDelay     time.Duration
	n            int
}

func newBackoff() *backoff {
	return &backoff{
		InitialDelay: time.Second,
		MaxDelay:     30 * time.Second,
		n:            0,
	}
}

// Duration returns the next wait period for the backoff. Not goroutine-safe.
func (b *backoff) Duration() time.Duration {
	backoff := math.Pow(2, float64(b.n))
	d := math.Min(b.InitialDelay.Seconds()*backoff, b.MaxDelay.Seconds())
	b.n++
	return time.Duration(d) * time.Second
}

// Reset resets the backoff's state.
func (b *backoff) Reset() {
	b.n = 0
}
