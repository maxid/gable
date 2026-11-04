// Package metrics is a small, dependency-free registry that renders Prometheus text.
//
// It exists because a service that cannot say how many requests it served, how long they
// took and how many failed is not operable — and because depending on a full client
// library would decide the exporter for every application. The output is the Prometheus
// text exposition format, which every scraper and gateway understands.
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// DefaultBuckets are the latency buckets used when none are given, in seconds. They span
// the range a request-serving process actually lives in.
var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Registry holds the instruments of one process. It is safe for concurrent use.
type Registry struct {
	mu           sync.RWMutex
	counters     map[string]*Counter
	gauges       map[string]*Gauge
	histograms   map[string]*Histogram
	counterFuncs map[string]*metricFunc
	gaugeFuncs   map[string]*metricFunc
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{
		counters:     map[string]*Counter{},
		gauges:       map[string]*Gauge{},
		histograms:   map[string]*Histogram{},
		counterFuncs: map[string]*metricFunc{},
		gaugeFuncs:   map[string]*metricFunc{},
	}
}

// Counter returns a monotonically increasing counter, creating it on first use with the
// same arguments. Reusing a name with different labels panics: two instruments sharing a
// name would render into one metric with mismatched labels, which the scraper rejects.
func (r *Registry) Counter(name, help string, labels ...string) *Counter {
	mustValidate(name, labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.counters[name]; ok {
		existing.mustMatch(labels)
		return existing
	}
	counter := &Counter{name: name, help: help, labels: labels, series: map[string]*counterSeries{}}
	r.counters[name] = counter
	return counter
}

// Gauge returns a value that can go up and down.
func (r *Registry) Gauge(name, help string, labels ...string) *Gauge {
	mustValidate(name, labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.gauges[name]; ok {
		existing.mustMatch(labels)
		return existing
	}
	gauge := &Gauge{name: name, help: help, labels: labels, series: map[string]*gaugeSeries{}}
	r.gauges[name] = gauge
	return gauge
}

// Histogram returns a bucketed distribution, for latencies and sizes.
func (r *Registry) Histogram(name, help string, buckets []float64, labels ...string) *Histogram {
	mustValidate(name, labels)
	if len(buckets) == 0 {
		buckets = DefaultBuckets
	}
	sorted := append([]float64(nil), buckets...)
	sort.Float64s(sorted)

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.histograms[name]; ok {
		existing.mustMatch(labels)
		return existing
	}
	histogram := &Histogram{name: name, help: help, labels: labels, buckets: sorted, series: map[string]*histogramSeries{}}
	r.histograms[name] = histogram
	return histogram
}

// CounterFunc registers a value read at scrape time. It is for snapshots owned
// by another component (cache counters, queue depth) that should not require a
// background goroutine. fn must be cheap and must not call back into the
// registry; a panic is contained and rendered as zero.
func (r *Registry) CounterFunc(name, help string, fn func() float64) {
	mustValidate(name, nil)
	if fn == nil {
		panic("metrics: nil CounterFunc")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.counters[name]; exists {
		panic(fmt.Sprintf("metrics: counter %q already registered", name))
	}
	if _, exists := r.counterFuncs[name]; exists {
		panic(fmt.Sprintf("metrics: counter %q already registered", name))
	}
	if _, exists := r.gauges[name]; exists {
		panic(fmt.Sprintf("metrics: gauge %q already registered", name))
	}
	if _, exists := r.gaugeFuncs[name]; exists {
		panic(fmt.Sprintf("metrics: gauge %q already registered", name))
	}
	r.counterFuncs[name] = &metricFunc{name: name, help: help, fn: fn, counter: true}
}

// GaugeFunc registers a value read at scrape time. See CounterFunc for the
// callback contract.
func (r *Registry) GaugeFunc(name, help string, fn func() float64) {
	mustValidate(name, nil)
	if fn == nil {
		panic("metrics: nil GaugeFunc")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.gauges[name]; exists {
		panic(fmt.Sprintf("metrics: gauge %q already registered", name))
	}
	if _, exists := r.gaugeFuncs[name]; exists {
		panic(fmt.Sprintf("metrics: gauge %q already registered", name))
	}
	if _, exists := r.counters[name]; exists {
		panic(fmt.Sprintf("metrics: counter %q already registered", name))
	}
	if _, exists := r.counterFuncs[name]; exists {
		panic(fmt.Sprintf("metrics: counter %q already registered", name))
	}
	r.gaugeFuncs[name] = &metricFunc{name: name, help: help, fn: fn}
}

// Handler serves every instrument in the Prometheus text format.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = io.WriteString(w, r.Render())
	})
}

// Render returns the exposition text.
func (r *Registry) Render() string {
	r.mu.RLock()
	counters := make([]*Counter, 0, len(r.counters))
	for _, counter := range r.counters {
		counters = append(counters, counter)
	}
	gauges := make([]*Gauge, 0, len(r.gauges))
	for _, gauge := range r.gauges {
		gauges = append(gauges, gauge)
	}
	histograms := make([]*Histogram, 0, len(r.histograms))
	for _, histogram := range r.histograms {
		histograms = append(histograms, histogram)
	}
	counterFuncs := make([]*metricFunc, 0, len(r.counterFuncs))
	for _, fn := range r.counterFuncs {
		counterFuncs = append(counterFuncs, fn)
	}
	gaugeFuncs := make([]*metricFunc, 0, len(r.gaugeFuncs))
	for _, fn := range r.gaugeFuncs {
		gaugeFuncs = append(gaugeFuncs, fn)
	}
	r.mu.RUnlock()

	sort.Slice(counters, func(i, j int) bool { return counters[i].name < counters[j].name })
	sort.Slice(gauges, func(i, j int) bool { return gauges[i].name < gauges[j].name })
	sort.Slice(histograms, func(i, j int) bool { return histograms[i].name < histograms[j].name })
	sort.Slice(counterFuncs, func(i, j int) bool { return counterFuncs[i].name < counterFuncs[j].name })
	sort.Slice(gaugeFuncs, func(i, j int) bool { return gaugeFuncs[i].name < gaugeFuncs[j].name })

	var builder strings.Builder
	for _, counter := range counters {
		counter.render(&builder)
	}
	for _, gauge := range gauges {
		gauge.render(&builder)
	}
	for _, histogram := range histograms {
		histogram.render(&builder)
	}
	for _, fn := range counterFuncs {
		fn.render(&builder)
	}
	for _, fn := range gaugeFuncs {
		fn.render(&builder)
	}
	return builder.String()
}

type metricFunc struct {
	name    string
	help    string
	fn      func() float64
	counter bool
}

func (f *metricFunc) render(builder *strings.Builder) {
	kind := "gauge"
	if f.counter {
		kind = "counter"
	}
	fmt.Fprintf(builder, "# HELP %s %s\n# TYPE %s %s\n", f.name, f.help, f.name, kind)
	builder.WriteString(f.name + " " + formatValue(safeValue(f.fn)) + "\n")
}

func safeValue(fn func() float64) (value float64) {
	defer func() { _ = recover() }()
	return fn()
}

// Counter is a monotonically increasing value.
type Counter struct {
	name   string
	help   string
	labels []string

	mu     sync.Mutex
	series map[string]*counterSeries
}

type counterSeries struct {
	values []string
	total  uint64
}

// Inc adds one.
func (c *Counter) Inc() { c.Add(1) }

// Add adds n.
func (c *Counter) Add(n uint64) { c.With().Add(n) }

// With returns the series for these label values.
func (c *Counter) With(values ...string) *CounterSeries {
	return &CounterSeries{counter: c, key: seriesKey(c.labels, values)}
}

func (c *Counter) seriesFor(values []string) *counterSeries {
	key := seriesKey(c.labels, values)
	c.mu.Lock()
	defer c.mu.Unlock()
	if series, ok := c.series[key]; ok {
		return series
	}
	series := &counterSeries{values: append([]string(nil), values...)}
	c.series[key] = series
	return series
}

func (c *Counter) render(builder *strings.Builder) {
	fmt.Fprintf(builder, "# HELP %s %s\n# TYPE %s counter\n", c.name, c.help, c.name)
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, line := range sortedSeries(c.series, c.labels, func(series *counterSeries) string {
		return fmt.Sprintf("%d", series.total)
	}) {
		builder.WriteString(c.name + line)
	}
}

// CounterSeries is one label combination of a counter.
type CounterSeries struct {
	counter *Counter
	key     string
}

// Inc adds one.
func (c *CounterSeries) Inc() { c.Add(1) }

// Add adds n.
func (c *CounterSeries) Add(n uint64) {
	c.counter.mu.Lock()
	defer c.counter.mu.Unlock()
	series, ok := c.counter.series[c.key]
	if !ok {
		// The key encodes the values; recover them so the first Add creates the series.
		values := strings.Split(c.key, "\x00")
		if len(c.counter.labels) == 0 {
			values = nil
		}
		series = &counterSeries{values: values}
		c.counter.series[c.key] = series
	}
	series.total += n
}

// Gauge is a value that can go up and down.
type Gauge struct {
	name   string
	help   string
	labels []string

	mu     sync.Mutex
	series map[string]*gaugeSeries
}

type gaugeSeries struct {
	values []string
	number float64
}

// Set replaces the value.
func (g *Gauge) Set(v float64) { g.With().Set(v) }

// Add adds delta.
func (g *Gauge) Add(delta float64) { g.With().Add(delta) }

// Inc adds one.
func (g *Gauge) Inc() { g.With().Add(1) }

// Dec removes one.
func (g *Gauge) Dec() { g.With().Add(-1) }

// With returns the series for these label values.
func (g *Gauge) With(values ...string) *GaugeSeries {
	return &GaugeSeries{gauge: g, key: seriesKey(g.labels, values)}
}

func (g *Gauge) render(builder *strings.Builder) {
	fmt.Fprintf(builder, "# HELP %s %s\n# TYPE %s gauge\n", g.name, g.help, g.name)
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, line := range sortedSeries(g.series, g.labels, func(series *gaugeSeries) string {
		return formatValue(series.number)
	}) {
		builder.WriteString(g.name + line)
	}
}

// GaugeSeries is one label combination of a gauge.
type GaugeSeries struct {
	gauge *Gauge
	key   string
}

// Set replaces the value.
func (g *GaugeSeries) Set(v float64) {
	g.gauge.mu.Lock()
	defer g.gauge.mu.Unlock()
	series, ok := g.gauge.series[g.key]
	if !ok {
		values := strings.Split(g.key, "\x00")
		if len(g.gauge.labels) == 0 {
			values = nil
		}
		series = &gaugeSeries{values: values}
		g.gauge.series[g.key] = series
	}
	series.number = v
}

// Add adds delta.
func (g *GaugeSeries) Add(delta float64) {
	g.gauge.mu.Lock()
	defer g.gauge.mu.Unlock()
	series, ok := g.gauge.series[g.key]
	if !ok {
		values := strings.Split(g.key, "\x00")
		if len(g.gauge.labels) == 0 {
			values = nil
		}
		series = &gaugeSeries{values: values}
		g.gauge.series[g.key] = series
	}
	series.number += delta
}

// Histogram is a bucketed distribution. Buckets are cumulative, as the exposition format
// requires.
type Histogram struct {
	name    string
	help    string
	labels  []string
	buckets []float64

	mu     sync.Mutex
	series map[string]*histogramSeries
}

type histogramSeries struct {
	values []string
	counts []uint64
	total  uint64
	sum    float64
}

// Observe records one sample.
func (h *Histogram) Observe(seconds float64) { h.With().Observe(seconds) }

// With returns the series for these label values.
func (h *Histogram) With(values ...string) *HistogramSeries {
	return &HistogramSeries{histogram: h, key: seriesKey(h.labels, values)}
}

func (h *Histogram) render(builder *strings.Builder) {
	fmt.Fprintf(builder, "# HELP %s %s\n# TYPE %s histogram\n", h.name, h.help, h.name)
	h.mu.Lock()
	defer h.mu.Unlock()
	keys := make([]string, 0, len(h.series))
	for key := range h.series {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		series := h.series[key]
		var cumulative uint64
		for i, bucket := range h.buckets {
			cumulative += series.counts[i]
			fmt.Fprintf(builder, "%s_bucket%s %d\n", h.name, formatLabels(h.labels, series.values, "le", formatValue(bucket)), cumulative)
		}
		fmt.Fprintf(builder, "%s_bucket%s %d\n", h.name, formatLabels(h.labels, series.values, "le", "+Inf"), series.total)
		fmt.Fprintf(builder, "%s_sum%s %s\n", h.name, formatLabels(h.labels, series.values), formatValue(series.sum))
		fmt.Fprintf(builder, "%s_count%s %d\n", h.name, formatLabels(h.labels, series.values), series.total)
	}
}

// HistogramSeries is one label combination of a histogram.
type HistogramSeries struct {
	histogram *Histogram
	key       string
}

// Observe records one sample.
func (h *HistogramSeries) Observe(seconds float64) {
	h.histogram.mu.Lock()
	defer h.histogram.mu.Unlock()
	series, ok := h.histogram.series[h.key]
	if !ok {
		values := strings.Split(h.key, "\x00")
		if len(h.histogram.labels) == 0 {
			values = nil
		}
		series = &histogramSeries{values: values, counts: make([]uint64, len(h.histogram.buckets))}
		h.histogram.series[h.key] = series
	}
	// Count the observation in the first bucket that contains it; render sums the
	// counts cumulatively. Counting every bucket it fits would double count at render
	// time, which is the classic way a histogram reports more samples than it saw.
	for i, bucket := range h.histogram.buckets {
		if seconds <= bucket {
			series.counts[i]++
			break
		}
	}
	series.total++
	series.sum += seconds
}

// seriesKey joins label values; the separator cannot appear in a label value.
func seriesKey(_ []string, values []string) string { return strings.Join(values, "\x00") }

// sortedSeries renders one instrument's series in a stable order.
func sortedSeries[S interface{ valuesOf() []string }](series map[string]S, labels []string, value func(S) string) []string {
	keys := make([]string, 0, len(series))
	for key := range series {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		item := series[key]
		lines = append(lines, formatLabels(labels, item.valuesOf())+" "+value(item)+"\n")
	}
	return lines
}

func (c *counterSeries) valuesOf() []string   { return c.values }
func (g *gaugeSeries) valuesOf() []string     { return g.values }
func (h *histogramSeries) valuesOf() []string { return h.values }

// formatLabels renders {k="v",...} plus optional extra pairs.
func formatLabels(names, values []string, extra ...string) string {
	pairs := make([]string, 0, len(names)+len(extra)/2)
	for i, name := range names {
		value := ""
		if i < len(values) {
			value = values[i]
		}
		pairs = append(pairs, fmt.Sprintf(`%s=%q`, name, value))
	}
	for i := 0; i+1 < len(extra); i += 2 {
		pairs = append(pairs, fmt.Sprintf(`%s=%q`, extra[i], extra[i+1]))
	}
	if len(pairs) == 0 {
		return ""
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func formatValue(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%g", v)
}

func (c *Counter) mustMatch(labels []string) { mustMatch("counter", c.name, c.labels, labels) }
func (g *Gauge) mustMatch(labels []string)   { mustMatch("gauge", g.name, g.labels, labels) }
func (h *Histogram) mustMatch(labels []string) {
	mustMatch("histogram", h.name, h.labels, labels)
}

func mustMatch(kind, name string, have, want []string) {
	if len(have) != len(want) {
		panic(fmt.Sprintf("metrics: %s %q already has labels %v, got %v", kind, name, have, want))
	}
	for i := range have {
		if have[i] != want[i] {
			panic(fmt.Sprintf("metrics: %s %q already has labels %v, got %v", kind, name, have, want))
		}
	}
}

func mustValidate(name string, labels []string) {
	if err := validName(name); err != nil {
		panic(err)
	}
	for _, label := range labels {
		if err := validName(label); err != nil {
			panic(err)
		}
	}
}

func validName(name string) error {
	if name == "" {
		return fmt.Errorf("metrics: empty metric name")
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_', r == ':':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return fmt.Errorf("metrics: name %q must match [a-zA-Z_:][a-zA-Z0-9_:]*", name)
		}
	}
	return nil
}
