package gable

import "context"

// Module is a unit of application wiring and lifecycle.
//
// Register runs once, before any module starts. Start and Stop are called in
// dependency order and in reverse order respectively. A module must tolerate
// Stop being called when Start was never reached.
//
// The shape is gplugin's Module[*Container] plus its optional Starter/Stopper, so a
// module is added to the assembly without an adapter; ordering, cycle detection and
// rollback live there (see App.Start).
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
