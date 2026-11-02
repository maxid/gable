package orm

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

var identifierRE = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// Field is one mapped struct field.
type Field struct {
	GoName     string
	Column     string
	Type       reflect.Type
	PK         bool
	Auto       bool
	Tenant     bool
	AutoCreate bool
	AutoUpdate bool
	Unique     bool
	SoftDelete bool
	Retain     bool
	Text       bool
	Size       int
}

// Model is the table metadata inferred from a Go struct.
type Model struct {
	Table      string
	Type       reflect.Type
	Fields     []Field
	Relations  []Relation
	PK         *Field
	Tenant     *Field
	AutoCreate *Field
	AutoUpdate *Field
	// SoftDelete and Retain are the two supported ways of hiding a deleted row:
	// a timestamp column that stops being NULL, or a flag that flips. Both are
	// excluded by default and reachable only through Unscoped.
	SoftDelete *Field
	Retain     *Field
}

var modelCache sync.Map // "type|table" -> *Model

// ModelOf returns the metadata for T mapped to table.
func ModelOf[T any](table string) (*Model, error) {
	t := reflect.TypeOf((*T)(nil)).Elem()
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("orm: %T is not a struct", *new(T))
	}
	key := t.String() + "|" + table
	if cached, ok := modelCache.Load(key); ok {
		return cached.(*Model), nil
	}
	model, err := buildModel(t, table)
	if err != nil {
		return nil, err
	}
	modelCache.Store(key, model)
	return model, nil
}

func buildModel(t reflect.Type, table string) (*Model, error) {
	table = strings.ToLower(strings.TrimSpace(table))
	if !identifierRE.MatchString(table) {
		return nil, fmt.Errorf("orm: invalid table name %q", table)
	}

	model := &Model{Table: table, Type: t}
	for i := 0; i < t.NumField(); i++ {
		structField := t.Field(i)
		if structField.PkgPath != "" {
			continue
		}
		tag := structField.Tag.Get("gable")
		if tag == "-" {
			continue
		}
		// A relation is not a column: it is loaded by Preload, never selected.
		if relation, ok, err := relationOf(structField, tag); err != nil {
			return nil, err
		} else if ok {
			model.Relations = append(model.Relations, relation)
			continue
		}

		field := Field{
			GoName: structField.Name,
			Column: columnName(structField),
			Type:   structField.Type,
			Size:   255,
		}
		for _, part := range strings.Split(tag, ",") {
			part = strings.TrimSpace(part)
			switch {
			case part == "pk":
				field.PK = true
			case part == "auto":
				field.Auto = true
			case part == "tenant":
				field.Tenant = true
			case part == "auto_create":
				field.AutoCreate = true
			case part == "auto_update":
				field.AutoUpdate = true
			case part == "unique":
				field.Unique = true
			case part == "soft_delete":
				field.SoftDelete = true
			case part == "retain":
				field.Retain = true
			case strings.HasPrefix(part, "column="):
				field.Column = strings.TrimSpace(strings.TrimPrefix(part, "column="))
			case strings.HasPrefix(part, "size="):
				var size int
				if _, err := fmt.Sscanf(strings.TrimPrefix(part, "size="), "%d", &size); err == nil && size > 0 {
					field.Size = size
				}
			}
		}
		if !identifierRE.MatchString(field.Column) {
			return nil, fmt.Errorf("orm: invalid column %q on %s", field.Column, structField.Name)
		}
		if field.Tenant && structField.Type.Kind() != reflect.Int64 {
			return nil, fmt.Errorf("orm: tenant field %s must be int64", structField.Name)
		}
		if field.SoftDelete && structField.Type != reflect.TypeOf(&time.Time{}) {
			return nil, fmt.Errorf("orm: soft_delete field %s must be *time.Time", structField.Name)
		}
		if field.Retain && structField.Type.Kind() != reflect.Bool {
			return nil, fmt.Errorf("orm: retain field %s must be bool", structField.Name)
		}

		model.Fields = append(model.Fields, field)
	}

	for i := range model.Fields {
		field := &model.Fields[i]
		switch {
		case field.PK:
			if model.PK != nil {
				return nil, fmt.Errorf("orm: %s has more than one primary key", t.Name())
			}
			model.PK = field
		case field.Tenant:
			if model.Tenant != nil {
				return nil, fmt.Errorf("orm: %s has more than one tenant field", t.Name())
			}
			model.Tenant = field
		}
		if field.AutoCreate {
			model.AutoCreate = field
		}
		if field.AutoUpdate {
			model.AutoUpdate = field
		}
		if field.SoftDelete {
			if model.SoftDelete != nil {
				return nil, fmt.Errorf("orm: %s has more than one soft_delete field", t.Name())
			}
			model.SoftDelete = field
		}
		if field.Retain {
			if model.Retain != nil {
				return nil, fmt.Errorf("orm: %s has more than one retain field", t.Name())
			}
			model.Retain = field
		}
	}
	if model.SoftDelete != nil && model.Retain != nil {
		return nil, fmt.Errorf("orm: %s declares both soft_delete and retain; pick one", t.Name())
	}
	if model.PK == nil {
		return nil, fmt.Errorf("orm: %s has no primary key field", t.Name())
	}
	if model.AutoCreate != nil && model.AutoCreate.Type != reflect.TypeOf(time.Time{}) {
		return nil, fmt.Errorf("orm: auto_create field must be time.Time")
	}
	if model.AutoUpdate != nil && model.AutoUpdate.Type != reflect.TypeOf(time.Time{}) {
		return nil, fmt.Errorf("orm: auto_update field must be time.Time")
	}
	return model, nil
}

func columnName(field reflect.StructField) string {
	if tag := field.Tag.Get("gable"); tag != "" {
		for _, part := range strings.Split(tag, ",") {
			if strings.HasPrefix(part, "column=") {
				return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(part, "column=")))
			}
		}
	}
	if tag := field.Tag.Get("db"); tag != "" {
		if name := strings.Split(tag, ",")[0]; name != "" {
			return strings.ToLower(name)
		}
	}
	if tag := field.Tag.Get("json"); tag != "" {
		if name := strings.Split(tag, ",")[0]; name != "" && name != "-" {
			return strings.ToLower(name)
		}
	}
	return snakeCase(field.Name)
}

func snakeCase(value string) string {
	var builder strings.Builder
	runes := []rune(value)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || nextLower {
					builder.WriteByte('_')
				}
			}
			builder.WriteRune(unicode.ToLower(r))
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func (m *Model) fieldByColumn(column string) *Field {
	for i := range m.Fields {
		if m.Fields[i].Column == column {
			return &m.Fields[i]
		}
	}
	return nil
}

func (m *Model) quotedColumns(d Dialect) string {
	names := make([]string, 0, len(m.Fields))
	for _, field := range m.Fields {
		names = append(names, d.Quote(field.Column))
	}
	return strings.Join(names, ", ")
}
