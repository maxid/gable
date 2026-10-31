// Package gable is a code-first application framework for Go services.
//
// Status: experimental scaffold (v0.0.0-dev). The API is not stable yet.
//
// The framework deliberately keeps three things explicit: the container that
// owns application dependencies, the lifecycle of modules, and the
// configuration they read. There is no package-level registry.
package gable
