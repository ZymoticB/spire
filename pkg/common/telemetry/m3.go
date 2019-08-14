package telemetry

import (
	"context"
	"github.com/sirupsen/logrus"
	"github.com/uber-go/tally"
	"github.com/uber-go/tally/m3"
	"io"
	"strings"
	"sync"
	"time"
)

type m3Sink struct {
	closer     io.Closer
	scope      tally.Scope
}

func newM3Sink(serviceName, address, env string, logger logrus.FieldLogger) (*m3Sink, error) {
	m3Config := m3.Configuration{
		Env: env,
		HostPort: address,
		Service: serviceName,
	}

	r, err := m3Config.NewReporter()
	if err != nil {
		return nil, err
	}

	scopeOpts := tally.ScopeOptions{
		CachedReporter: r,
		Prefix: serviceName,
	}

	reportEvery := time.Second
	scope, closer := tally.NewRootScope(scopeOpts, reportEvery)
	sink := &m3Sink{
		closer:closer,
		scope:scope,
	}

	return sink, nil
}

func (m *m3Sink) SetGauge(key []string, val float32) {
	setGauge(key, val, m.scope)
}

func (m *m3Sink) SetGaugeWithLabels(key []string, val float32, labels []Label) {
	subscope := m.subscopeWithLabels(labels)
	setGauge(key, val, subscope)
}

// Not implemented for m3
func (m *m3Sink) EmitKey(key []string, val float32) {}

// Counters should accumulate values
func (m *m3Sink) IncrCounter(key []string, val float32) {
	incrCounter(key, val, m.scope)
}

func (m *m3Sink) IncrCounterWithLabels(key []string, val float32, labels []Label) {
	subscope := m.subscopeWithLabels(labels)
	incrCounter(key, val, subscope)
}

// Samples are for timing information, where quantiles are used
func (m *m3Sink) AddSample(key []string, val float32) {
	addSample(key, val, m.scope)
}

func (m *m3Sink) AddSampleWithLabels(key []string, val float32, labels []Label) {
	subscope := m.subscopeWithLabels(labels)
	addSample(key, val, subscope)
}

func (m *m3Sink) subscopeWithLabels(labels []Label) tally.Scope {
	tags := labelsToTags(labels)
	return m.scope.Tagged(tags)
}

// Flattens the key for formatting, removes spaces
func flattenKey(parts []string) string {
	joined := strings.Join(parts, ".")
	return sanitizeTagKey(joined)
}

func sanitizeTagKey(unsanitizedKey string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '+', ',', '=', ' ', ':', '|', '\n', '.':
			return '_'
		default:
			return r
		}
	}, unsanitizedKey)
}

func sanitizeNameOrTagValue(unsanitizedValue string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '+', ',', '=', ' ', ':', '|', '\n':
			return '_'
		default:
			return r
		}
	}, unsanitizedValue)
}

func labelsToTags(labels []Label) map[string]string {
	tags := make(map[string]string, len(labels))
	for _, l := range labels {
		key := sanitizeTagKey(l.Name)
		tags[key] = sanitizeNameOrTagValue(l.Value)
	}

	return tags
}

func setGauge(key []string, val float32, scope tally.Scope) {
	flattenedKey := flattenKey(key)
	gauge := scope.Gauge(flattenedKey)
	val64 := float64(val)
	gauge.Update(val64)
}

func incrCounter(key []string, val float32, scope tally.Scope) {
	flattenedKey := flattenKey(key)
	counter := scope.Counter(flattenedKey)
	val64 := int64(val)
	counter.Inc(val64)
}

func addSample(key []string, val float32, scope tally.Scope) {
	flattenedKey := flattenKey(key)
	histogram := scope.Histogram(flattenedKey, tally.DefaultBuckets)
	val64 := float64(val)
	histogram.RecordValue(val64)
}

var _ Sink = (*m3Sink)(nil)

type m3Runner struct {
	loadedSinks []*m3Sink
}

func newM3Runner(c *MetricsConfig) (sinkRunner, error) {
	runner := &m3Runner{}
	for _, conf := range c.FileConfig.M3 {
		sink, err := newM3Sink(c.ServiceName, conf.Address, conf.Env, c.Logger)
		if err != nil {
			return runner, err
		}

		runner.loadedSinks = append(runner.loadedSinks, sink)
	}

	return runner, nil
}

func (r *m3Runner) isConfigured() bool {
	return len(r.loadedSinks) > 0
}

func (r *m3Runner) sinks() []Sink {
	s := make([]Sink, len(r.loadedSinks))
	for i, v := range r.loadedSinks {
		s[i] = v
	}

	return s
}

func (r *m3Runner) run(ctx context.Context) error {
	if !r.isConfigured() {
		return nil
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		for _, s := range r.loadedSinks {
			s.closer.Close()
		}
	}()

	wg.Wait()
	return ctx.Err()
}
