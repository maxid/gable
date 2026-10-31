package rpc

import "errors"

// The stable errors.
var (
	// ErrPermanent means retrying the same call cannot succeed.
	ErrPermanent = errors.New("rpc: permanent error")
	// ErrAlreadyRegistered means a method name was registered twice.
	ErrAlreadyRegistered = errors.New("rpc: already registered")
	// ErrNoRegistry means a local client was built without a registry.
	ErrNoRegistry = errors.New("rpc: registry is required")
	// ErrNoEndpoint means no instance could be resolved for a service.
	ErrNoEndpoint = errors.New("rpc: no endpoint for service")
	// ErrUnauthorized means the shared key was missing or wrong.
	ErrUnauthorized = errors.New("rpc: unauthorized")
)
