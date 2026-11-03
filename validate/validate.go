// Package validate turns struct tags into a framework error with per-field detail.
//
// The framework already depends on go-playground/validator through Gin, so this package
// does not add a validator: it gives the errors one shape. A handler returns the error and
// the web layer writes it as `invalid_argument` with the field list, which is what a form
// needs and what a log line can be searched for.
package validate

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
)

// Issue is one failing field.
type Issue struct {
	// Field is the struct path of the field, e.g. "Title" or "Address.City".
	Field string `json:"field"`
	// Rule is the tag that failed, e.g. "required" or "max".
	Rule string `json:"rule"`
	// Param is the rule's parameter, when the tag has one: "max=128" reports "128".
	Param string `json:"param,omitempty"`
	// Message is human-readable and may be translated by a Translator.
	Message string `json:"message"`
	// Value is the rejected value, as a string. It is included because "invalid input"
	// without the input is not actionable; callers that log the error must treat it as
	// potentially sensitive.
	Value string `json:"value,omitempty"`
}

// Issues is a validation failure: every field that did not pass.
type Issues []Issue

// Error implements error, listing each issue once.
func (i Issues) Error() string {
	if len(i) == 0 {
		return ""
	}
	parts := make([]string, 0, len(i))
	for _, issue := range i {
		parts = append(parts, issue.Field+": "+issue.Message)
	}
	return "invalid request: " + strings.Join(parts, "; ")
}

// Fields renders the issues as the entries a response envelope carries.
func (i Issues) Fields() []map[string]string {
	fields := make([]map[string]string, 0, len(i))
	for _, issue := range i {
		entry := map[string]string{"field": issue.Field, "rule": issue.Rule, "message": issue.Message}
		if issue.Param != "" {
			entry["param"] = issue.Param
		}
		fields = append(fields, entry)
	}
	return fields
}

// Describe renders one rule's default English message, so the web layer can explain a
// binding error without going through a validator instance.
func Describe(rule, param string) string {
	template, ok := Messages[rule]
	if !ok {
		template = Messages["default"]
	}
	if strings.Contains(template, "%s") {
		return fmt.Sprintf(template, param)
	}
	return template
}

// Translator localises a message. The default Messages is English; an application that
// needs another language implements this over its bundle.
type Translator interface {
	Translate(ctx context.Context, key string, params map[string]string) string
}

// Messages is the default English message set, keyed by validator rule.
var Messages = map[string]string{
	"required": "is required",
	"min":      "must be at least %s",
	"max":      "must be at most %s",
	"len":      "must be exactly %s characters",
	"email":    "must be a valid email address",
	"url":      "must be a valid URL",
	"uuid":     "must be a valid UUID",
	"oneof":    "must be one of %s",
	"numeric":  "must be a number",
	"gte":      "must be greater than or equal to %s",
	"lte":      "must be less than or equal to %s",
	"gt":       "must be greater than %s",
	"lt":       "must be less than %s",
	"e164":     "must be a phone number in E.164 format",
	"alphanum": "must contain only letters and digits",
	"default":  "is invalid",
}

// Validator validates structs and ranks them into Issues.
//
// The zero value is not usable; call New. It is safe for concurrent use.
type Validator struct {
	validate   *validator.Validate
	translator Translator

	mu       sync.RWMutex
	messages map[string]string
}

// New returns a validator with the default messages.
func New() *Validator {
	instance := validator.New(validator.WithRequiredStructEnabled())
	// Prefer the JSON name in messages: that is the name the client sent.
	instance.RegisterTagNameFunc(func(field reflect.StructField) string {
		for _, tag := range []string{"json", "form", "yaml"} {
			name := strings.Split(field.Tag.Get(tag), ",")[0]
			if name != "" && name != "-" {
				return name
			}
		}
		return field.Name
	})

	messages := make(map[string]string, len(Messages))
	for rule, message := range Messages {
		messages[rule] = message
	}
	return &Validator{validate: instance, messages: messages}
}

// WithTranslator translates messages; nil keeps the English defaults.
func (v *Validator) WithTranslator(translator Translator) *Validator {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.translator = translator
	return v
}

// SetMessage overrides the message for one rule.
func (v *Validator) SetMessage(rule, message string) *Validator {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.messages[rule] = message
	return v
}

// Register adds a custom rule. The message is used when the rule fails.
func (v *Validator) Register(rule string, message string, fn func(value any) bool) error {
	v.mu.Lock()
	v.messages[rule] = message
	v.mu.Unlock()
	return v.validate.RegisterValidation(rule, func(field validator.FieldLevel) bool {
		return fn(field.Field().Interface())
	})
}

// Struct validates a struct (or a pointer to one) and returns the failing fields.
//
// A value that is not a struct is a programming error and is reported as a plain error, not
// as a validation issue: no user input can fix it.
func (v *Validator) Struct(entity any) Issues {
	if entity == nil {
		return Issues{{Field: "body", Rule: "required", Message: "is required"}}
	}
	err := v.validate.Struct(entity)
	if err == nil {
		return nil
	}
	var invalid *validator.InvalidValidationError
	if errors.As(err, &invalid) {
		return Issues{{Field: "body", Rule: "invalid", Message: err.Error()}}
	}
	var fieldErrors validator.ValidationErrors
	if !errors.As(err, &fieldErrors) {
		return Issues{{Field: "body", Rule: "invalid", Message: err.Error()}}
	}

	issues := make(Issues, 0, len(fieldErrors))
	for _, fieldError := range fieldErrors {
		issues = append(issues, Issue{
			Field:   fieldError.Field(),
			Rule:    fieldError.Tag(),
			Param:   fieldError.Param(),
			Message: v.message(context.Background(), fieldError),
			Value:   fmt.Sprintf("%v", fieldError.Value()),
		})
	}
	return issues
}

// StructContext is Struct with a request context, so a translator can pick the language.
func (v *Validator) StructContext(ctx context.Context, entity any) Issues {
	issues := v.Struct(entity)
	if v.translator == nil || len(issues) == 0 {
		return issues
	}
	for i := range issues {
		issues[i].Message = v.translator.Translate(ctx, issues[i].Rule, map[string]string{
			"field": issues[i].Field,
			"param": issues[i].Param,
		})
	}
	return issues
}

// message renders the rule's message with its parameter.
func (v *Validator) message(ctx context.Context, fieldError validator.FieldError) string {
	rule := fieldError.Tag()
	// A failed required tag on a nested struct reports "required" on the struct; that is
	// still what the client has to fix.
	v.mu.RLock()
	translator := v.translator
	template, ok := v.messages[rule]
	v.mu.RUnlock()

	if translator != nil {
		if translated := translator.Translate(ctx, rule, map[string]string{
			"field": fieldError.Field(), "param": fieldError.Param(),
		}); translated != "" {
			return translated
		}
	}
	if !ok {
		template = v.messages["default"]
	}
	if strings.Contains(template, "%s") {
		return fmt.Sprintf(template, fieldError.Param())
	}
	return template
}
