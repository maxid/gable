package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Dialect selects the SQL syntax of the history store.
type Dialect string

const (
	// DialectMySQL is MySQL/MariaDB/TiDB-compatible syntax.
	DialectMySQL Dialect = "mysql"
	// DialectPostgres is PostgreSQL/Kingbase/GaussDB-compatible syntax.
	DialectPostgres Dialect = "postgres"
)

// DefaultHistoryTable is Flyway's default table name. Keeping it the default
// is what makes an existing Flyway history readable without an import step.
const DefaultHistoryTable = "flyway_schema_history"

// Store persists Flyway history and executes migration SQL.
type Store interface {
	// Ensure creates the history table if needed.
	Ensure(ctx context.Context) error
	// List returns every history row ordered by installed_rank.
	List(ctx context.Context) ([]History, error)
	// Apply executes the migration SQL and appends its history row.
	Apply(ctx context.Context, m Migration) error
	// Record appends a history row without executing SQL (baseline/repair).
	Record(ctx context.Context, m Migration) error
	// DeleteFailed removes rows whose success flag is false.
	DeleteFailed(ctx context.Context) error
	// Lock acquires the migration lock and returns its release function.
	Lock(ctx context.Context, name string) (func(), error)
	// Close closes the database handle.
	Close() error
}

// Option configures a SQL store.
type Option func(*sqlStore)

// WithTable overrides the history table name.
func WithTable(name string) Option {
	return func(s *sqlStore) { s.table = name }
}

// WithInstalledBy sets flyway_schema_history.installed_by.
func WithInstalledBy(name string) Option {
	return func(s *sqlStore) { s.installedBy = name }
}

// OpenMySQL opens a MySQL-protocol history store. The DSN must enable
// parseTime=true when the driver returns TIMESTAMP columns as time.Time.
func OpenMySQL(dsn string, opts ...Option) (Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("%w: empty MySQL DSN", ErrInvalidConfig)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	return newStore(db, DialectMySQL, opts...)
}

// OpenPostgres opens a PostgreSQL-protocol history store.
func OpenPostgres(dsn string, opts ...Option) (Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("%w: empty PostgreSQL DSN", ErrInvalidConfig)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	return newStore(db, DialectPostgres, opts...)
}

// NewStore wraps an existing database handle. It is useful for tests and for
// applications that already own a pool.
func NewStore(db *sql.DB, dialect Dialect, opts ...Option) (Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil database", ErrInvalidConfig)
	}
	return newStore(db, dialect, opts...)
}

func newStore(db *sql.DB, dialect Dialect, opts ...Option) (*sqlStore, error) {
	if dialect != DialectMySQL && dialect != DialectPostgres {
		_ = db.Close()
		return nil, fmt.Errorf("%w: unsupported dialect %q", ErrInvalidConfig, dialect)
	}
	s := &sqlStore{db: db, dialect: dialect, table: DefaultHistoryTable, installedBy: "gable"}
	for _, opt := range opts {
		opt(s)
	}
	if !identifierRE.MatchString(s.table) {
		_ = db.Close()
		return nil, fmt.Errorf("%w: invalid history table %q", ErrInvalidConfig, s.table)
	}
	if strings.TrimSpace(s.installedBy) == "" {
		s.installedBy = "gable"
	}
	return s, nil
}

var identifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type sqlStore struct {
	db          *sql.DB
	dialect     Dialect
	table       string
	installedBy string
}

func (s *sqlStore) Close() error { return s.db.Close() }

func (s *sqlStore) quote(value string) string {
	if s.dialect == DialectMySQL {
		return "`" + value + "`"
	}
	return `"` + value + `"`
}

func (s *sqlStore) placeholder(n int) string {
	if s.dialect == DialectMySQL {
		return "?"
	}
	return fmt.Sprintf("$%d", n)
}

func (s *sqlStore) Ensure(ctx context.Context) error {
	for _, statement := range s.createStatements() {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate: ensure history: %w", err)
		}
	}
	return nil
}

func (s *sqlStore) createStatements() []string {
	table := s.quote(s.table)
	if s.dialect == DialectMySQL {
		return []string{`CREATE TABLE IF NOT EXISTS ` + table + ` (
  installed_rank INT NOT NULL,
  version VARCHAR(50) NULL,
  description VARCHAR(200) NOT NULL,
  type VARCHAR(20) NOT NULL,
  script VARCHAR(1000) NOT NULL,
  checksum INT NULL,
  installed_by VARCHAR(100) NOT NULL,
  installed_on TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  execution_time INT NOT NULL,
  success TINYINT(1) NOT NULL,
  PRIMARY KEY (installed_rank),
  KEY ` + s.quote(s.table+"_s_idx") + ` (success)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`}
	}
	return []string{
		`CREATE TABLE IF NOT EXISTS ` + table + ` (
  installed_rank INT NOT NULL,
  version VARCHAR(50),
  description VARCHAR(200) NOT NULL,
  type VARCHAR(20) NOT NULL,
  script VARCHAR(1000) NOT NULL,
  checksum INT,
  installed_by VARCHAR(100) NOT NULL,
  installed_on TIMESTAMP NOT NULL DEFAULT now(),
  execution_time INT NOT NULL,
  success BOOLEAN NOT NULL,
  CONSTRAINT ` + s.quote(s.table+"_pk") + ` PRIMARY KEY (installed_rank)
)`,
		`CREATE INDEX IF NOT EXISTS ` + s.quote(s.table+"_s_idx") + ` ON ` + table + ` (success)`,
	}
}

