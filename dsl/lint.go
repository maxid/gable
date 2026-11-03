package dsl

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	resourceNameRE = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)
	identifierRE   = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
)

var fieldTypes = map[string]bool{
	"int64":   true,
	"int32":   true,
	"string":  true,
	"text":    true,
	"bool":    true,
	"float64": true,
	"time":    true,
	"json":    true,
}

var controlTypes = map[string]bool{
	"INPUT":        true,
	"TEXTAREA":     true,
	"SELECT":       true,
	"TREE_SELECT":  true,
	"NUMBER":       true,
	"INPUT_NUMBER": true,
	"SWITCH":       true,
	"DATE":         true,
	"DATETIME":     true,
	"CHECKBOX":     true,
	"RADIO":        true,
	"RADIOS":       true,
}

// Lint validates every resource and the references between them.
func (s *Set) Lint() Issues {
	var issues Issues
	for _, name := range sortedKeys(s.Models) {
		doc := s.Models[name]
		issues = append(issues, envelopeIssues(KindModel, doc.APIVersion, doc.Kind, doc.Metadata)...)
		issues = append(issues, lintModel(doc)...)
	}
	for _, name := range sortedKeys(s.Tables) {
		doc := s.Tables[name]
		issues = append(issues, envelopeIssues(KindTable, doc.APIVersion, doc.Kind, doc.Metadata)...)
		issues = append(issues, s.lintTable(doc)...)
	}
	for _, name := range sortedKeys(s.Forms) {
		doc := s.Forms[name]
		issues = append(issues, envelopeIssues(KindForm, doc.APIVersion, doc.Kind, doc.Metadata)...)
		issues = append(issues, s.lintForm(doc)...)
	}
	for _, name := range sortedKeys(s.Pages) {
		doc := s.Pages[name]
		issues = append(issues, envelopeIssues(KindPage, doc.APIVersion, doc.Kind, doc.Metadata)...)
		issues = append(issues, s.lintPage(doc)...)
	}
	for _, name := range sortedKeys(s.APIs) {
		doc := s.APIs[name]
		issues = append(issues, envelopeIssues(KindApi, doc.APIVersion, doc.Kind, doc.Metadata)...)
		issues = append(issues, s.lintAPI(doc)...)
	}
	for _, name := range sortedKeys(s.MCPs) {
		doc := s.MCPs[name]
		issues = append(issues, envelopeIssues(KindMcp, doc.APIVersion, doc.Kind, doc.Metadata)...)
		issues = append(issues, lintMcp(doc)...)
	}
	return issues
}

func envelopeIssues(expected Kind, apiVersion string, kind Kind, metadata Metadata) Issues {
	var issues Issues
	resource := string(expected) + "/" + metadata.Name
	if apiVersion != APIVersion {
		issues = append(issues, Issue{Resource: resource, Message: fmt.Sprintf("apiVersion must be %s", APIVersion)})
	}
	if kind != expected {
		issues = append(issues, Issue{Resource: resource, Message: fmt.Sprintf("kind must be %s", expected)})
	}
	if !resourceNameRE.MatchString(metadata.Name) {
		issues = append(issues, Issue{Resource: resource, Message: "metadata.name must match ^[a-z][a-z0-9._-]*$"})
	}
	if !statuses[strings.ToLower(strings.TrimSpace(metadata.Status))] {
		issues = append(issues, Issue{
			Resource: resource,
			Field:    "metadata.status",
			Message:  fmt.Sprintf("unsupported status %q (want active or deprecated)", metadata.Status),
		})
	}
	return issues
}

// statuses are the lifecycle values a resource may declare. Empty means active.
var statuses = map[string]bool{"": true, "active": true, "deprecated": true}

