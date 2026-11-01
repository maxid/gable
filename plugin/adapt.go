package plugin

import (
	"context"
	"fmt"
)

// Spec describes a capability by closures instead of a type.
//
// It exists for modules that already have their own shape — the platform's modules take
// a concrete `*Container` and their own lifecycle interface, so they cannot satisfy this
// package's interfaces by signature. Describing such a module with closures lets it join
// selection and manifests without renaming anything in its own repository.
type Spec struct {
	// ID is the capability name used for selection.
	ID string
	// Version is reported in the manifest; empty means the module does not declare one.
	Version string
	// DependsOn lists the capabilities that must start first.
	DependsOn []string
	// Register wires the capability. The container is this package's interface; a
	// module whose Register takes its own concrete type adapts it inside this closure.
	Register func(Container) error
	// Start and Stop are optional; nil means the capability has no lifecycle.
	Start func(context.Context) error
	Stop  func(context.Context) error
}

// From turns a Spec into a Module.
//
// No description is inferred: everything the manifest shows comes from the Spec, so an
// adapted module's version and dependencies must be stated rather than guessed. A spec
// without hooks becomes a wiring-only module — it does not satisfy Starter, so selection
// never lists it as started and a rollback has nothing to stop for it.
func From(spec Spec) (Module, error) {
	if spec.ID == "" {
		return nil, fmt.Errorf("%w: capability id is required", ErrPermanent)
	}
	if spec.Register == nil {
		return nil, fmt.Errorf("%w: capability %q has no register function", ErrPermanent, spec.ID)
	}
	base := specModule{spec: spec}
	if spec.Start == nil && spec.Stop == nil {
		return base, nil
	}
	return specLifecycle{base}, nil
}

// specModule is the wiring half of an adapted spec: identity, version and dependencies.
type specModule struct {
	spec Spec
}

// ID implements Module.
func (m specModule) ID() string { return m.spec.ID }

// Version implements Versioned.
func (m specModule) Version() string { return m.spec.Version }

// DependsOn implements Dependent.
func (m specModule) DependsOn() []string { return append([]string(nil), m.spec.DependsOn...) }

// Register implements Module.
func (m specModule) Register(container Container) error { return m.spec.Register(container) }

// specLifecycle is a spec that declared at least one hook. Only this variant satisfies
// Starter/Stopper, which is what keeps a wiring-only capability out of the start list.
type specLifecycle struct {
	specModule
}

// Start implements Starter.
func (m specLifecycle) Start(ctx context.Context) error {
	if m.spec.Start == nil {
		return nil
	}
	return m.spec.Start(ctx)
}

// Stop implements Stopper. A spec that only declared Stop still gets called here, so a
// capability can clean up what it built during Register.
func (m specLifecycle) Stop(ctx context.Context) error {
	if m.spec.Stop == nil {
		return nil
	}
	return m.spec.Stop(ctx)
}
