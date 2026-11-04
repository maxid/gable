// Package dialect provides the canonical SQL dialect layer for gable and its
// applications:
// placeholders, identifier quoting, pagination and upsert SQL for the database
// engines the platform supports.
//
// It deliberately does not hide SQL. Repositories stay hand-written; this
// package only centralises the few constructs that differ between engines.
package dialect

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupported is returned for constructs an engine cannot express.
var ErrUnsupported = errors.New("gable/orm/dialect: unsupported by dialect")

// Dialect describes one SQL dialect.
type Dialect interface {
	Name() string
	// Placeholder returns the bind placeholder for the n-th argument (1-based).
	Placeholder(n int) string
	// Quote quotes a (possibly dotted) identifier and rejects anything unsafe.
	Quote(ident string) (string, error)
	// Pagination returns a LIMIT/OFFSET clause. argBase is the number of
	// arguments already bound; placeholders continue from argBase+1.
	Pagination(limit, offset, argBase int) string
	// PaginationArgs returns the arguments in the order the clause expects them.
	PaginationArgs(limit, offset int) []any
	// Upsert builds an INSERT ... statement; placeholders use the dialect's
	// style starting at 1.
	Upsert(table string, keyCols, columns []string) (string, error)
}

// MySQL returns the MySQL/TiDB/OceanBase dialect.
func MySQL() Dialect { return mysqlDialect{} }

// Postgres returns the PostgreSQL/Kingbase/GaussDB dialect.
func Postgres() Dialect { return postgresDialect{} }

// ClickHouse returns the ClickHouse dialect.
func ClickHouse() Dialect { return clickHouseDialect{} }

// DM returns the 达梦 dialect (limited: pagination/upsert are planned).
func DM() Dialect { return dmDialect{} }

// DialectFor resolves a dialect name or common alias.
func DialectFor(name string) (Dialect, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "mysql", "mariadb", "tidb", "oceanbase":
		return MySQL(), nil
	case "postgres", "postgresql", "pg", "kingbase", "gaussdb", "opengauss", "highgo":
		return Postgres(), nil
	case "clickhouse", "ch":
		return ClickHouse(), nil
	case "dm", "dameng":
		return DM(), nil
	default:
		return nil, fmt.Errorf("gable/orm/dialect: unknown dialect %q", name)
	}
}

// --- MySQL ---------------------------------------------------------------

type mysqlDialect struct{}

func (mysqlDialect) Name() string { return "mysql" }
func (mysqlDialect) Placeholder(int) string {
	return "?"
}
func (mysqlDialect) Quote(ident string) (string, error) { return quoteWith(ident, '`') }
func (mysqlDialect) Pagination(_, _, _ int) string      { return "LIMIT ? OFFSET ?" }
func (mysqlDialect) PaginationArgs(limit, offset int) []any {
	return []any{clamp(limit), clamp(offset)}
}
func (d mysqlDialect) Upsert(table string, keyCols, columns []string) (string, error) {
	cols, err := quoteAll(d, columns)
	if err != nil {
		return "", err
	}
	keys, err := quoteAll(d, keyCols)
	if err != nil {
		return "", err
	}
	if err := checkKeySubset(keyCols, columns); err != nil {
		return "", err
	}
	t, err := d.Quote(table)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "INSERT INTO %s (%s) VALUES (%s)", t, strings.Join(cols, ", "), placeholders(len(columns), d))
	updates := nonKeyCols(keyCols, columns)
	if len(updates) == 0 {
		fmt.Fprintf(&b, " ON DUPLICATE KEY UPDATE %s = %s", keys[0], keys[0])
		return b.String(), nil
	}
	parts := make([]string, 0, len(updates))
	for _, c := range updates {
		q, err := d.Quote(c)
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("%s = VALUES(%s)", q, q))
	}
	fmt.Fprintf(&b, " ON DUPLICATE KEY UPDATE %s", strings.Join(parts, ", "))
	return b.String(), nil
}

// --- PostgreSQL ----------------------------------------------------------

type postgresDialect struct{}

