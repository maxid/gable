package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxid/gable/metrics"
)

// The rendered document has to be the shape a Prometheus scraper expects: HELP, TYPE,
// one sample per line, labels quoted.
func TestRenderExpositionText(t *testing.T) {
	registry := metrics.New()
	requests := registry.Counter("http_requests_total", "Requests served.", "method", "status")
	requests.With("GET", "200").Inc()
	requests.With("GET", "200").Inc()
	requests.With("POST", "500").Inc()

	inflight := registry.Gauge("http_inflight_requests", "Requests in flight.")
	inflight.Add(1)
	inflight.Add(1)
	inflight.Add(-1)

	latency := registry.Histogram("http_request_duration_seconds", "Latency.", []float64{0.01, 0.1}, "method")
	latency.With("GET").Observe(0.005)
	latency.With("GET").Observe(0.05)
	latency.With("GET").Observe(0.5)

	text := registry.Render()
	for _, want := range []string{
		"# TYPE http_requests_total counter",
		`http_requests_total{method="GET",status="200"} 2`,
		`http_requests_total{method="POST",status="500"} 1`,
		"# TYPE http_inflight_requests gauge",
		"http_inflight_requests 1",
		"# TYPE http_request_duration_seconds histogram",
		`http_request_duration_seconds_bucket{method="GET",le="0.01"} 1`,
		`http_request_duration_seconds_bucket{method="GET",le="0.1"} 2`,
		`http_request_duration_seconds_bucket{method="GET",le="+Inf"} 3`,
		`http_request_duration_seconds_sum{method="GET"} 0.555`,
		`http_request_duration_seconds_count{method="GET"} 3`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("render is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "# TYPE http_requests_total gauge") {
		t.Fatalf("counter rendered as a gauge:\n%s", text)
	}
}

// Instruments without labels render without braces, which keeps a simple counter readable.
func TestUnlabelledInstruments(t *testing.T) {
	registry := metrics.New()
	registry.Counter("go_jobs_total", "Jobs.").Add(3)
	registry.Gauge("go_queue_depth", "Depth.").Set(7)
	text := registry.Render()
	if !strings.Contains(text, "go_jobs_total 3") || !strings.Contains(text, "go_queue_depth 7") {
		t.Fatalf("render =\n%s", text)
	}
	if strings.Contains(text, "go_jobs_total{") {
		t.Fatalf("unlabelled counter rendered braces:\n%s", text)
	}
}

// A name reused with different labels is a programming error, and it is caught when the
// instrument is requested rather than silently merged in the output.
func TestReusingNameWithOtherLabelsPanics(t *testing.T) {
	registry := metrics.New()
	registry.Counter("requests_total", "Requests.", "method").With("GET").Inc()

	defer func() {
		if recover() == nil {
			t.Fatal("a conflicting label set must panic")
		}
	}()
	registry.Counter("requests_total", "Requests.", "route")
}

func TestInvalidNamesPanic(t *testing.T) {
	for _, name := range []string{"", "has space", "1starts_with_digit"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("name %q must be rejected", name)
				}
			}()
			metrics.New().Counter(name, "help")
		}()
	}
}

// The handler is what an orchestrator scrapes.
func TestHandlerServesText(t *testing.T) {
	registry := metrics.New()
	registry.Counter("scrapes_total", "Scrapes.").Inc()
	recorder := httptest.NewRecorder()
	registry.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain") {
		t.Fatalf("content type = %q", contentType)
	}
	if !strings.Contains(recorder.Body.String(), "scrapes_total 1") {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

// Many goroutines touching the same series is the normal case; the registry must not lose
// updates or race.
func TestConcurrentUse(t *testing.T) {
	registry := metrics.New()
	counter := registry.Counter("concurrent_total", "Concurrent.", "worker")
	histogram := registry.Histogram("concurrent_seconds", "Concurrent.", nil)
	done := make(chan struct{})
	for worker := 0; worker < 8; worker++ {
		go func(worker int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 100; i++ {
				counter.With("one").Inc()
				histogram.Observe(0.001)
			}
		}(worker)
	}
	for worker := 0; worker < 8; worker++ {
		<-done
	}
	text := registry.Render()
	if !strings.Contains(text, `concurrent_total{worker="one"} 800`) {
		t.Fatalf("counter lost updates:\n%s", text)
	}
	if !strings.Contains(text, "concurrent_seconds_count 800") {
		t.Fatalf("histogram lost updates:\n%s", text)
	}
}
