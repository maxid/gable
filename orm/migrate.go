package orm

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

var timeType = reflect.TypeOf(time.Time{})

// SchemaSQL renders a CREATE TABLE IF NOT EXISTS statement for the model.
// It creates missing tables only; changes to an existing table are left to
// explicit migrations.
func SchemaSQL(model *Model, dialect Dialect) (string, error) {
	if model == nil {
		return "", fmt.Errorf("orm: nil model")
	}
	if dialect == nil {
		return "", fmt.Errorf("orm: nil dialect")
	}

	columns := make([]string, 0, len(model.Fields))
	for _, field := range model.Fields {
		parts := []string{dialect.Quote(field.Column), columnType(field, dialect)}
		if field.PK {
			parts = append(parts, "PRIMARY KEY")
		}
		if field.Tenant {
			parts = append(parts, "NOT NULL")
		}
		if field.Unique && !field.PK {
			parts = append(parts, "UNIQUE")
		}
		columns = append(columns, strings.Join(parts, " "))
	}

	return "CREATE TABLE IF NOT EXISTS " + dialect.Quote(model.Table) +
		" (" + strings.Join(columns, ", ") + ")", nil
}

func columnType(field Field, dialect Dialect) string {
	typ := field.Type
	for typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}

	if field.Text {
		return "TEXT"
	}

	switch typ.Kind() {
	case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint64:
		if field.PK && field.Auto {
			if dialect.Name() == "postgres" {
				return "BIGSERIAL"
			}
			return "BIGINT AUTO_INCREMENT"
		}
		return "BIGINT"
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return "INTEGER"
	case reflect.Float32:
		return "REAL"
	case reflect.Float64:
		if dialect.Name() == "postgres" {
			return "DOUBLE PRECISION"
		}
		return "DOUBLE"
	case reflect.Bool:
		if dialect.Name() == "postgres" {
			return "BOOLEAN"
		}
		return "TINYINT(1)"
	case reflect.String:
		size := field.Size
		if size <= 0 {
			size = 255
		}
		return fmt.Sprintf("VARCHAR(%d)", size)
	case reflect.Slice:
		if typ.Elem().Kind() == reflect.Uint8 {
			if dialect.Name() == "postgres" {
				return "BYTEA"
			}
			return "BLOB"
		}
		return "TEXT"
	case reflect.Struct:
		if typ == timeType {
			if dialect.Name() == "postgres" {
				return "TIMESTAMP"
			}
			return "DATETIME"
		}
		return "TEXT"
	default:
		return "TEXT"
	}
}
