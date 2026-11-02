package orm

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// RelationKind is the direction of a relation.
type RelationKind string

const (
	// HasMany links one row of this model to many rows of another: the other table
	// carries the foreign key.
	HasMany RelationKind = "has_many"
	// BelongsTo links this row to one row of another: this table carries the foreign key.
	BelongsTo RelationKind = "belongs_to"
)

// Relation is a struct field that Preload fills.
//
// A relation is metadata, not a column: the field is never part of a SELECT list, and
// loading it is always an explicit Preload on the query that asked for it — there is no
// lazy loading, so a handler cannot accidentally issue a query per row.
type Relation struct {
	// Name is the struct field.
	Name string
	Kind RelationKind
	// Table is the related table.
	Table string
	// ForeignKey is the column that carries the key, wherever it lives: on the related
	// table for HasMany, on this table for BelongsTo.
	ForeignKey string
	// Many reports whether the field holds a slice.
	Many bool
}

// relationOf reads a relation tag, if the field carries one.
func relationOf(field reflect.StructField, tag string) (Relation, bool, error) {
	var relation Relation
	for _, part := range strings.Split(tag, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch name {
		case string(HasMany), string(BelongsTo):
			relation.Kind = RelationKind(name)
			relation.Table = strings.TrimSpace(value)
		case "foreign_key":
			relation.ForeignKey = strings.TrimSpace(value)
		}
	}
	if relation.Kind == "" {
		return Relation{}, false, nil
	}
	relation.Name = field.Name

	if !identifierRE.MatchString(relation.Table) {
		return Relation{}, false, fmt.Errorf("orm: relation %s has an invalid table %q", relation.Name, relation.Table)
	}
	if !identifierRE.MatchString(relation.ForeignKey) {
		return Relation{}, false, fmt.Errorf("orm: relation %s needs foreign_key=<column>", relation.Name)
	}

	typ := field.Type
	if typ.Kind() == reflect.Slice {
		relation.Many = true
		typ = typ.Elem()
		if typ.Kind() == reflect.Ptr {
			typ = typ.Elem()
		}
		if relation.Kind != HasMany {
			return Relation{}, false, fmt.Errorf("orm: belongs_to field %s must be a struct or *struct", relation.Name)
		}
		if typ.Kind() != reflect.Struct {
			return Relation{}, false, fmt.Errorf("orm: has_many field %s must slice a struct", relation.Name)
		}
		return relation, true, nil
	}
	if typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return Relation{}, false, fmt.Errorf("orm: relation field %s must be a struct, *struct or slice", relation.Name)
	}
	if relation.Kind != BelongsTo {
		return Relation{}, false, fmt.Errorf("orm: has_many field %s must be a slice", relation.Name)
	}
	return relation, true, nil
}

// loadedRow is one related row with the key that matches it to its parent.
type loadedRow struct {
	Key  any
	Item any
}

// relationLoader is what a repository offers a Preload. It is implemented by Repo[U], so
// a preload stays type-safe at the call site while the query that runs it is generic.
type relationLoader interface {
	loadRelated(ctx context.Context, filterColumn string, values []any, keyColumn string, unscoped bool) ([]loadedRow, error)
}

// loadRelated implements relationLoader for the related model.
//
// An empty column means the related model's primary key: belongs_to filters on the
// related key and has_many on its own foreign key, so neither the parent model nor the
// query has to know the related model's columns.
func (r *Repo[T]) loadRelated(ctx context.Context, filterColumn string, values []any, keyColumn string, unscoped bool) ([]loadedRow, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if filterColumn == "" {
		filterColumn = r.model.PK.Column
	}
	if keyColumn == "" {
		keyColumn = filterColumn
	}
	key := r.model.fieldByColumn(keyColumn)
	if key == nil {
		return nil, fmt.Errorf("%s has no column %q", r.model.Table, keyColumn)
	}

	query := r.Query().Where(In(filterColumn, values...))
	if unscoped {
		query = query.Unscoped()
	}
	rows, err := query.All(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]loadedRow, 0, len(rows))
	for i := range rows {
		value := reflect.ValueOf(&rows[i]).Elem().FieldByName(key.GoName)
		out = append(out, loadedRow{Key: value.Interface(), Item: &rows[i]})
	}
	return out, nil
}

