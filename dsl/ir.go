package dsl

import (
	"fmt"
	"strings"
)

// IRAPIVersion is the low-code envelope version consumed by vamis.
const IRAPIVersion = "lcdp.ir/v1"

// IR returns the low-code IR document for a Table, Form or Page.
func (s *Set) IR(kind Kind, name string) (map[string]any, error) {
	switch kind {
	case KindTable:
		doc, ok := s.Tables[name]
		if !ok {
			return nil, fmt.Errorf("dsl: table %q not found", name)
		}
		return s.tableIR(doc)
	case KindForm:
		doc, ok := s.Forms[name]
		if !ok {
			return nil, fmt.Errorf("dsl: form %q not found", name)
		}
		return s.formIR(doc)
	case KindPage:
		doc, ok := s.Pages[name]
		if !ok {
			return nil, fmt.Errorf("dsl: page %q not found", name)
		}
		return s.pageIR(doc)
	default:
		return nil, fmt.Errorf("dsl: %s has no UI IR", kind)
	}
}

func (s *Set) tableIR(doc *TableDoc) (map[string]any, error) {
	model, ok := s.Models[doc.Spec.Model]
	if !ok {
		return nil, fmt.Errorf("dsl: table %q references unknown model %q", doc.Metadata.Name, doc.Spec.Model)
	}
	title := firstNonEmpty(doc.Metadata.Title, model.Metadata.Title, doc.Metadata.Name)
	runtimeAPI := doc.Spec.RuntimeAPI
	if runtimeAPI == "" {
		runtimeAPI = "/dsl/data/" + model.Metadata.Name
	}

	var columns []map[string]any
	if len(doc.Spec.Columns) == 0 {
		for _, field := range model.Spec.Fields {
			if field.Primary || field.Tenant {
				continue
			}
			columns = append(columns, map[string]any{
				"name":        field.Name,
				"comment":     firstNonEmpty(field.Label, field.Name),
				"listVisible": true,
				"pk":          false,
				"sortable":    false,
			})
		}
	} else {
		for _, column := range doc.Spec.Columns {
			field := model.Field(column.Field)
			if field == nil {
				continue
			}
			visible := true
			if column.Visible != nil {
				visible = *column.Visible
			}
			columns = append(columns, map[string]any{
				"name":        field.Name,
				"comment":     firstNonEmpty(column.Label, field.Label, field.Name),
				"listVisible": visible,
				"pk":          field.Primary,
				"sortable":    column.Sortable,
			})
		}
	}

	return map[string]any{
		"apiVersion": IRAPIVersion,
		"kind":       "TableDef",
		"metadata": map[string]any{
			"name":        doc.Metadata.Name,
			"description": firstNonEmpty(doc.Metadata.Description, title),
		},
		"spec": map[string]any{
			"tableComment": title,
			"runtimeApi":   runtimeAPI,
			"columns":      columns,
		},
	}, nil
}

func (s *Set) formIR(doc *FormDoc) (map[string]any, error) {
	model, ok := s.Models[doc.Spec.Model]
	if !ok {
		return nil, fmt.Errorf("dsl: form %q references unknown model %q", doc.Metadata.Name, doc.Spec.Model)
	}
	title := firstNonEmpty(doc.Metadata.Title, model.Metadata.Title, doc.Metadata.Name)

	var fields []map[string]any
	if len(doc.Spec.Fields) == 0 {
		for _, field := range model.Spec.Fields {
			if field.Primary || field.Tenant || field.AutoCreate || field.AutoUpdate {
				continue
			}
			fields = append(fields, map[string]any{
				"name":        field.Name,
				"label":       firstNonEmpty(field.Label, field.Name),
				"controlType": defaultControlType(field),
				"required":    field.Required,
				"readonly":    false,
				"visible":     true,
				"multiple":    false,
			})
		}
	} else {
		for _, item := range doc.Spec.Fields {
			field := model.Field(item.Field)
			if field == nil {
				continue
			}
			visible := true
			if item.Visible != nil {
				visible = *item.Visible
			}
			control := strings.ToUpper(item.ControlType)
			if control == "" {
				control = defaultControlType(*field)
			}
			fields = append(fields, map[string]any{
				"name":        field.Name,
				"label":       firstNonEmpty(item.Label, field.Label, field.Name),
				"controlType": control,
				"required":    item.Required || field.Required,
				"readonly":    item.Readonly,
				"visible":     visible,
				"multiple":    item.Multiple,
			})
		}
	}

	spec := map[string]any{
		"formComment": title,
		"fields":      fields,
	}
	if doc.Spec.Layout != nil && doc.Spec.Layout.LabelPosition != "" {
		spec["layout"] = map[string]any{"labelPosition": strings.ToUpper(doc.Spec.Layout.LabelPosition)}
	}
	return map[string]any{
		"apiVersion": IRAPIVersion,
		"kind":       "FormDef",
		"metadata": map[string]any{
			"name":        doc.Metadata.Name,
			"description": firstNonEmpty(doc.Metadata.Description, title),
		},
		"spec": spec,
	}, nil
}