func (postgresDialect) Name() string { return "postgres" }
func (postgresDialect) Placeholder(n int) string {
	return fmt.Sprintf("$%d", n)
}
func (postgresDialect) Quote(ident string) (string, error) { return quoteWith(ident, '"') }
func (d postgresDialect) Pagination(limit, offset, argBase int) string {
	return fmt.Sprintf("LIMIT %s OFFSET %s", d.Placeholder(argBase+1), d.Placeholder(argBase+2))
}
func (postgresDialect) PaginationArgs(limit, offset int) []any {
	return []any{clamp(limit), clamp(offset)}
}
func (d postgresDialect) Upsert(table string, keyCols, columns []string) (string, error) {
	cols, err := quoteAll(d, columns)
	if err != nil {
		return "", err
	}
	keys, err := quoteAll(d, keyCols)
	if err != nil {
		return "", err
	}
	if err := checkKeySubset(keyCols, columns); err != nil {
		return "", err
	}
	t, err := d.Quote(table)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s)", t, strings.Join(cols, ", "), placeholders(len(columns), d), strings.Join(keys, ", "))
	updates := nonKeyCols(keyCols, columns)
	if len(updates) == 0 {
		b.WriteString(" DO NOTHING")
		return b.String(), nil
	}
	parts := make([]string, 0, len(updates))
	for _, c := range updates {
		q, err := d.Quote(c)
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("%s = EXCLUDED.%s", q, q))
	}
	fmt.Fprintf(&b, " DO UPDATE SET %s", strings.Join(parts, ", "))
	return b.String(), nil
}

// --- ClickHouse ----------------------------------------------------------

type clickHouseDialect struct{}

func (clickHouseDialect) Name() string { return "clickhouse" }
func (clickHouseDialect) Placeholder(int) string {
	return "?"
}
func (clickHouseDialect) Quote(ident string) (string, error) { return quoteWith(ident, '`') }
func (clickHouseDialect) Pagination(_, _, _ int) string      { return "LIMIT ? OFFSET ?" }
func (clickHouseDialect) PaginationArgs(limit, offset int) []any {
	return []any{clamp(limit), clamp(offset)}
}
func (clickHouseDialect) Upsert(string, []string, []string) (string, error) {
	return "", fmt.Errorf("%w: ClickHouse has no upsert (use ReplacingMergeTree)", ErrUnsupported)
}

// --- 达梦 ----------------------------------------------------------------

type dmDialect struct{}

func (dmDialect) Name() string { return "dm" }
func (dmDialect) Placeholder(int) string {
	return "?"
}
func (dmDialect) Quote(ident string) (string, error) { return quoteWith(ident, '"') }
func (dmDialect) Pagination(_, _, _ int) string      { return "OFFSET ? ROWS FETCH NEXT ? ROWS ONLY" }
func (dmDialect) PaginationArgs(limit, offset int) []any {
	// 达梦/Oracle 风格：先 OFFSET 再 FETCH。
	return []any{clamp(offset), clamp(limit)}
}
func (dmDialect) Upsert(string, []string, []string) (string, error) {
	return "", fmt.Errorf("%w: dm upsert (MERGE) is not implemented yet", ErrUnsupported)
}

// --- helpers -------------------------------------------------------------

func quoteWith(ident string, quote byte) (string, error) {
	if ident == "" {
		return "", errors.New("gable/orm/dialect: empty identifier")
	}
	parts := strings.Split(ident, ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if !validIdent(p) {
			return "", fmt.Errorf("gable/orm/dialect: unsafe identifier %q", ident)
		}
		out = append(out, string(quote)+p+string(quote))
	}
	return strings.Join(out, "."), nil
}

func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func quoteAll(d Dialect, cols []string) ([]string, error) {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		q, err := d.Quote(c)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, nil
}

func placeholders(n int, d Dialect) string {
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, d.Placeholder(i))
	}
	return strings.Join(out, ", ")
}

func checkKeySubset(keys, columns []string) error {
	set := make(map[string]bool, len(columns))
	for _, c := range columns {
		set[c] = true
	}
	for _, k := range keys {
		if !set[k] {
			return fmt.Errorf("gable/orm/dialect: key column %q not present in columns", k)
		}
	}
	return nil
}

func nonKeyCols(keys, columns []string) []string {
	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}
	var out []string
	for _, c := range columns {
		if !keySet[c] {
			out = append(out, c)
		}
	}
	return out
}

func clamp(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
