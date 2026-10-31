package migrate

import (
	"context"
	"fmt"
)

const lockName = "gable-migrate"

// Engine validates and applies migrations against a Store.
type Engine struct {
	store      Store
	outOfOrder bool
}

// EngineOption configures an Engine.
type EngineOption func(*Engine)

// WithOutOfOrder allows a lower pending version to run after a higher applied
// version. Flyway defaults to false; keep that default for production.
func WithOutOfOrder(allow bool) EngineOption {
	return func(e *Engine) { e.outOfOrder = allow }
}

// NewEngine creates an engine backed by store.
func NewEngine(store Store, opts ...EngineOption) (*Engine, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: nil store", ErrInvalidConfig)
	}
	e := &Engine{store: store}
	for _, opt := range opts {
		opt(e)
	}
	return e, nil
}

// Validate checks a source set against the history without changing it.
func (e *Engine) Validate(ctx context.Context, ms []Migration) error {
	if err := e.store.Ensure(ctx); err != nil {
		return err
	}
	history, err := e.store.List(ctx)
	if err != nil {
		return err
	}
	return e.validate(history, ms)
}

// Status reports applied, pending and failed counts.
func (e *Engine) Status(ctx context.Context, ms []Migration) (Status, error) {
	if err := e.store.Ensure(ctx); err != nil {
		return Status{}, err
	}
	history, err := e.store.List(ctx)
	if err != nil {
		return Status{}, err
	}
	status := Status{}
	applied := map[string]bool{}
	for _, h := range history {
		if !h.Success {
			status.Failed++
			continue
		}
		if h.Type == "BASELINE" {
			status.Baseline = h.Version
			continue
		}
		applied[h.Version] = true
		status.Applied++
	}
	for _, m := range ms {
		if applied[m.Version] {
			continue
		}
		if status.Baseline != "" && VersionCompare(m.Version, status.Baseline) <= 0 {
			continue
		}
		status.Pending++
		if status.Latest == "" || VersionCompare(m.Version, status.Latest) > 0 {
			status.Latest = m.Version
		}
	}
	return status, nil
}

// Up applies every pending migration in order and returns the applied ones.
func (e *Engine) Up(ctx context.Context, ms []Migration) ([]Migration, error) {
	release, err := e.store.Lock(ctx, lockName)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := e.store.Ensure(ctx); err != nil {
		return nil, err
	}
	history, err := e.store.List(ctx)
	if err != nil {
		return nil, err
	}
	if err := e.validate(history, ms); err != nil {
		return nil, err
	}

	applied := map[string]History{}
	baseline := ""
	maxApplied := ""
	for _, h := range history {
		if h.Type == "BASELINE" {
			baseline = h.Version
			continue
		}
		applied[h.Version] = h
		if maxApplied == "" || VersionCompare(h.Version, maxApplied) > 0 {
			maxApplied = h.Version
		}
	}

	var done []Migration
	for _, m := range ms {
		if _, ok := applied[m.Version]; ok {
			continue
		}
		if baseline != "" && VersionCompare(m.Version, baseline) <= 0 {
			continue
		}
		if !e.outOfOrder && maxApplied != "" && VersionCompare(m.Version, maxApplied) < 0 {
			return done, fmt.Errorf("%w: %s is lower than applied %s", ErrOutOfOrder, m.Version, maxApplied)
		}
		if err := e.store.Apply(ctx, m); err != nil {
			return done, fmt.Errorf("%s %s: %w", m.Script, m.Version, err)
		}
		done = append(done, m)
		if maxApplied == "" || VersionCompare(m.Version, maxApplied) > 0 {
			maxApplied = m.Version
		}
	}
	return done, nil
}

// Baseline records a Flyway baseline row. It is allowed only when the history
// table is empty; migrations at or below version are then ignored by Up.
func (e *Engine) Baseline(ctx context.Context, version string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	release, err := e.store.Lock(ctx, lockName)
	if err != nil {
		return err
	}
	defer release()
	if err := e.store.Ensure(ctx); err != nil {
		return err
	}
	history, err := e.store.List(ctx)
	if err != nil {
		return err
	}
	if len(history) != 0 {
		return ErrBaselineExists
	}
	return e.store.Record(ctx, Migration{
		Version:     version,
		Description: "<< Flyway Baseline >>",
		Script:      "<< Flyway Baseline >>",
		Type:        "BASELINE",
	})
}

// Repair removes failed history rows. It does not rewrite checksums: a
// checksum mismatch means the file changed after it was applied, and silently
// accepting that would hide a real drift.
func (e *Engine) Repair(ctx context.Context) error {
	release, err := e.store.Lock(ctx, lockName)
	if err != nil {
		return err
	}
	defer release()
	if err := e.store.Ensure(ctx); err != nil {
		return err
	}
	return e.store.DeleteFailed(ctx)
}

func (e *Engine) validate(history []History, ms []Migration) error {
	source := make(map[string]Migration, len(ms))
	for _, m := range ms {
		if _, exists := source[m.Version]; exists {
			return fmt.Errorf("%w: duplicate source version %s", ErrValidation, m.Version)
		}
		source[m.Version] = m
	}

	baseline := ""
	maxApplied := ""
	appliedVersions := map[string]bool{}
	for _, h := range history {
		if !h.Success {
			return fmt.Errorf("%w: failed migration %s (%s); run Repair after fixing it", ErrValidation, h.Version, h.Script)
		}
		if h.Type == "BASELINE" {
			baseline = h.Version
			continue
		}
		appliedVersions[h.Version] = true
		m, ok := source[h.Version]
		if !ok {
			return fmt.Errorf("%w: applied migration %s (%s) is missing from the source", ErrValidation, h.Version, h.Script)
		}
		if h.Checksum != nil && m.Checksum != *h.Checksum {
			return fmt.Errorf("%w: checksum mismatch for %s: applied %d, source %d", ErrValidation, h.Version, *h.Checksum, m.Checksum)
		}
		if maxApplied == "" || VersionCompare(h.Version, maxApplied) > 0 {
			maxApplied = h.Version
		}
	}

	if e.outOfOrder || maxApplied == "" {
		return nil
	}
	for _, m := range ms {
		if appliedVersions[m.Version] {
			continue
		}
		if baseline != "" && VersionCompare(m.Version, baseline) <= 0 {
			continue
		}
		if VersionCompare(m.Version, maxApplied) < 0 {
			return fmt.Errorf("%w: pending %s is lower than applied %s", ErrOutOfOrder, m.Version, maxApplied)
		}
	}
	return nil
}
