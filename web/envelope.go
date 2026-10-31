package web

import (
	"time"

	gableerrors "github.com/maxid/gable/errors"
)

// Envelope is the six-field response body shared by every endpoint.
type Envelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
	RequestID string `json:"reqId"`
	Success   bool   `json:"success"`
	Timestamp int64  `json:"tm"`
}

// EnvelopeConfig maps framework error codes to the codes an application exposes.
type EnvelopeConfig struct {
	SuccessCode      string
	InvalidCode      string
	UnauthorizedCode string
	ForbiddenCode    string
	NotFoundCode     string
	ConflictCode     string
	InternalCode     string
}

// DefaultEnvelopeConfig uses short, transport-neutral codes.
func DefaultEnvelopeConfig() EnvelopeConfig {
	return EnvelopeConfig{
		SuccessCode:      "0",
		InvalidCode:      "invalid_argument",
		UnauthorizedCode: "unauthorized",
		ForbiddenCode:    "forbidden",
		NotFoundCode:     "not_found",
		ConflictCode:     "conflict",
		InternalCode:     "internal",
	}
}

// MapCode translates a standard framework error code to the configured code.
// Unknown codes pass through unchanged.
func (c EnvelopeConfig) MapCode(code string) string {
	switch code {
	case gableerrors.CodeInvalid:
		return c.InvalidCode
	case gableerrors.CodeUnauthorized:
		return c.UnauthorizedCode
	case gableerrors.CodeForbidden:
		return c.ForbiddenCode
	case gableerrors.CodeNotFound:
		return c.NotFoundCode
	case gableerrors.CodeConflict:
		return c.ConflictCode
	case gableerrors.CodeInternal:
		return c.InternalCode
	default:
		return code
	}
}

func newEnvelope(code, message string, data any, requestID string, success bool) Envelope {
	return Envelope{
		Code:      code,
		Message:   message,
		Data:      data,
		RequestID: requestID,
		Success:   success,
		Timestamp: time.Now().UnixMilli(),
	}
}
