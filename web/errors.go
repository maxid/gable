package web

import (
	"errors"

	"github.com/go-playground/validator/v10"

	"github.com/maxid/gable/validate"
)

// validationDetails turns a validation failure into the envelope's data.
//
// Both shapes are reported the same way — the framework's own validate.Issues and the
// raw validator errors Gin's binding returns — so a client parses one list of fields no
// matter which layer rejected the request.
func validationDetails(err error) (map[string]any, bool) {
	var issues validate.Issues
	if errors.As(err, &issues) && len(issues) > 0 {
		return map[string]any{"fields": issues.Fields()}, true
	}

	var fieldErrors validator.ValidationErrors
	if !errors.As(err, &fieldErrors) {
		return nil, false
	}
	fields := make([]map[string]string, 0, len(fieldErrors))
	for _, field := range fieldErrors {
		entry := map[string]string{
			"field":   field.Field(),
			"rule":    field.Tag(),
			"message": validate.Describe(field.Tag(), field.Param()),
		}
		if field.Param() != "" {
			entry["param"] = field.Param()
		}
		fields = append(fields, entry)
	}
	return map[string]any{"fields": fields}, true
}
