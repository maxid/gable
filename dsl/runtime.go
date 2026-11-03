package dsl

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/maxid/gcosid"

	gableerrors "github.com/maxid/gable/errors"
	"github.com/maxid/gable/orm"
	"github.com/maxid/gable/tenant"
)

// Runtime executes CRUD for DSL models against a real database.
//
// The resource set is a snapshot the runtime swaps on Reload, never a value it mutates: a
// request either sees the whole old set or the whole new one, so a reload cannot serve a model
// whose table metadata is half-replaced.
type Runtime struct {
	connectors *Connectors
	set        atomic.Pointer[Set]
	ids        map[string]gcosid.Generator
	defaultIDs gcosid.Generator
}

// RuntimeOption configures a Runtime.
type RuntimeOption func(*Runtime)

// WithIDGenerator sets the generator for one model.
func WithIDGenerator(model string, generator gcosid.Generator) RuntimeOption {
	return func(r *Runtime) {
		if generator != nil {
			r.ids[model] = generator
		}
	}
}

// WithDefaultIDGenerator sets the fallback generator for models that do not
// have their own.
func WithDefaultIDGenerator(generator gcosid.Generator) RuntimeOption {
	return func(r *Runtime) { r.defaultIDs = generator }
}

// WithConnectors binds the runtime to a connector set instead of a single database.
//
// The default connector is the one passed to NewRuntime; this option adds the named ones a
// model may point at.
func WithConnectors(connectors *Connectors) RuntimeOption {
	return func(r *Runtime) {
		if connectors != nil {
			r.connectors = connectors
		}
	}
}

// NewRuntime validates the set and binds it to a database.
func NewRuntime(db *orm.DB, set *Set, opts ...RuntimeOption) (*Runtime, error) {
	if db == nil {
		return nil, fmt.Errorf("dsl: nil database")
	}
	if set == nil {
		return nil, fmt.Errorf("dsl: nil set")
	}
	if issues := set.Lint(); len(issues) > 0 {
		return nil, issues
	}
	runtime := &Runtime{connectors: NewConnectors(db), ids: map[string]gcosid.Generator{}}
	runtime.set.Store(set)
	for _, opt := range opts {
		opt(runtime)
	}
	// A model that names an unregistered connector is a wiring error, not a 500 on the first
	// request against it.
	if issues := runtime.connectors.Validate(set); len(issues) > 0 {
		return nil, issues
	}
	return runtime, nil
}

// Connectors returns the connector set the runtime resolves models through.
func (r *Runtime) Connectors() *Connectors { return r.connectors }

// dbFor returns the database of the connector a model names.
func (r *Runtime) dbFor(doc *ModelDoc) (*orm.DB, error) { return r.connectors.For(doc) }

// dialectFor renders SQL for the model's connector. Resolution cannot fail here: an unknown
// connector is rejected when the set is bound, so this is only the quoting style.
func (r *Runtime) dialectFor(doc *ModelDoc) orm.Dialect {
	db, err := r.dbFor(doc)
	if err != nil {
		return r.connectors.Default().Dialect()
	}
	return db.Dialect()
}

// Set returns the loaded resources.
func (r *Runtime) Set() *Set { return r.current() }

// current returns the active snapshot. A request takes it once and uses it throughout, so a
// reload in the middle of a request cannot change the model it is querying.
func (r *Runtime) current() *Set {
	snapshot := r.set.Load()
	if snapshot == nil {
		return NewSet()
	}
	return snapshot
}

// Reload replaces the resources the data API serves.
//
// The new set is linted first: a set that does not lint is refused and the running snapshot
// keeps serving, because accepting a broken definition would take a working API down. A
// resource removed from the new set stops being reachable on the next request — that is what
// "unload" means here.
func (r *Runtime) Reload(set *Set) error {
	if set == nil {
		return fmt.Errorf("dsl: nil set")
	}
	if issues := set.Lint(); len(issues) > 0 {
		return issues
	}
	r.set.Store(set)
	return nil
}

// Migrate creates missing tables for every model.
func (r *Runtime) Migrate(context.Context) error {
	current := r.current()
	for _, name := range sortedKeys(current.Models) {
		doc := current.Models[name]
		model, err := doc.ORMModel()
		if err != nil {
			return err
		}
		db, err := r.dbFor(doc)
		if err != nil {
			return err
		}
		if err := db.AutoMigrate(model); err != nil {
			return err
		}
	}
	return nil
}