func (s *Set) pageIR(doc *PageDoc) (map[string]any, error) {
	title := firstNonEmpty(doc.Spec.Title, doc.Metadata.Title, doc.Metadata.Name)
	body := []any{}

	if doc.Spec.Table != "" {
		table, ok := s.Tables[doc.Spec.Table]
		if !ok {
			return nil, fmt.Errorf("dsl: page %q references unknown table %q", doc.Metadata.Name, doc.Spec.Table)
		}
		model, ok := s.Models[table.Spec.Model]
		if !ok {
			return nil, fmt.Errorf("dsl: table %q references unknown model %q", table.Metadata.Name, table.Spec.Model)
		}
		api := table.Spec.RuntimeAPI
		if api == "" {
			api = "/dsl/data/" + model.Metadata.Name
		}
		var columns []map[string]any
		for _, column := range table.Spec.Columns {
			field := model.Field(column.Field)
			if field == nil || field.Primary {
				continue
			}
			visible := true
			if column.Visible != nil {
				visible = *column.Visible
			}
			if !visible {
				continue
			}
			columns = append(columns, map[string]any{
				"name":     field.Name,
				"label":    firstNonEmpty(column.Label, field.Label, field.Name),
				"sortable": column.Sortable,
			})
		}
		if len(columns) == 0 {
			for _, field := range model.Spec.Fields {
				if field.Primary || field.Tenant {
					continue
				}
				columns = append(columns, map[string]any{
					"name":     field.Name,
					"label":    firstNonEmpty(field.Label, field.Name),
					"sortable": false,
				})
			}
		}
		body = append(body, map[string]any{
			"type":    "crud",
			"name":    table.Metadata.Name,
			"api":     api,
			"columns": columns,
		})
	}

	if doc.Spec.Form != "" {
		form, ok := s.Forms[doc.Spec.Form]
		if !ok {
			return nil, fmt.Errorf("dsl: page %q references unknown form %q", doc.Metadata.Name, doc.Spec.Form)
		}
		model, ok := s.Models[form.Spec.Model]
		if !ok {
			return nil, fmt.Errorf("dsl: form %q references unknown model %q", form.Metadata.Name, form.Spec.Model)
		}
		var fields []map[string]any
		items := form.Spec.Fields
		if len(items) == 0 {
			for _, field := range model.Spec.Fields {
				if field.Primary || field.Tenant || field.AutoCreate || field.AutoUpdate {
					continue
				}
				items = append(items, FormFieldSpec{Field: field.Name})
			}
		}
		for _, item := range items {
			field := model.Field(item.Field)
			if field == nil {
				continue
			}
			control := strings.ToUpper(item.ControlType)
			if control == "" {
				control = defaultControlType(*field)
			}
			fields = append(fields, map[string]any{
				"type":     amisControl(control),
				"name":     field.Name,
				"label":    firstNonEmpty(item.Label, field.Label, field.Name),
				"required": item.Required || field.Required,
			})
		}
		body = append(body, map[string]any{
			"type": "form",
			"name": form.Metadata.Name,
			"body": fields,
		})
	}

	return map[string]any{
		"apiVersion": IRAPIVersion,
		"kind":       "UiPage",
		"metadata": map[string]any{
			"name":        doc.Metadata.Name,
			"description": firstNonEmpty(doc.Metadata.Description, title),
		},
		"spec": map[string]any{
			"type":  "page",
			"title": title,
			"body":  body,
		},
	}, nil
}

func defaultControlType(field FieldSpec) string {
	if len(field.Enum) > 0 {
		return "SELECT"
	}
	switch field.Type {
	case "bool":
		return "SWITCH"
	case "int32", "int64", "float64":
		return "INPUT_NUMBER"
	case "text":
		return "TEXTAREA"
	case "time":
		return "DATETIME"
	case "json":
		return "TEXTAREA"
	default:
		return "INPUT"
	}
}

func amisControl(control string) string {
	switch control {
	case "INPUT":
		return "input-text"
	case "TEXTAREA":
		return "textarea"
	case "SELECT":
		return "select"
	case "TREE_SELECT":
		return "tree-select"
	case "NUMBER", "INPUT_NUMBER":
		return "input-number"
	case "SWITCH":
		return "switch"
	case "DATE", "DATETIME":
		return "input-date"
	case "CHECKBOX":
		return "checkbox"
	case "RADIO", "RADIOS":
		return "radios"
	default:
		return "input-text"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