func lintModel(doc *ModelDoc) Issues {
	resource := string(KindModel) + "/" + doc.Metadata.Name
	var issues Issues
	if !identifierRE.MatchString(doc.TableName()) {
		issues = append(issues, Issue{Resource: resource, Field: "spec.table", Message: "table must be a snake_case identifier (set spec.table when the resource name contains dots)"})
	}
	if len(doc.Spec.Fields) == 0 {
		issues = append(issues, Issue{Resource: resource, Field: "spec.fields", Message: "at least one field is required"})
		return issues
	}

	seen := map[string]bool{}
	primary, tenant := 0, 0
	for index, field := range doc.Spec.Fields {
		fieldResource := fmt.Sprintf("%s.fields[%d]", resource, index)
		if !identifierRE.MatchString(field.Name) {
			issues = append(issues, Issue{Resource: fieldResource, Field: "name", Message: "must be a snake_case identifier"})
			continue
		}
		if seen[field.Name] {
			issues = append(issues, Issue{Resource: fieldResource, Field: "name", Message: "duplicate field"})
		}
		seen[field.Name] = true

		if !fieldTypes[field.Type] {
			issues = append(issues, Issue{Resource: fieldResource, Field: "type", Message: fmt.Sprintf("unsupported type %q", field.Type)})
			continue
		}
		if field.Primary {
			primary++
			if field.Type != "int64" {
				issues = append(issues, Issue{Resource: fieldResource, Field: "type", Message: "primary key must be int64"})
			}
			switch field.Generator {
			case "", "snowflake", "auto", "none":
			default:
				issues = append(issues, Issue{Resource: fieldResource, Field: "generator", Message: fmt.Sprintf("unsupported generator %q", field.Generator)})
			}
		} else if field.Generator != "" {
			issues = append(issues, Issue{Resource: fieldResource, Field: "generator", Message: "generator is only valid on a primary key"})
		}
		if field.Tenant {
			tenant++
			if field.Type != "int64" {
				issues = append(issues, Issue{Resource: fieldResource, Field: "type", Message: "tenant field must be int64"})
			}
		}
		if field.AutoCreate || field.AutoUpdate {
			if field.Type != "time" {
				issues = append(issues, Issue{Resource: fieldResource, Field: "type", Message: "autoCreate/autoUpdate fields must be time"})
			}
		}
		if len(field.Enum) > 0 {
			if field.Type != "string" {
				issues = append(issues, Issue{Resource: fieldResource, Field: "enum", Message: "enum is only valid on string fields"})
			}
			if field.Default != nil {
				value := fmt.Sprint(field.Default)
				found := false
				for _, candidate := range field.Enum {
					if candidate == value {
						found = true
						break
					}
				}
				if !found {
					issues = append(issues, Issue{Resource: fieldResource, Field: "default", Message: "default must be one of enum"})
				}
			}
		}
		if field.Type == "string" && field.Size < 0 {
			issues = append(issues, Issue{Resource: fieldResource, Field: "size", Message: "size must not be negative"})
		}
	}
	if primary == 0 {
		issues = append(issues, Issue{Resource: resource, Field: "spec.fields", Message: "exactly one primary field is required"})
	} else if primary > 1 {
		issues = append(issues, Issue{Resource: resource, Field: "spec.fields", Message: "only one primary field is allowed"})
	}
	if tenant > 1 {
		issues = append(issues, Issue{Resource: resource, Field: "spec.fields", Message: "only one tenant field is allowed"})
	}
	return issues
}

func (s *Set) lintTable(doc *TableDoc) Issues {
	resource := string(KindTable) + "/" + doc.Metadata.Name
	var issues Issues
	model, ok := s.Models[doc.Spec.Model]
	if !ok {
		issues = append(issues, Issue{Resource: resource, Field: "spec.model", Message: fmt.Sprintf("unknown model %q", doc.Spec.Model)})
		return issues
	}
	if doc.Spec.RuntimeAPI != "" && !strings.HasPrefix(doc.Spec.RuntimeAPI, "/") {
		issues = append(issues, Issue{Resource: resource, Field: "spec.runtimeApi", Message: "must start with /"})
	}
	seen := map[string]bool{}
	for index, column := range doc.Spec.Columns {
		fieldResource := fmt.Sprintf("%s.columns[%d]", resource, index)
		if seen[column.Field] {
			issues = append(issues, Issue{Resource: fieldResource, Field: "field", Message: "duplicate column"})
		}
		seen[column.Field] = true
		if model.Field(column.Field) == nil {
			issues = append(issues, Issue{Resource: fieldResource, Field: "field", Message: fmt.Sprintf("unknown model field %q", column.Field)})
		}
	}
	for index, filter := range doc.Spec.Filters {
		if model.Field(filter) == nil {
			issues = append(issues, Issue{Resource: fmt.Sprintf("%s.filters[%d]", resource, index), Message: fmt.Sprintf("unknown model field %q", filter)})
		}
	}
	return issues
}

