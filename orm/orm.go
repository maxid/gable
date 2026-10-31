// Package orm is a small SQL-first ORM built on database/sql and sqlx.
//
// It keeps SQL visible, supports MySQL and PostgreSQL, enforces tenant scope
// for models that declare a tenant field, and wires gcosid for primary keys.
package orm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"

	gableerrors "github.com/maxid/gable/errors"
)

// Framework errors.
var (
	ErrNotFound       = gableerrors.New("orm_not_found", "record not found").WithStatus(404)
	ErrTenantMismatch = gableerrors.New("orm_tenant_mismatch", "record tenant does not match the context").WithStatus(400)
	ErrIDRequired     = gableerrors.New("orm_id_required", "primary key is required").WithStatus(400)
	ErrInvalidModel   = gableerrors.New("orm_invalid_model", "invalid model").WithStatus(500)
)

// Dialect renders SQL for one database engine.
type Dialect interface {
	Name() string
	Placeholder(index int) string
	Quote(identifier string) string
	Pagination(limit, offset int) string
	Returning() bool
}

type mysqlDialect struct{}

func (mysqlDialect) Name() string                   { return "mysql" }
func (mysqlDialect) Placeholder(int) string         { return "?" }
func (mysqlDialect) Quote(identifier string) string { return "`" + identifier + "`" }
func (mysqlDialect) Pagination(limit, offset int) string {
	if limit <= 0 {
		return ""
	}
	return fmt.Sprintf("LIMIT %d OFFSET %d", limit, offset)
}
func (mysqlDialect) Returning() bool { return false }

type postgresDialect struct{}

func (postgresDialect) Name() string                   { return "postgres" }
func (postgresDialect) Placeholder(index int) string   { return fmt.Sprintf("$%d", index) }
func (postgresDialect) Quote(identifier string) string { return `"` + identifier + `"` }
func (postgresDialect) Pagination(limit, offset int) string {
	if limit <= 0 {
		return ""
	}
	return fmt.Sprintf("LIMIT %d OFFSET %d", limit, offset)
}
func (postgresDialect) Returning() bool { return true }

// MySQL returns the MySQL dialect.
func MySQL() Dialect { return mysqlDialect{} }

// Postgres returns the PostgreSQL dialect.
func Postgres() Dialect { return postgresDialect{} }

// DialectFor returns the dialect for a database/sql driver name.
func DialectFor(driver string) (Dialect, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "mysql":
		return MySQL(), nil
	case "postgres", "postgresql", "pgx", "pg":
		return Postgres(), nil
	default:
		return nil, fmt.Errorf("orm: unsupported driver %q", driver)
	}
}

// DB wraps sqlx with a dialect.
type DB struct {
	*sqlx.DB
	dialect Dialect
}

// Open connects to a database and selects its dialect.
func Open(driver, dsn string) (*DB, error) {
	dialect, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	db, err := sqlx.Connect(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("orm: connect %s: %w", driver, err)
	}
	return &DB{DB: db, dialect: dialect}, nil
}

// New wraps an existing sqlx.DB.
func New(db *sqlx.DB, dialect Dialect) (*DB, error) {
	if db == nil {
		return nil, fmt.Errorf("orm: nil sqlx.DB")
	}
	if dialect == nil {
		return nil, fmt.Errorf("orm: nil dialect")
	}
	return &DB{DB: db, dialect: dialect}, nil
}

// Dialect returns the configured dialect.
func (db *DB) Dialect() Dialect { return db.dialect }

// Configure sets the connection pool bounds.
func (db *DB) Configure(maxOpen, maxIdle int) {
	if maxOpen > 0 {
		db.SetMaxOpenConns(maxOpen)
	}
	if maxIdle > 0 {
		db.SetMaxIdleConns(maxIdle)
	}
}

// AutoMigrate creates missing tables. It never alters or drops an existing
// table; schema evolution belongs to explicit migrations.
func (db *DB) AutoMigrate(models ...*Model) error {
	for _, model := range models {
		statement, err := SchemaSQL(model, db.dialect)
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(context.Background(), statement); err != nil {
			return fmt.Errorf("orm: migrate %s: %w", model.Table, err)
		}
	}
	return nil
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
		return ErrNotFound
	}
	return nil
}
