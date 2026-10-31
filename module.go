package gable

import (
	"context"
	"fmt"
	"sort"
)

// Module is a unit of application wiring and lifecycle.
//
// Register runs once, before any module starts. Start and Stop are called in
// dependency order and in reverse order respectively. A module must tolerate
// Stop being called when Start was never reached.
type Module interface {
	ID() string
	Register(*Container) error
	Start(context.Context) error
	Stop(context.Context) error
}

// Dependent is an optional interface a Module implements to declare the
// modules it needs started first.
type Dependent interface {
	DependsOn() []string
}

// orderModules returns modules in dependency order. Ties are broken by ID so
// that startup is deterministic.
func orderModules(modules []Module) ([]Module, error) {
	byID := make(map[string]Module, len(modules))
	for _, module := range modules {
		id := module.ID()
		if id == "" {
			return nil, fmt.Errorf("gable: module id must not be empty")
		}
		if _, exists := byID[id]; exists {
			return nil, fmt.Errorf("gable: duplicate module %q", id)
		}
		byID[id] = module
	}

	deps := make(map[string][]string, len(modules))
	for id, module := range byID {
		seen := make(map[string]bool)
		var list []string
		if dependent, ok := module.(Dependent); ok {
			for _, dep := range dependent.DependsOn() {
				if dep == "" || dep == id || seen[dep] {
					continue
				}
				if _, exists := byID[dep]; !exists {
					return nil, fmt.Errorf("gable: module %q depends on unknown module %q", id, dep)
				}
				seen[dep] = true
				list = append(list, dep)
			}
		}
		deps[id] = list
	}

	indegree := make(map[string]int, len(modules))
	for id, list := range deps {
		indegree[id] = len(list)
	}

	ready := make([]string, 0, len(modules))
	for id, count := range indegree {
		if count == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)

	ordered := make([]Module, 0, len(modules))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		ordered = append(ordered, byID[id])

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

	if len(ordered) != len(byID) {
		return nil, fmt.Errorf("gable: module dependency cycle")
	}
	return ordered, nil
}
