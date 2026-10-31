package orm

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/maxid/gable/tenant"
)

// Cond is one WHERE condition. Raw bypasses column validation and is the
// caller's responsibility.
type Cond struct {
	column  string
	op      string
	value   any
	values  []any
	raw     string
	rawArgs []any
}

// Eq matches column = value. A nil value renders IS NULL.
func Eq(column string, value any) Cond { return Cond{column: column, op: "=", value: value} }

// Ne matches column <> value.
func Ne(column string, value any) Cond { return Cond{column: column, op: "<>", value: value} }

// Gt matches column > value.
func Gt(column string, value any) Cond { return Cond{column: column, op: ">", value: value} }

// Lt matches column < value.
func Lt(column string, value any) Cond { return Cond{column: column, op: "<", value: value} }

// Like matches column LIKE pattern. The caller supplies the wildcards.
func Like(column, pattern string) Cond {
	return Cond{column: column, op: "LIKE", value: pattern}
}

// In matches column IN (values...). An empty list matches nothing.
func In(column string, values ...any) Cond {
	return Cond{column: column, op: "IN", values: values}
}

// IsNull matches column IS NULL.
func IsNull(column string) Cond { return Cond{column: column, op: "IS NULL"} }

// Raw injects a raw SQL fragment. Placeholders must match the dialect of the
// database the query runs on.
func Raw(where string, args ...any) Cond {
	return Cond{raw: where, rawArgs: args}
}

func (c Cond) build(d Dialect, index *int) (string, []any, error) {
	if c.raw != "" {
		return c.raw, append([]any{}, c.rawArgs...), nil
	}
	if !identifierRE.MatchString(c.column) {
		return "", nil, fmt.Errorf("orm: invalid condition column %q", c.column)
	}
	quoted := d.Quote(c.column)

	switch c.op {
	case "IN":
		if len(c.values) == 0 {
			return "1=0", nil, nil
		}
		placeholders := make([]string, 0, len(c.values))
		for range c.values {
			placeholders = append(placeholders, d.Placeholder(*index))
			*index++
		}
		return quoted + " IN (" + strings.Join(placeholders, ", ") + ")", append([]any{}, c.values...), nil
	case "IS NULL":
		return quoted + " IS NULL", nil, nil
	case "=":
		if c.value == nil {
			return quoted + " IS NULL", nil, nil
		}
	}
	if c.value == nil {
		return "", nil, fmt.Errorf("orm: %s requires a value", c.op)
	}
	placeholder := d.Placeholder(*index)
	*index++
	return quoted + " " + c.op + " " + placeholder, []any{c.value}, nil
}

type orderBy struct {
	column string
	desc   bool
}

// Query is a typed query builder. Conditions are ANDed.
type Query[T any] struct {
	repo     *Repo[T]
	conds    []Cond
	orders   []orderBy
	limit    int
	offset   int
	unscoped bool
	err      error
}

// Where adds AND conditions.
func (q *Query[T]) Where(conds ...Cond) *Query[T] {
	q.conds = append(q.conds, conds...)
	return q
}

// OrderBy adds an order clause.
func (q *Query[T]) OrderBy(column string, desc bool) *Query[T] {
	if !identifierRE.MatchString(column) {
		q.err = fmt.Errorf("orm: invalid order column %q", column)
		return q
	}
	q.orders = append(q.orders, orderBy{column: column, desc: desc})
	return q
}

// Limit bounds the row count. Zero means no limit.
func (q *Query[T]) Limit(limit int) *Query[T] {
	q.limit = limit
	return q
}

// Offset skips rows.
func (q *Query[T]) Offset(offset int) *Query[T] {
	q.offset = offset
	return q
}

// Page applies 1-based pagination. A negative size means "no limit".
func (q *Query[T]) Page(number, size int) *Query[T] {
	if number < 1 {
		number = 1
	}
	if size < 0 {
		q.limit = 0
		q.offset = 0
		return q
	}
	if size == 0 {
		size = 20
	}
	q.limit = size
	q.offset = (number - 1) * size
	return q
}

// Unscoped disables the tenant predicate. Use it only for privileged reads.
func (q *Query[T]) Unscoped() *Query[T] {
	q.unscoped = true
	return q
}

// All returns every matching row.
func (q *Query[T]) All(ctx context.Context) ([]T, error) {
	if q.err != nil {
		return nil, q.err
	}
	model := q.repo.model
	dialect := q.repo.db.dialect

	where, args, err := q.where(ctx)
	if err != nil {
		return nil, err
	}
	statement := "SELECT " + model.quotedColumns(dialect) + " FROM " + dialect.Quote(model.Table)
	if where != "" {
		statement += " WHERE " + where
	}
	if len(q.orders) > 0 {
		parts := make([]string, 0, len(q.orders))
		for _, order := range q.orders {
			direction := "ASC"
			if order.desc {
				direction = "DESC"
			}
			parts = append(parts, dialect.Quote(order.column)+" "+direction)
		}
		statement += " ORDER BY " + strings.Join(parts, ", ")
	}
	if pagination := dialect.Pagination(q.limit, q.offset); pagination != "" {
		statement += " " + pagination
	}

	rows, err := q.repo.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("orm: query %s: %w", statement, err)
	}
	defer rows.Close()

	results := make([]T, 0)
	for rows.Next() {
		value, err := scanValue[T](rows, model)
		if err != nil {
			return nil, err
		}
		results = append(results, value)
	}
	return results, rows.Err()
}

// First returns the first matching row or ErrNotFound.
func (q *Query[T]) First(ctx context.Context) (*T, error) {
	clone := *q
	clone.limit = 1
	clone.offset = 0
	rows, err := clone.All(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

// Count returns the number of matching rows.
func (q *Query[T]) Count(ctx context.Context) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}
	model := q.repo.model
	dialect := q.repo.db.dialect
	where, args, err := q.where(ctx)
	if err != nil {
		return 0, err
	}
	statement := "SELECT COUNT(*) FROM " + dialect.Quote(model.Table)
	if where != "" {
		statement += " WHERE " + where
	}
	var count int64
	if err := q.repo.db.QueryRowContext(ctx, statement, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (q *Query[T]) where(ctx context.Context) (string, []any, error) {
	model := q.repo.model
	dialect := q.repo.db.dialect
	index := 1
	var clauses []string
	var args []any

	if model.Tenant != nil && !q.unscoped {
		current, err := tenant.MustFromContext(ctx)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, dialect.Quote(model.Tenant.Column)+" = "+dialect.Placeholder(index))
		args = append(args, current.ID)
		index++
	}

	for _, cond := range q.conds {
		clause, condArgs, err := cond.build(dialect, &index)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, clause)
		args = append(args, condArgs...)
	}
	return strings.Join(clauses, " AND "), args, nil
}

func scanValue[T any](rows *sql.Rows, model *Model) (T, error) {
	var zero T
	value := reflect.New(model.Type).Elem()
	destinations := make([]any, len(model.Fields))
	for i, field := range model.Fields {
		destinations[i] = value.FieldByName(field.GoName).Addr().Interface()
	}
	if err := rows.Scan(destinations...); err != nil {
		return zero, err
	}
	typed, ok := value.Interface().(T)
	if !ok {
		return zero, fmt.Errorf("orm: cannot scan %s into %T", model.Type, zero)
	}
	return typed, nil
}