// ListOptions controls a dynamic list query.
type ListOptions struct {
	Page    int
	Size    int
	Sort    string
	Order   string
	Filters map[string]string
}

// PageResult is the dynamic list response.
type PageResult struct {
	Items []map[string]any `json:"items"`
	Total int64            `json:"total"`
	Page  int              `json:"page"`
	Size  int              `json:"size"`
}

// List returns a page of rows for a model.
func (r *Runtime) List(ctx context.Context, model string, opts ListOptions) (*PageResult, error) {
	doc, err := r.model(model)
	if err != nil {
		return nil, err
	}
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.Size <= 0 {
		opts.Size = 20
	}
	if opts.Size > 200 {
		opts.Size = 200
	}
	sortField := doc.Primary().Name
	if opts.Sort != "" {
		if doc.Field(opts.Sort) == nil {
			return nil, invalid("unknown sort field " + opts.Sort)
		}
		sortField = opts.Sort
	}
	order := "ASC"
	if strings.EqualFold(opts.Order, "desc") {
		order = "DESC"
	}

	where, args, err := r.where(ctx, doc, opts.Filters)
	if err != nil {
		return nil, err
	}
	db, err := r.dbFor(doc)
	if err != nil {
		return nil, err
	}
	dialect := db.Dialect()
	countSQL := "SELECT COUNT(*) FROM " + dialect.Quote(doc.TableName())
	if where != "" {
		countSQL += " WHERE " + where
	}
	var total int64
	if err := db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, err
	}

	statement := "SELECT " + r.columns(doc) + " FROM " + dialect.Quote(doc.TableName())
	if where != "" {
		statement += " WHERE " + where
	}
	statement += " ORDER BY " + dialect.Quote(sortField) + " " + order
	offset := (opts.Page - 1) * opts.Size
	if pagination := dialect.Pagination(opts.Size, offset); pagination != "" {
		statement += " " + pagination
	}

	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := r.scan(doc, rows)
	if err != nil {
		return nil, err
	}
	return &PageResult{Items: items, Total: total, Page: opts.Page, Size: opts.Size}, nil
}

// Get returns one row by primary key.
func (r *Runtime) Get(ctx context.Context, model string, id int64) (map[string]any, error) {
	doc, err := r.model(model)
	if err != nil {
		return nil, err
	}
	where, args, err := r.whereByID(ctx, doc, id)
	if err != nil {
		return nil, err
	}
	db, err := r.dbFor(doc)
	if err != nil {
		return nil, err
	}
	dialect := db.Dialect()
	statement := "SELECT " + r.columns(doc) + " FROM " + dialect.Quote(doc.TableName()) + " WHERE " + where
	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	item, err := r.scanOne(doc, rows)
	if err != nil {
		return nil, err
	}
	return item, nil
}

// Create inserts a row from a JSON-shaped map.
func (r *Runtime) Create(ctx context.Context, model string, payload map[string]any) (map[string]any, error) {
	doc, err := r.model(model)
	if err != nil {
		return nil, err
	}
	if err := r.validatePayload(doc, payload, false); err != nil {
		return nil, err
	}

	values := map[string]any{}
	current, hasTenant, err := r.currentTenant(ctx, doc)
	if err != nil {
		return nil, err
	}
	hasTenant = hasTenant && current.ID != 0

	for _, field := range doc.Spec.Fields {
		switch {
		case field.Primary:
			continue
		case field.Tenant:
			if !hasTenant {
				return nil, tenant.ErrRequired
			}
			values[field.Name] = current.ID
		case field.AutoCreate || field.AutoUpdate:
			values[field.Name] = time.Now()
		default:
			raw, ok := payload[field.Name]
			if ok && raw != nil {
				converted, err := toDBValue(&field, raw)
				if err != nil {
					return nil, err
				}
				values[field.Name] = converted
				continue
			}
			if field.Default != nil {
				converted, err := toDBValue(&field, field.Default)
				if err != nil {
					return nil, err
				}
				values[field.Name] = converted
				continue
			}
			if field.Required {
				return nil, invalid("field " + field.Name + " is required")
			}
		}
	}

	primary := doc.Primary()
	autoIncrement := false
	if primary.Generator == "auto" {
		autoIncrement = true
	} else if raw, ok := payload[primary.Name]; ok && raw != nil {
		id, err := toInt64(raw)
		if err != nil {
			return nil, invalid("primary key must be an integer")
		}
		values[primary.Name] = id
	} else {
		generator := r.ids[doc.Metadata.Name]
		if generator == nil {
			generator = r.defaultIDs
		}
		if generator == nil {
			return nil, invalid("no id generator for model " + doc.Metadata.Name)
		}
		id, err := generator.Next(ctx)
		if err != nil {
			return nil, err
		}
		values[primary.Name] = id
	}

	db, err := r.dbFor(doc)
	if err != nil {
		return nil, err
	}
	var columns []string
	var placeholders []string
	var args []any
	index := 1
	dialect := db.Dialect()
	for _, field := range doc.Spec.Fields {
		value, ok := values[field.Name]
		if !ok || (autoIncrement && field.Primary) {
			continue
		}
		columns = append(columns, dialect.Quote(field.Name))
		placeholders = append(placeholders, dialect.Placeholder(index))
		args = append(args, value)
		index++
	}

	statement := "INSERT INTO " + dialect.Quote(doc.TableName()) + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	if autoIncrement && dialect.Returning() {
		statement += " RETURNING " + dialect.Quote(primary.Name)
		var id int64
		if err := db.QueryRowContext(ctx, statement, args...).Scan(&id); err != nil {
			return nil, err
		}
		values[primary.Name] = id
	} else {
		result, err := db.ExecContext(ctx, statement, args...)
		if err != nil {
			return nil, err
		}
		if autoIncrement {
			id, err := result.LastInsertId()
			if err != nil {
				return nil, err
			}
			values[primary.Name] = id
		}
	}
	id, err := toInt64(values[primary.Name])
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, model, id)
}

