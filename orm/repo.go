package orm

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/maxid/gcosid"

	"github.com/maxid/gable/tenant"
)

// Repo binds a model to a database and optional id generator.
type Repo[T any] struct {
	db    *DB
	model *Model
	ids   gcosid.Generator
}

type repoOptions struct {
	ids gcosid.Generator
}

// RepoOption configures a Repo.
type RepoOption func(*repoOptions)

// WithIDGenerator sets the primary key generator used when the key is zero.
func WithIDGenerator(ids gcosid.Generator) RepoOption {
	return func(o *repoOptions) {
		if ids != nil {
			o.ids = ids
		}
	}
}

// NewRepo returns a repository for T mapped to table.
func NewRepo[T any](db *DB, table string, opts ...RepoOption) (*Repo[T], error) {
	if db == nil {
		return nil, fmt.Errorf("orm: nil database")
	}
	typ := reflect.TypeOf((*T)(nil)).Elem()
	if typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("orm: repository element must be a struct, got %s", typ)
	}
	model, err := ModelOf[T](table)
	if err != nil {
		return nil, err
	}
	options := repoOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	return &Repo[T]{db: db, model: model, ids: options.ids}, nil
}

// Model returns the table metadata.
func (r *Repo[T]) Model() *Model { return r.model }

// Query starts a tenant-scoped query.
func (r *Repo[T]) Query() *Query[T] { return &Query[T]{repo: r} }

// Unscoped starts a query without the tenant predicate.
func (r *Repo[T]) Unscoped() *Query[T] { return &Query[T]{repo: r, unscoped: true} }

// Get loads one row by primary key.
func (r *Repo[T]) Get(ctx context.Context, id int64) (*T, error) {
	where, args, err := r.pkWhere(ctx, id)
	if err != nil {
		return nil, err
	}
	statement := "SELECT " + r.model.quotedColumns(r.db.dialect) + " FROM " + r.db.dialect.Quote(r.model.Table) + " WHERE " + where
	rows, err := r.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, ErrNotFound
	}
	value, err := scanValue[T](rows, r.model)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

// Create inserts the row. A zero primary key is filled by the id generator (or
// by the database when the field is tagged auto). A tenant field is filled from
// the context and a conflicting value is rejected.
func (r *Repo[T]) Create(ctx context.Context, entity *T) error {
	if entity == nil {
		return fmt.Errorf("orm: nil entity")
	}
	value := reflect.ValueOf(entity).Elem()
	model := r.model
	dialect := r.db.dialect

	if model.Tenant != nil {
		current, err := tenant.MustFromContext(ctx)
		if err != nil {
			return err
		}
		field := value.FieldByName(model.Tenant.GoName)
		switch {
		case field.Int() == 0:
			field.SetInt(current.ID)
		case field.Int() != current.ID:
			return ErrTenantMismatch
		}
	}

	now := time.Now()
	setAutoTime(value, model.AutoCreate, now)
	setAutoTime(value, model.AutoUpdate, now)

	autoIncrement := false
	pk := value.FieldByName(model.PK.GoName)
	if pk.Int() == 0 {
		switch {
		case r.ids != nil:
			id, err := r.ids.Next(ctx)
			if err != nil {
				return fmt.Errorf("orm: allocate id: %w", err)
			}
			pk.SetInt(id)
		case model.PK.Auto:
			autoIncrement = true
		default:
			return ErrIDRequired
		}
	}

	var columns []string
	var placeholders []string
	var args []any
	index := 1
	for _, field := range model.Fields {
		if autoIncrement && field.PK {
			continue
		}
		columns = append(columns, dialect.Quote(field.Column))
		placeholders = append(placeholders, dialect.Placeholder(index))
		args = append(args, value.FieldByName(field.GoName).Interface())
		index++
	}

	statement := "INSERT INTO " + dialect.Quote(model.Table) + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	if autoIncrement && dialect.Returning() {
		statement += " RETURNING " + dialect.Quote(model.PK.Column)
		var id int64
		if err := r.db.QueryRowContext(ctx, statement, args...).Scan(&id); err != nil {
			return err
		}
		pk.SetInt(id)
		return nil
	}

	result, err := r.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	if autoIncrement {
		id, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("orm: read last insert id: %w", err)
		}
		pk.SetInt(id)
	}
	return nil
}

// Update writes every mutable column by primary key.
//
// soft_delete and retain columns are owned by Delete and Restore: writing an entity that
// carries a stale timestamp must not resurrect or re-delete a row.
func (r *Repo[T]) Update(ctx context.Context, entity *T) error {
	if entity == nil {
		return fmt.Errorf("orm: nil entity")
	}
	value := reflect.ValueOf(entity).Elem()
	model := r.model
	dialect := r.db.dialect

	pk := value.FieldByName(model.PK.GoName)
	if pk.Int() == 0 {
		return ErrIDRequired
	}
	if model.Tenant != nil {
		current, err := tenant.MustFromContext(ctx)
		if err != nil {
			return err
		}
		field := value.FieldByName(model.Tenant.GoName)
		switch {
		case field.Int() == 0:
			field.SetInt(current.ID)
		case field.Int() != current.ID:
			return ErrTenantMismatch
		}
	}
	setAutoTime(value, model.AutoUpdate, time.Now())

	var sets []string
	var args []any
	index := 1
	for _, field := range model.Fields {
		if field.PK || field.Tenant || field.AutoCreate || field.SoftDelete || field.Retain {
			continue
		}
		sets = append(sets, dialect.Quote(field.Column)+" = "+dialect.Placeholder(index))
		args = append(args, value.FieldByName(field.GoName).Interface())
		index++
	}
	if len(sets) == 0 {
		return fmt.Errorf("orm: no mutable columns")
	}

	where, whereArgs, err := r.pkWhereWith(ctx, pk.Int(), &index, false)
	if err != nil {
		return err
	}
	args = append(args, whereArgs...)
	statement := "UPDATE " + dialect.Quote(model.Table) + " SET " + strings.Join(sets, ", ") + " WHERE " + where
	return execAffected(r.db.ExecContext(ctx, statement, args...))
}

