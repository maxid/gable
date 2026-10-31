// Package errors defines the framework's stable error contract.
package errors

import (
	stderrors "errors"
	"fmt"
	"net/http"
)

// Stable error codes. Applications may add their own; these are the ones the
// framework itself emits.
const (
	CodeInvalid      = "invalid_argument"
	CodeUnauthorized = "unauthorized"
	CodeForbidden    = "forbidden"
	CodeNotFound     = "not_found"
	CodeConflict     = "conflict"
	CodeInternal     = "internal"
)

// Error is a framework error with a stable code, a human message, an HTTP
// status and an optional cause.
type Error struct {
	Code    string
	Message string
	Status  int
	cause   error
}

// New returns an internal error with the given code and message.
func New(code, message string) *Error {
	return &Error{Code: code, Message: message, Status: http.StatusInternalServerError}
}

// WithStatus overrides the HTTP status.
func (e *Error) WithStatus(status int) *Error {
	e.Status = status
	return e
}

// WithCause attaches the underlying error.
func (e *Error) WithCause(err error) *Error {
	e.cause = err
	return e
}

// Error implements error.
func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the cause to errors.Is/As.
func (e *Error) Unwrap() error { return e.cause }

// As returns the first *Error in err's chain.
func As(err error) (*Error, bool) {
	var target *Error
	if stderrors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// IsCode reports whether err is a framework error with the given code.
func IsCode(err error, code string) bool {
	target, ok := As(err)
	return ok && target.Code == code
}

// CodeOf returns the framework error code, or "internal" for an unknown error
// and "" for a nil error.
func CodeOf(err error) string {
	if err == nil {
		return ""
	}
	if target, ok := As(err); ok {
		return target.Code
	}
	return CodeInternal
}