// Update updates the supplied fields and returns the new row.
func (r *Runtime) Update(ctx context.Context, model string, id int64, payload map[string]any) (map[string]any, error) {
	doc, err := r.model(model)
	if err != nil {
		return nil, err
	}
	if err := r.validatePayload(doc, payload, true); err != nil {
		return nil, err
	}

	db, err := r.dbFor(doc)
	if err != nil {
		return nil, err
	}
	var sets []string
	var args []any
	index := 1
	dialect := db.Dialect()
	for _, field := range doc.Spec.Fields {
		if field.Primary || field.Tenant || field.AutoCreate {
			continue
		}
		if field.AutoUpdate {
			sets = append(sets, dialect.Quote(field.Name)+" = "+dialect.Placeholder(index))
			args = append(args, time.Now())
			index++
			continue
		}
		raw, ok := payload[field.Name]
		if !ok {
			continue
		}
		converted, err := toDBValue(&field, raw)
		if err != nil {
			return nil, err
		}
		sets = append(sets, dialect.Quote(field.Name)+" = "+dialect.Placeholder(index))
		args = append(args, converted)
		index++
	}
	if len(sets) == 0 {
		return nil, invalid("no fields to update")
	}
	where, whereArgs, err := r.whereByIDFrom(ctx, doc, id, &index)
	if err != nil {
		return nil, err
	}
	args = append(args, whereArgs...)
	statement := "UPDATE " + dialect.Quote(doc.TableName()) + " SET " + strings.Join(sets, ", ") + " WHERE " + where
	if err := execAffected(db.ExecContext(ctx, statement, args...)); err != nil {
		return nil, err
	}
	return r.Get(ctx, model, id)
}

// Delete removes one row by primary key.
func (r *Runtime) Delete(ctx context.Context, model string, id int64) error {
	doc, err := r.model(model)
	if err != nil {
		return err
	}
	where, args, err := r.whereByID(ctx, doc, id)
	if err != nil {
		return err
	}
	db, err := r.dbFor(doc)
	if err != nil {
		return err
	}
	dialect := db.Dialect()
	statement := "DELETE FROM " + dialect.Quote(doc.TableName()) + " WHERE " + where
	return execAffected(db.ExecContext(ctx, statement, args...))
}

func (r *Runtime) model(name string) (*ModelDoc, error) {
	doc, ok := r.current().Models[name]
	if !ok {
		return nil, notFound("unknown model " + name)
	}
	return doc, nil
}

func (r *Runtime) columns(doc *ModelDoc) string {
	// The quoting style comes from the dialect; every supported dialect quotes identifiers
	// the same way structurally, so the model's own connector decides it.
	db, err := r.dbFor(doc)
	if err != nil {
		db = r.connectors.Default()
	}
	dialect := db.Dialect()
	columns := make([]string, 0, len(doc.Spec.Fields))
	for _, field := range doc.Spec.Fields {
		columns = append(columns, dialect.Quote(field.Name))
	}
	return strings.Join(columns, ", ")
}

