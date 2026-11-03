// Package health reports whether a process may receive traffic.
//
// It separates the two questions an orchestrator asks: liveness ("is this process
// wedged?") and readiness ("may it serve right now?"). Liveness never fails because of a
// dependency — a database outage must not restart a healthy process — while readiness
// does, so a load balancer can take the instance out of rotation without killing it.
package health

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Status is the outcome of one check or of a whole report.
type Status string

const (
	// StatusUp means everything answered.
	StatusUp Status = "up"
	// StatusDown means at least one check failed.
	StatusDown Status = "down"
)

// Check is one named probe. A check returning an error is down; the error text is
// reported, because "which dependency" is the only useful part of a failing readiness
// call.
type Check struct {
	Name string
	// Critical marks a check whose failure takes the whole report down. A non-critical
	// failure is reported but leaves readiness up, which is how an optional dependency
	// (a cache, a metrics sink) stops being a single point of failure.
	Critical bool
	Run      func(context.Context) error
}

// Result is one check's outcome.
type Result struct {
	Name     string `json:"name"`
	Status   Status `json:"status"`
	Critical bool   `json:"critical"`
	Error    string `json:"error,omitempty"`
	Millis   int64  `json:"millis"`
}

// Report is the answer to one probe.
type Report struct {
	Status Status   `json:"status"`
	Checks []Result `json:"checks,omitempty"`
}

// OK reports whether the report is up.
func (r Report) OK() bool { return r.Status == StatusUp }

// Registry holds the checks an application declared.
//
// The zero value is not usable; call New. It is safe for concurrent use, because a check
// is usually added during wiring while an orchestrator is already polling.
type Registry struct {
	mu     sync.RWMutex
	checks map[string]Check
	// started gates readiness: a process that is still starting answers "down" even
	// when every dependency is fine.
	started bool
	stopped bool
}

// New returns an empty registry.
func New() *Registry { return &Registry{checks: map[string]Check{}} }

// Add registers a check. Re-adding a name replaces it, so a module can be restarted
// without leaving a stale probe behind.
func (r *Registry) Add(name string, critical bool, run func(context.Context) error) {
	if name == "" || run == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = Check{Name: name, Critical: critical, Run: run}
}

// Remove drops a check, for a module that stops providing one.
func (r *Registry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.checks, name)
}

// SetStarted records that the application finished starting. Readiness stays down until
// then, so a load balancer cannot reach a half-wired process.
func (r *Registry) SetStarted(started bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = started
	if !started {
		r.stopped = true
	}
}

// Live answers the liveness question. It runs no dependency checks on purpose: a wedged
// process is one that cannot answer at all, and a failing database is not that.
func (r *Registry) Live(context.Context) Report {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.stopped {
		return Report{Status: StatusDown}
	}
	return Report{Status: StatusUp}
}

// Ready runs every check and reports the slow or failing ones.
func (r *Registry) Ready(ctx context.Context) Report {
	r.mu.RLock()
	started, stopped := r.started, r.stopped
	checks := make([]Check, 0, len(r.checks))
	for _, check := range r.checks {
		checks = append(checks, check)
	}
	r.mu.RUnlock()

	if !started || stopped {
		return Report{Status: StatusDown}
	}

	// Deterministic order: two probes of the same process must return the same document.
	sort.Slice(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })

	report := Report{Status: StatusUp, Checks: make([]Result, 0, len(checks))}
	for _, check := range checks {
		started := time.Now()
		err := runCheck(ctx, check)
		result := Result{
			Name:     check.Name,
			Status:   StatusUp,
			Critical: check.Critical,
			Millis:   time.Since(started).Milliseconds(),
		}
		if err != nil {
			result.Status = StatusDown
			result.Error = err.Error()
			if check.Critical {
				report.Status = StatusDown
			}
		}
		report.Checks = append(report.Checks, result)
	}
	return report
}

// runCheck turns a panicking check into a failure: a probe must never take the process
// down with it.
func runCheck(ctx context.Context, check Check) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("check panicked: %v", recovered)
		}
	}()
	return check.Run(ctx)
}