func (s *sqlStore) List(ctx context.Context) ([]History, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT installed_rank, version, description, type, script, checksum, installed_by, installed_on, execution_time, success FROM `+s.quote(s.table)+` ORDER BY installed_rank`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []History
	for rows.Next() {
		var h History
		var version sql.NullString
		var checksum sql.NullInt64
		var installedOn any
		if err := rows.Scan(&h.InstalledRank, &version, &h.Description, &h.Type, &h.Script, &checksum, &h.InstalledBy, &installedOn, &h.ExecutionTime, &h.Success); err != nil {
			return nil, err
		}
		h.Version = version.String
		if checksum.Valid {
			value := int32(checksum.Int64)
			h.Checksum = &value
		}
		h.InstalledOn = timeValue(installedOn)
		out = append(out, h)
	}
	return out, rows.Err()
}

func timeValue(value any) string {
	switch v := value.(type) {
	case time.Time:
		return v.Format(time.RFC3339Nano)
	case []byte:
		return string(v)
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func (s *sqlStore) nextRank(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int, error) {
	var rank int
	err := queryer.QueryRowContext(ctx, `SELECT COALESCE(MAX(installed_rank), 0) + 1 FROM `+s.quote(s.table)).Scan(&rank)
	return rank, err
}

func (s *sqlStore) Apply(ctx context.Context, m Migration) error {
	started := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, string(m.SQL)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("%s %s: %w", m.Script, m.Version, err)
	}
	rank, err := s.nextRank(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	execution := int(time.Since(started).Milliseconds())
	query := `INSERT INTO ` + s.quote(s.table) + ` (installed_rank, version, description, type, script, checksum, installed_by, execution_time, success) VALUES (` +
		s.placeholder(1) + `, ` + s.placeholder(2) + `, ` + s.placeholder(3) + `, ` + s.placeholder(4) + `, ` + s.placeholder(5) + `, ` + s.placeholder(6) + `, ` + s.placeholder(7) + `, ` + s.placeholder(8) + `, TRUE)`
	if _, err := tx.ExecContext(ctx, query, rank, m.Version, m.Description, m.migrationType(), m.Script, int32(m.Checksum), s.installedBy, execution); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *sqlStore) Record(ctx context.Context, m Migration) error {
	rank, err := s.nextRank(ctx, s.db)
	if err != nil {
		return err
	}
	query := `INSERT INTO ` + s.quote(s.table) + ` (installed_rank, version, description, type, script, checksum, installed_by, execution_time, success) VALUES (` +
		s.placeholder(1) + `, ` + s.placeholder(2) + `, ` + s.placeholder(3) + `, ` + s.placeholder(4) + `, ` + s.placeholder(5) + `, NULL, ` + s.placeholder(6) + `, 0, TRUE)`
	_, err = s.db.ExecContext(ctx, query, rank, m.Version, m.Description, m.migrationType(), m.Script, s.installedBy)
	return err
}

func (s *sqlStore) DeleteFailed(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM `+s.quote(s.table)+` WHERE success = FALSE`)
	return err
}

func (s *sqlStore) Lock(ctx context.Context, name string) (func(), error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if s.dialect == DialectMySQL {
		var got sql.NullInt64
		if err := conn.QueryRowContext(ctx, `SELECT GET_LOCK(?, 30)`, name).Scan(&got); err != nil {
			_ = conn.Close()
			return nil, err
		}
		if !got.Valid || got.Int64 != 1 {
			_ = conn.Close()
			return nil, fmt.Errorf("migrate: could not acquire lock %q", name)
		}
		return func() {
			_, _ = conn.ExecContext(context.Background(), `SELECT RELEASE_LOCK(?)`, name)
			_ = conn.Close()
		}, nil
	}

	key := advisoryKey(name)
	var got bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&got); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !got {
		_ = conn.Close()
		return nil, fmt.Errorf("migrate: could not acquire advisory lock %q", name)
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
		_ = conn.Close()
	}, nil
}

func advisoryKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64())
}