// Preload loads a relation for the rows this query returns, in one extra query per
// relation rather than one per row.
//
// The name is the struct field; the repository passed in is the related model's. A
// relation declared on the entity but not asked for here stays empty, and a name that
// does not exist is a query error instead of a silently skipped load. The related rows
// obey the same tenant and soft-delete guards as this query, including Unscoped.
func (q *Query[T]) Preload(name string, related relationLoader) *Query[T] {
	if related == nil {
		q.err = fmt.Errorf("orm: preload %s: nil repository", name)
		return q
	}
	if q.preloads == nil {
		q.preloads = map[string]relationLoader{}
	}
	q.preloads[name] = related
	return q
}

// preload fills the requested relations on the loaded rows.
func (q *Query[T]) preload(ctx context.Context, rows []T) error {
	for name, related := range q.preloads {
		relation, ok := q.repo.model.relation(name)
		if !ok {
			return fmt.Errorf("orm: %s has no relation %q", q.repo.model.Table, name)
		}
		if err := q.preloadOne(ctx, relation, related, rows); err != nil {
			return fmt.Errorf("orm: preload %s: %w", name, err)
		}
	}
	return nil
}

func (q *Query[T]) preloadOne(ctx context.Context, relation Relation, related relationLoader, rows []T) error {
	model := q.repo.model

	// The key this side contributes: the foreign key for belongs_to (it lives on this
	// table), the primary key for has_many (the foreign key lives on the related table,
	// which is also what the related read filters on).
	var localField *Field
	filterColumn := relation.ForeignKey
	if relation.Kind == BelongsTo {
		localField = model.fieldByColumn(relation.ForeignKey)
		if localField == nil {
			return fmt.Errorf("%s has no column %q", model.Table, relation.ForeignKey)
		}
		// This row carries the related key, so match on the related primary key.
		filterColumn = ""
	} else {
		localField = model.PK
	}

	keys := make([]any, 0, len(rows))
	seen := map[string]bool{}
	local := make([]string, len(rows))
	for i := range rows {
		value := reflect.ValueOf(&rows[i]).Elem().FieldByName(localField.GoName)
		key := value.Interface()
		local[i] = keyString(key)
		if seen[local[i]] {
			continue
		}
		seen[local[i]] = true
		keys = append(keys, key)
	}

	loaded, err := related.loadRelated(ctx, filterColumn, keys, filterColumn, q.unscoped)
	if err != nil {
		return err
	}
	grouped := map[string][]any{}
	for _, row := range loaded {
		grouped[keyString(row.Key)] = append(grouped[keyString(row.Key)], row.Item)
	}

	for i := range rows {
		target := reflect.ValueOf(&rows[i]).Elem().FieldByName(relation.Name)
		matches := grouped[local[i]]
		if relation.Many {
			slice := reflect.MakeSlice(target.Type(), 0, len(matches))
			for _, item := range matches {
				element := reflect.ValueOf(item).Elem()
				if target.Type().Elem().Kind() == reflect.Ptr {
					slice = reflect.Append(slice, element.Addr())
					continue
				}
				slice = reflect.Append(slice, element)
			}
			target.Set(slice)
			continue
		}
		if len(matches) == 0 {
			target.Set(reflect.Zero(target.Type()))
			continue
		}
		element := reflect.ValueOf(matches[0]).Elem()
		if target.Kind() == reflect.Ptr {
			target.Set(element.Addr())
			continue
		}
		target.Set(element)
	}
	return nil
}

// relation returns a declared relation by struct field name.
func (m *Model) relation(name string) (Relation, bool) {
	for _, relation := range m.Relations {
		if relation.Name == name {
			return relation, true
		}
	}
	return Relation{}, false
}

// keyString normalizes a key for grouping. Scanned keys are int64 while a declared field
// may be int, so keys are compared as text instead of by Go type.
func keyString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case int64:
		return strconv.FormatInt(typed, 10)
	case int:
		return strconv.Itoa(typed)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}