func (r *Runtime) currentTenant(ctx context.Context, doc *ModelDoc) (tenant.Tenant, bool, error) {
	if doc.TenantField() == nil {
		return tenant.Tenant{}, false, nil
	}
	current, err := tenant.MustFromContext(ctx)
	if err != nil {
		return tenant.Tenant{}, false, err
	}
	return current, true, nil
}

func (r *Runtime) where(ctx context.Context, doc *ModelDoc, filters map[string]string) (string, []any, error) {
	dialect := r.dialectFor(doc)
	index := 1
	var parts []string
	var args []any

	current, hasTenant, err := r.currentTenant(ctx, doc)
	if err != nil {
		return "", nil, err
	}
	if hasTenant {
		parts = append(parts, dialect.Quote(doc.TenantField().Name)+" = "+dialect.Placeholder(index))
		args = append(args, current.ID)
		index++
	}
	filterNames := make([]string, 0, len(filters))
	for name := range filters {
		filterNames = append(filterNames, name)
	}
	sort.Strings(filterNames)
	for _, name := range filterNames {
		raw := filters[name]
		field := doc.Field(name)
		if field == nil {
			return "", nil, invalid("unknown filter " + name)
		}
		value, err := toDBValue(field, raw)
		if err != nil {
			return "", nil, err
		}
		parts = append(parts, dialect.Quote(field.Name)+" = "+dialect.Placeholder(index))
		args = append(args, value)
		index++
	}
	return strings.Join(parts, " AND "), args, nil
}

func (r *Runtime) whereByID(ctx context.Context, doc *ModelDoc, id int64) (string, []any, error) {
	return r.whereByIDFrom(ctx, doc, id, nil)
}

func (r *Runtime) whereByIDFrom(ctx context.Context, doc *ModelDoc, id int64, start *int) (string, []any, error) {
	dialect := r.dialectFor(doc)
	index := 1
	if start != nil {
		index = *start
	}
	var parts []string
	var args []any
	current, hasTenant, err := r.currentTenant(ctx, doc)
	if err != nil {
		return "", nil, err
	}
	if hasTenant {
		parts = append(parts, dialect.Quote(doc.TenantField().Name)+" = "+dialect.Placeholder(index))
		args = append(args, current.ID)
		index++
	}
	parts = append(parts, dialect.Quote(doc.Primary().Name)+" = "+dialect.Placeholder(index))
	args = append(args, id)
	if start != nil {
		*start = index + 1
	}
	return strings.Join(parts, " AND "), args, nil
}

func (r *Runtime) validatePayload(doc *ModelDoc, payload map[string]any, allowPartial bool) error {
	for key, raw := range payload {
		field := doc.Field(key)
		if field == nil {
			return invalid("unknown field " + key)
		}
		if field.Primary {
			if !allowPartial && field.Generator == "none" {
				continue
			}
			return invalid("field " + key + " is managed by the runtime")
		}
		if field.Tenant || field.AutoCreate || field.AutoUpdate {
			return invalid("field " + key + " is managed by the runtime")
		}
		if raw == nil {
			if field.Required {
				return invalid("field " + field.Name + " is required")
			}
			continue
		}
		if _, err := toDBValue(field, raw); err != nil {
			return err
		}
	}
	if !allowPartial {
		for _, field := range doc.Spec.Fields {
			if field.Primary || field.Tenant || field.AutoCreate || field.AutoUpdate || field.Default != nil {
				continue
			}
			if field.Required {
				if _, ok := payload[field.Name]; !ok {
					return invalid("field " + field.Name + " is required")
				}
			}
		}
	}
	return nil
}

