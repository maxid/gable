package dsl

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/maxid/gable/orm"
)

// ModelDDL renders the CREATE TABLE statement for a Model resource.
func (s *Set) ModelDDL(name string, dialect orm.Dialect) (string, error) {
	doc, ok := s.Models[name]
	if !ok {
		return "", fmt.Errorf("dsl: model %q not found", name)
	}
	model, err := doc.ORMModel()
	if err != nil {
		return "", err
	}
	return orm.SchemaSQL(model, dialect)
}

// ORMModel converts a Model resource into the ORM metadata used by the DDL
// renderer.
func (m *ModelDoc) ORMModel() (*orm.Model, error) {
	fields := make([]orm.Field, 0, len(m.Spec.Fields))
	for _, spec := range m.Spec.Fields {
		typ, err := ormType(spec.Type)
		if err != nil {
			return nil, fmt.Errorf("dsl: model %s field %s: %w", m.Metadata.Name, spec.Name, err)
		}
		fields = append(fields, orm.Field{
			GoName:     spec.Name,
			Column:     spec.Name,
			Type:       typ,
			PK:         spec.Primary,
			Tenant:     spec.Tenant,
			Auto:       spec.Generator == "auto",
			AutoCreate: spec.AutoCreate,
			AutoUpdate: spec.AutoUpdate,
			Unique:     spec.Unique,
			Text:       spec.Type == "text" || spec.Type == "json",
			Size:       spec.Size,
		})
	}
	model := &orm.Model{Table: m.TableName(), Fields: fields}
	for i := range model.Fields {
		field := &model.Fields[i]
		if field.PK {
			model.PK = field
		}
		if field.Tenant {
			model.Tenant = field
		}
		if field.AutoCreate {
			model.AutoCreate = field
		}
		if field.AutoUpdate {
			model.AutoUpdate = field
		}
	}
	if model.PK == nil {
		return nil, fmt.Errorf("dsl: model %s has no primary field", m.Metadata.Name)
	}
	return model, nil
}

func ormType(kind string) (reflect.Type, error) {
	switch kind {
	case "int64":
		return reflect.TypeOf(int64(0)), nil
	case "int32":
		return reflect.TypeOf(int32(0)), nil
	case "string", "text":
		return reflect.TypeOf(""), nil
	case "bool":
		return reflect.TypeOf(false), nil
	case "float64":
		return reflect.TypeOf(float64(0)), nil
	case "time":
		return reflect.TypeOf(time.Time{}), nil
	case "json":
		return reflect.TypeOf(json.RawMessage{}), nil
	default:
		return nil, fmt.Errorf("unsupported type %q", kind)
	}
}

// Preview returns the preview payload for a resource. Models include their DDL;
// Table/Form/Page return the low-code IR document.
func (s *Set) Preview(kind Kind, name string, dialect orm.Dialect) (any, error) {
	if kind != KindModel {
		return s.IR(kind, name)
	}
	doc, ok := s.Models[name]
	if !ok {
		return nil, fmt.Errorf("dsl: model %q not found", name)
	}
	ddl, err := s.ModelDDL(name, dialect)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"apiVersion": doc.APIVersion,
		"kind":       doc.Kind,
		"metadata":   doc.Metadata,
		"spec":       doc.Spec,
		"table":      doc.TableName(),
		"ddl":        ddl,
	}, nil
}
