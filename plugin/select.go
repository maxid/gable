package plugin

import (
	"context"
	"fmt"
	"sort"
)

// Assembly is a selected, ordered set of capabilities.
type Assembly struct {
	set     *Set
	order   []string
	modules []Module
}

// Select resolves the requested ids, pulls in their transitive dependencies and orders
// the result topologically.
//
// Every failure is a startup error rather than a silently smaller assembly: an unknown
// capability, a dependency that was not selected, or a cycle all mean the binary does
// not match the declaration.
func (s *Set) Select(ids ...string) (*Assembly, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: no capability selected", ErrPermanent)
	}

	// Transitive closure: a requested capability drags in what it needs.
	selected := map[string]bool{}
	queue := append([]string(nil), ids...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if selected[id] {
			continue
		}
		module, ok := s.modules[id]
		if !ok {
			return nil, fmt.Errorf("%w: capability %q is not in this binary", ErrPermanent, id)
		}
		selected[id] = true
		if dependent, ok := module.(Dependent); ok {
			queue = append(queue, dependent.DependsOn()...)
		}
	}

	order, err := topoSort(s.modules, selected)
	if err != nil {
		return nil, err
	}

	modules := make([]Module, 0, len(order))
	for _, id := range order {
		modules = append(modules, s.modules[id])
	}
	return &Assembly{set: s, order: order, modules: modules}, nil
}

// Manifest reports what the assembly contains, in start order.
func (a *Assembly) Manifest() []Entry {
	entries := make([]Entry, 0, len(a.modules))
	for _, module := range a.modules {
		entry := Entry{ID: module.ID()}
		if versioned, ok := module.(Versioned); ok {
			entry.Version = versioned.Version()
		}
		if dependent, ok := module.(Dependent); ok {
			entry.DependsOn = append([]string(nil), dependent.DependsOn()...)
		}
		entries = append(entries, entry)
	}
	return entries
}

// IDs returns the capability ids in start order.
func (a *Assembly) IDs() []string { return append([]string(nil), a.order...) }

// Register runs every capability's registration in dependency order.
func (a *Assembly) Register(container Container) error {
	for _, module := range a.modules {
		if err := module.Register(container); err != nil {
			return fmt.Errorf("gable/plugin: register %s: %w", module.ID(), err)
		}
	}
	return nil
}

// Start registers and then starts the capabilities in order.
//
// A failure stops the ones already started, in reverse order, so a half-started
// application never keeps serving: the same rule the application runtime follows.
func (a *Assembly) Start(ctx context.Context, container Container) error {
	if err := a.Register(container); err != nil {
		return err
	}

	started := make([]Module, 0, len(a.modules))
	for _, module := range a.modules {
		if err := ctx.Err(); err != nil {
			_ = a.stopModules(ctx, started)
			return err
		}
		starter, ok := module.(Starter)
		if !ok {
			continue
		}
		if err := starter.Start(ctx); err != nil {
			_ = a.stopModules(ctx, started)
			return fmt.Errorf("gable/plugin: start %s: %w", module.ID(), err)
		}
		started = append(started, module)
	}
	return nil
}

// Stop shuts the capabilities down in reverse order, joining every failure so one
// broken capability cannot mask the rest.
func (a *Assembly) Stop(ctx context.Context) error {
	return a.stopModules(ctx, a.modules)
}

func (a *Assembly) stopModules(ctx context.Context, modules []Module) error {
	var failures []error
	for i := len(modules) - 1; i >= 0; i-- {
		stopper, ok := modules[i].(Stopper)
		if !ok {
			continue
		}
		if err := stopper.Stop(ctx); err != nil {
			failures = append(failures, fmt.Errorf("gable/plugin: stop %s: %w", modules[i].ID(), err))
		}
	}
	return joinErrors(failures)
}

// topoSort orders the selected capabilities. Ties break on id, so the start order is
// the same on every process — a rolling restart must not reorder capabilities.
func topoSort(modules map[string]Module, selected map[string]bool) ([]string, error) {
	deps := make(map[string][]string, len(selected))
	for id := range selected {
		module := modules[id]
		var list []string
		if dependent, ok := module.(Dependent); ok {
			seen := map[string]bool{}
			for _, dep := range dependent.DependsOn() {
				if dep == "" || dep == id || seen[dep] {
					continue
				}
				seen[dep] = true
				if !selected[dep] {
					return nil, fmt.Errorf("%w: capability %q needs %q, which is not in this binary", ErrPermanent, id, dep)
				}
				list = append(list, dep)
			}
		}
		deps[id] = list
	}

	indegree := make(map[string]int, len(deps))
	ready := make([]string, 0, len(deps))
	for id, list := range deps {
		indegree[id] = len(list)
		if len(list) == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)

	order := make([]string, 0, len(deps))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)

		for other, list := range deps {
			for _, dep := range list {
				if dep != id {
					continue
				}
				indegree[other]--
				if indegree[other] == 0 {
					ready = append(ready, other)
				}
			}
		}
		sort.Strings(ready)
	}

	if len(order) != len(deps) {
		return nil, fmt.Errorf("%w: capability dependency cycle", ErrPermanent)
	}
	return order, nil
}

// joinErrors aggregates failures without dropping any.
func joinErrors(failures []error) error {
	switch len(failures) {
	case 0:
		return nil
	case 1:
		return failures[0]
	}
	var message string
	for i, err := range failures {
		if i > 0 {
			message += "\n"
		}
		message += err.Error()
	}
	return fmt.Errorf("%d capability failures: %s", len(failures), message)
}