func (r *Runtime) scan(doc *ModelDoc, rows *sql.Rows) ([]map[string]any, error) {
	items := make([]map[string]any, 0)
	for rows.Next() {
		item, err := r.scanRow(doc, rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Runtime) scanOne(doc *ModelDoc, rows *sql.Rows) (map[string]any, error) {
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, notFound("record not found")
	}
	return r.scanRow(doc, rows)
}

func (r *Runtime) scanRow(doc *ModelDoc, rows *sql.Rows) (map[string]any, error) {
	raw := make([]any, len(doc.Spec.Fields))
	destinations := make([]any, len(raw))
	for i := range raw {
		destinations[i] = &raw[i]
	}
	if err := rows.Scan(destinations...); err != nil {
		return nil, err
	}
	item := make(map[string]any, len(raw))
	for i, field := range doc.Spec.Fields {
		item[field.Name] = fromDBValue(&field, raw[i])
	}
	return item, nil
}

func toDBValue(field *FieldSpec, value any) (any, error) {
	switch field.Type {
	case "int64":
		return toInt64(value)
	case "int32":
		id, err := toInt64(value)
		return int32(id), err
	case "float64":
		switch typed := value.(type) {
		case float64:
			return typed, nil
		case float32:
			return float64(typed), nil
		case int:
			return float64(typed), nil
		case int64:
			return float64(typed), nil
		case json.Number:
			return typed.Float64()
		default:
			return nil, invalid("field " + field.Name + " must be a number")
		}
	case "bool":
		switch typed := value.(type) {
		case bool:
			return typed, nil
		case string:
			parsed, err := strconv.ParseBool(typed)
			if err != nil {
				return nil, invalid("field " + field.Name + " must be a boolean")
			}
			return parsed, nil
		default:
			return nil, invalid("field " + field.Name + " must be a boolean")
		}
	case "time":
		switch typed := value.(type) {
		case time.Time:
			return typed, nil
		case string:
			parsed, err := time.Parse(time.RFC3339, typed)
			if err != nil {
				return nil, invalid("field " + field.Name + " must be RFC3339 time")
			}
			return parsed, nil
		default:
			return nil, invalid("field " + field.Name + " must be RFC3339 time")
		}
	case "json":
		if raw, ok := value.(string); ok {
			if !json.Valid([]byte(raw)) {
				return nil, invalid("field " + field.Name + " must be valid JSON")
			}
			return raw, nil
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return string(raw), nil
	case "string", "text":
		text, ok := value.(string)
		if !ok {
			return nil, invalid("field " + field.Name + " must be a string")
		}
		if field.Size > 0 && len([]rune(text)) > field.Size {
			return nil, invalid("field " + field.Name + " exceeds size " + strconv.Itoa(field.Size))
		}
		if len(field.Enum) > 0 {
			found := false
			for _, candidate := range field.Enum {
				if candidate == text {
					found = true
					break
				}
			}
			if !found {
				return nil, invalid("field " + field.Name + " must be one of " + strings.Join(field.Enum, "/"))
			}
		}
		return text, nil
	default:
		return nil, invalid("unsupported field type " + field.Type)
	}
}

func fromDBValue(field *FieldSpec, value any) any {
	if value == nil {
		return nil
	}
	switch field.Type {
	case "int64":
		switch typed := value.(type) {
		case int64:
			return typed
		case int32:
			return int64(typed)
		case int:
			return int64(typed)
		case []byte:
			parsed, _ := strconv.ParseInt(string(typed), 10, 64)
			return parsed
		case string:
			parsed, _ := strconv.ParseInt(typed, 10, 64)
			return parsed
		}
	case "int32":
		switch typed := value.(type) {
		case int64:
			return int32(typed)
		case []byte:
			parsed, _ := strconv.ParseInt(string(typed), 10, 64)
			return int32(parsed)
		}
	case "float64":
		switch typed := value.(type) {
		case float64:
			return typed
		case []byte:
			parsed, _ := strconv.ParseFloat(string(typed), 64)
			return parsed
		}
	case "bool":
		switch typed := value.(type) {
		case bool:
			return typed
		case []byte:
			if len(typed) == 1 {
				return typed[0] != 0
			}
			return string(typed) == "1" || strings.EqualFold(string(typed), "true")
		}
	case "time":
		switch typed := value.(type) {
		case time.Time:
			return typed
		case []byte:
			return string(typed)
		case string:
			return typed
		}
	case "json":
		switch typed := value.(type) {
		case []byte:
			if json.Valid(typed) {
				return json.RawMessage(typed)
			}
			return string(typed)
		case string:
			if json.Valid([]byte(typed)) {
				return json.RawMessage(typed)
			}
			return typed
		}
	case "string", "text":
		if raw, ok := value.([]byte); ok {
			return string(raw)
		}
	}
	return value
}

func toInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case int32:
		return int64(typed), nil
	case int:
		return int64(typed), nil
	case float64:
		if typed != float64(int64(typed)) {
			return 0, invalid("value must be an integer")
		}
		return int64(typed), nil
	case json.Number:
		return typed.Int64()
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return 0, invalid("value must be an integer")
	}
}

func execAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return notFound("record not found")
	}
	return nil
}

func invalid(message string) *gableerrors.Error {
	return gableerrors.New(gableerrors.CodeInvalid, message).WithStatus(400)
}

func notFound(message string) *gableerrors.Error {
	return gableerrors.New(gableerrors.CodeNotFound, message).WithStatus(404)
}
