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
)

// Metadata identifies a resource.
type Metadata struct {
	Name        string            `yaml:"name" json:"name"`
	Title       string            `yaml:"title,omitempty" json:"title,omitempty"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Version     int               `yaml:"version,omitempty" json:"version,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
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