func (s *Set) lintForm(doc *FormDoc) Issues {
	resource := string(KindForm) + "/" + doc.Metadata.Name
	var issues Issues
	model, ok := s.Models[doc.Spec.Model]
	if !ok {
		issues = append(issues, Issue{Resource: resource, Field: "spec.model", Message: fmt.Sprintf("unknown model %q", doc.Spec.Model)})
		return issues
	}
	seen := map[string]bool{}
	for index, field := range doc.Spec.Fields {
		fieldResource := fmt.Sprintf("%s.fields[%d]", resource, index)
		if seen[field.Field] {
			issues = append(issues, Issue{Resource: fieldResource, Field: "field", Message: "duplicate field"})
		}
		seen[field.Field] = true
		if model.Field(field.Field) == nil {
			issues = append(issues, Issue{Resource: fieldResource, Field: "field", Message: fmt.Sprintf("unknown model field %q", field.Field)})
		}
		if field.ControlType != "" && !controlTypes[strings.ToUpper(field.ControlType)] {
			issues = append(issues, Issue{Resource: fieldResource, Field: "controlType", Message: fmt.Sprintf("unsupported control type %q", field.ControlType)})
		}
	}
	return issues
}

func (s *Set) lintPage(doc *PageDoc) Issues {
	resource := string(KindPage) + "/" + doc.Metadata.Name
	var issues Issues
	if doc.Spec.Table == "" && doc.Spec.Form == "" {
		issues = append(issues, Issue{Resource: resource, Field: "spec", Message: "a page needs a table or a form"})
	}
	if doc.Spec.Table != "" {
		if _, ok := s.Tables[doc.Spec.Table]; !ok {
			issues = append(issues, Issue{Resource: resource, Field: "spec.table", Message: fmt.Sprintf("unknown table %q", doc.Spec.Table)})
		}
	}
	if doc.Spec.Form != "" {
		if _, ok := s.Forms[doc.Spec.Form]; !ok {
			issues = append(issues, Issue{Resource: resource, Field: "spec.form", Message: fmt.Sprintf("unknown form %q", doc.Spec.Form)})
		}
	}
	return issues
}

// apiMethods are the verbs an Api resource may declare.
var apiMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true,
}

// lintAPI checks the binding, not the handler: whether the named handler exists is the
// application's question, and Lint has no application. What it can prove is that the
// resource is well-formed enough to bind later.
func (s *Set) lintAPI(doc *ApiDoc) Issues {
	resource := resourceKey(KindApi, doc.Metadata.Name)
	var issues Issues
	issue := func(message string) {
		issues = append(issues, Issue{Resource: resource, Message: message})
	}

	if strings.TrimSpace(doc.Spec.Handler) == "" {
		issue("spec.handler is required: an Api binds a registered Go handler")
	}
	if method := strings.TrimSpace(doc.Spec.Method); method != "" && !apiMethods[strings.ToUpper(method)] {
		issue(fmt.Sprintf("unsupported method %q", method))
	}
	if path := strings.TrimSpace(doc.Spec.Path); path == "" {
		issue("spec.path is required")
	} else if strings.HasPrefix(path, "/") {
		issue("spec.path is relative to the runtime prefix and must not start with /")
	} else if strings.Contains(path, "..") || strings.Contains(path, "//") {
		issue(fmt.Sprintf("spec.path %q is not a clean relative path", path))
	}
	if model := strings.TrimSpace(doc.Spec.Model); model != "" && s.Models[model] == nil {
		issue(fmt.Sprintf("unknown model %q", model))
	}
	for _, guard := range doc.Spec.Guards {
		if !resourceNameRE.MatchString(guard) {
			issue(fmt.Sprintf("guard %q must match %s", guard, resourceNameRE))
		}
	}
	return issues
}

// lintMcp checks the metadata a model will read.
func lintMcp(doc *McpDoc) Issues {
	resource := resourceKey(KindMcp, doc.Metadata.Name)
	var issues Issues
	issue := func(message string) {
		issues = append(issues, Issue{Resource: resource, Message: message})
	}

	if strings.TrimSpace(doc.Spec.Handler) == "" {
		issue("spec.handler is required: an Mcp resource names a registered Go handler")
	}
	if name := strings.TrimSpace(doc.Spec.Name); name != "" && !resourceNameRE.MatchString(name) {
		issue(fmt.Sprintf("spec.name %q must match %s", name, resourceNameRE))
	}
	if doc.Spec.Params != nil {
		// The schema is passed through, so the one thing worth checking is its shape: a
		// model that gets a string where an object belongs cannot call the tool.
		if kind, ok := doc.Spec.Params["type"]; ok && kind != "object" {
			issue("spec.params is a JSON Schema object and its type must be \"object\"")
		}
	}
	for _, guard := range doc.Spec.Guards {
		if !resourceNameRE.MatchString(guard) {
			issue(fmt.Sprintf("guard %q must match %s", guard, resourceNameRE))
		}
	}
	return issues
}
