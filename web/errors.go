package web

import (
	"errors"

	"github.com/go-playground/validator/v10"
)

func validationDetails(err error) (map[string]string, bool) {
	var fieldErrors validator.ValidationErrors
	if !errors.As(err, &fieldErrors) {
		return nil, false
	}
	details := make(map[string]string, len(fieldErrors))
	for _, field := range fieldErrors {
		details[field.Field()] = field.Tag()
	}
	return details, true
}