// Delete removes the row, or hides it when the model declares soft_delete or retain.
//
// Both spellings mean the same thing to callers: the row stops appearing in scoped
// queries. Use Purge to remove it for good.
func (r *Repo[T]) Delete(ctx context.Context, id int64) error {
	model := r.model
	if model.SoftDelete == nil && model.Retain == nil {
		return r.Purge(ctx, id)
	}
	dialect := r.db.dialect
	now := time.Now()
	var sets []string
	var args []any
	index := 1
	if model.SoftDelete != nil {
		sets = append(sets, dialect.Quote(model.SoftDelete.Column)+" = "+dialect.Placeholder(index))
		args = append(args, now)
		index++
	}
	if model.Retain != nil {
		sets = append(sets, dialect.Quote(model.Retain.Column)+" = "+dialect.Placeholder(index))
		args = append(args, true)
		index++
	}
	where, whereArgs, err := r.pkWhereWith(ctx, id, &index, false)
	if err != nil {
		return err
	}
	args = append(args, whereArgs...)
	statement := "UPDATE " + dialect.Quote(model.Table) + " SET " + strings.Join(sets, ", ") + " WHERE " + where
	return execAffected(r.db.ExecContext(ctx, statement, args...))
}

// Purge removes the row for good, ignoring soft_delete and retain. It is the explicit
// escape hatch for retention jobs and for tests; ordinary code should call Delete.
func (r *Repo[T]) Purge(ctx context.Context, id int64) error {
	dialect := r.db.dialect
	// Purging a hidden row is the whole point, so the guard must not select it out.
	where, args, err := r.hiddenWhere(ctx, id, nil)
	if err != nil {
		return err
	}
	statement := "DELETE FROM " + dialect.Quote(r.model.Table) + " WHERE " + where
	return execAffected(r.db.ExecContext(ctx, statement, args...))
}

// Restore clears soft_delete and retain on one row.
func (r *Repo[T]) Restore(ctx context.Context, id int64) error {
	model := r.model
	if model.SoftDelete == nil && model.Retain == nil {
		return fmt.Errorf("orm: %s is not soft-deleted", model.Table)
	}
	dialect := r.db.dialect
	var sets []string
	var args []any
	index := 1
	if model.SoftDelete != nil {
		sets = append(sets, dialect.Quote(model.SoftDelete.Column)+" = NULL")
	}
	if model.Retain != nil {
		sets = append(sets, dialect.Quote(model.Retain.Column)+" = "+dialect.Placeholder(index))
		args = append(args, false)
		index++
	}
	// The row is hidden, so the guard is dropped and the key alone selects it.
	where, whereArgs, err := r.hiddenWhere(ctx, id, &index)
	if err != nil {
		return err
	}
	args = append(args, whereArgs...)
	statement := "UPDATE " + dialect.Quote(model.Table) + " SET " + strings.Join(sets, ", ") + " WHERE " + where
	return execAffected(r.db.ExecContext(ctx, statement, args...))
}

func (r *Repo[T]) pkWhere(ctx context.Context, id int64) (string, []any, error) {
	return r.pkWhereWith(ctx, id, nil, false)
}

// hiddenWhere selects a row that the guard would otherwise skip, so Restore can reach it.
func (r *Repo[T]) hiddenWhere(ctx context.Context, id int64, start *int) (string, []any, error) {
	return r.pkWhereWith(ctx, id, start, true)
}

func (r *Repo[T]) pkWhereWith(ctx context.Context, id int64, start *int, includeHidden bool) (string, []any, error) {
	dialect := r.db.dialect
	index := 1
	if start != nil {
		index = *start
	}
	var parts []string
	var args []any

	if r.model.Tenant != nil {
		current, err := tenant.MustFromContext(ctx)
		if err != nil {
			return "", nil, err
		}
		parts = append(parts, dialect.Quote(r.model.Tenant.Column)+" = "+dialect.Placeholder(index))
		args = append(args, current.ID)
		index++
	}
	if !includeHidden {
		visible, visibleArgs, err := visibleClause(r.model, dialect, index)
		if err != nil {
			return "", nil, err
		}
		if visible != "" {
			parts = append(parts, visible)
			args = append(args, visibleArgs...)
			index += len(visibleArgs)
		}
	}
	parts = append(parts, dialect.Quote(r.model.PK.Column)+" = "+dialect.Placeholder(index))
	args = append(args, id)
	if start != nil {
		*start = index + 1
	}
	return strings.Join(parts, " AND "), args, nil
}

func setAutoTime(value reflect.Value, field *Field, now time.Time) {
	if field == nil {
		return
	}
	target := value.FieldByName(field.GoName)
	if target.IsZero() {
		target.Set(reflect.ValueOf(now))
	}
}
