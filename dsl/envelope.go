// Package dsl loads, validates and previews gable's declarative resources.
//
// The DSL is not a second source of truth for code: it carries the runtime
// variable parts of an application (models, pages and their presentation) and
// is validated before anything is applied or previewed.
package dsl

import (
	"fmt"
	"strings"
)

// APIVersion is the only supported envelope version.
const APIVersion = "gable/v1"

// Kind is a resource kind.
type Kind string

const (
	KindModel Kind = "Model"
	KindTable Kind = "Table"
	KindForm  Kind = "Form"
	KindPage  Kind = "Page"
	// KindApi binds an action to a Go handler the application registered. The DSL never
	// names a Go package or a function path: only a symbolic name the app resolves.
	KindApi Kind = "Api"
	// KindMcp declares a tool's metadata for a Go handler the application registered.
	KindMcp Kind = "Mcp"
)

// Metadata identifies a resource, and carries the two flags that decide what may happen to it.
type Metadata struct {
	Name        string            `yaml:"name" json:"name"`
	Title       string            `yaml:"title,omitempty" json:"title,omitempty"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Version     int               `yaml:"version,omitempty" json:"version,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	// Readonly marks a resource the pipeline may not overwrite. It is the guard rail that lets
	// a hand-written resource live next to generated ones: a push or a restore that would
	// change it is refused instead of silently winning.
	Readonly bool `yaml:"readonly,omitempty" json:"readonly,omitempty"`
	// Status is the lifecycle of a resource. "active" and an empty value are the same thing;
	// "deprecated" still loads and serves, so a resource can be retired over several
	// deployments instead of in one breaking change.
	Status string `yaml:"status,omitempty" json:"status,omitempty"`
	// Tags group resources for the UI and for filtered listings.
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	// Sort orders resources of the same kind; a smaller number comes first.
	Sort int `yaml:"sort,omitempty" json:"sort,omitempty"`
}

// Deprecated reports a resource marked for retirement.
func (m Metadata) Deprecated() bool {
	return strings.EqualFold(strings.TrimSpace(m.Status), "deprecated")
}

// Issue is one lint problem.
type Issue struct {
	Resource string `json:"resource"`
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
}

// Error implements error.
func (i Issue) Error() string {
	if i.Field != "" {
		return fmt.Sprintf("%s: %s: %s", i.Resource, i.Field, i.Message)
	}
	return fmt.Sprintf("%s: %s", i.Resource, i.Message)
}

// Issues is a list of lint problems.
type Issues []Issue

// Error implements error.
func (i Issues) Error() string {
	if len(i) == 0 {
		return ""
	}
	parts := make([]string, 0, len(i))
	for _, issue := range i {
		parts = append(parts, issue.Error())
	}
	return strings.Join(parts, "; ")
}

type header struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       Kind     `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
}
