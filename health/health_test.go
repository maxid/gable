package health_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maxid/gable/health"
)

// A dependency failing must not make the process look wedged: liveness stays up while
// readiness goes down, which is the difference between "restart it" and "stop routing".
func TestReadinessFailsWithoutLiveness(t *testing.T) {
	registry := health.New()
	registry.SetStarted(true)
	registry.Add("database", true, func(context.Context) error { return errors.New("connection refused") })

	ready := registry.Ready(context.Background())
	if ready.OK() {
		t.Fatalf("readiness = %+v, want down", ready)
	}
	if len(ready.Checks) != 1 || ready.Checks[0].Error != "connection refused" {
		t.Fatalf("checks = %+v", ready.Checks)
	}
	if !registry.Live(context.Background()).OK() {
		t.Fatal("liveness must not depend on a check")
	}
}

// A non-critical check is reported but does not take the whole instance out of rotation.
func TestNonCriticalFailureKeepsReadinessUp(t *testing.T) {
	registry := health.New()
	registry.SetStarted(true)
	registry.Add("cache", false, func(context.Context) error { return errors.New("cache down") })
	registry.Add("database", true, func(context.Context) error { return nil })

	report := registry.Ready(context.Background())
	if !report.OK() {
		t.Fatalf("report = %+v, want up", report)
	}
	if len(report.Checks) != 2 || report.Checks[0].Name != "cache" || report.Checks[0].Status != health.StatusDown {
		t.Fatalf("checks = %+v", report.Checks)
	}
}

// Before Start finishes, nothing may be routed to the process, even with every
// dependency healthy.
func TestReadinessWaitsForStart(t *testing.T) {
	registry := health.New()
	registry.Add("database", true, func(context.Context) error { return nil })
	if registry.Ready(context.Background()).OK() {
		t.Fatal("a process that has not started must not be ready")
	}
	registry.SetStarted(true)
	if !registry.Ready(context.Background()).OK() {
		t.Fatal("a started process with healthy checks must be ready")
	}
	registry.SetStarted(false)
	if registry.Live(context.Background()).OK() || registry.Ready(context.Background()).OK() {
		t.Fatal("a stopped process must be neither live nor ready")
	}
}

// A probe that panics is a failing probe, not a crashed process.
func TestPanickingCheckIsAFailure(t *testing.T) {
	registry := health.New()
	registry.SetStarted(true)
	registry.Add("broken", true, func(context.Context) error { panic("boom") })

	report := registry.Ready(context.Background())
	if report.OK() {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(report.Checks[0].Error, "boom") {
		t.Fatalf("error = %q", report.Checks[0].Error)
	}
}

// The report is ordered by name, so two probes of the same process agree byte for byte.
func TestReportIsDeterministic(t *testing.T) {
	registry := health.New()
	registry.SetStarted(true)
	for _, name := range []string{"zeta", "alpha", "mid"} {
		time.Sleep(time.Millisecond)
		registry.Add(name, true, func(context.Context) error { return nil })
	}
	report := registry.Ready(context.Background())
	var names []string
	for _, check := range report.Checks {
		names = append(names, check.Name)
	}
	if strings.Join(names, ",") != "alpha,mid,zeta" {
		t.Fatalf("order = %v", names)
	}
}

func TestRemoveAndReplaceChecks(t *testing.T) {
	registry := health.New()
	registry.SetStarted(true)
	registry.Add("cache", true, func(context.Context) error { return errors.New("down") })
	registry.Add("cache", true, func(context.Context) error { return nil })
	if report := registry.Ready(context.Background()); !report.OK() || len(report.Checks) != 1 {
		t.Fatalf("replaced check = %+v", report)
	}
	registry.Remove("cache")
	if report := registry.Ready(context.Background()); !report.OK() || len(report.Checks) != 0 {
		t.Fatalf("removed check = %+v", report)
	}
	// An empty name or a nil function is ignored rather than registered as a probe that
	// can never fail.
	registry.Add("", true, func(context.Context) error { return nil })
	registry.Add("nothing", true, nil)
	if report := registry.Ready(context.Background()); len(report.Checks) != 0 {
		t.Fatalf("invalid checks = %+v", report)
	}
}
