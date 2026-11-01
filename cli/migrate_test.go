package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const modelDSL = `apiVersion: gable/v1
kind: Model
metadata:
  name: order
spec:
  table: orders
  fields:
    - {name: id, type: int64, primary: true, generator: snowflake}
    - {name: tenant_id, type: int64, tenant: true}
    - {name: code, type: string, length: 64}
`

func writeMigrateFixture(t *testing.T) (dir, cfgPath string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "order.yaml"), []byte(modelDSL), 0o600); err != nil {
		t.Fatalf("write dsl: %v", err)
	}

	cfgPath = filepath.Join(dir, "gable.yaml")
	if err := os.WriteFile(cfgPath, []byte("app:\n  name: migrate-test\ndb:\n  driver: mysql\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return dir, cfgPath
}

// --dry-run is the reviewable path: it prints the statements and never touches a
// database, so a schema change can be read before it runs.
func TestMigrateDryRunPrintsStatements(t *testing.T) {
	dir, cfgPath := writeMigrateFixture(t)

	cmd := newMigrateCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", cfgPath, "--dir", dir, "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out.String())
	}

	statement := out.String()
	if !strings.Contains(statement, "CREATE TABLE") || !strings.Contains(statement, "orders") {
		t.Fatalf("output = %s", statement)
	}
	// Field names and types come from the DSL, not from a template.
	for _, want := range []string{"`id`", "`tenant_id`", "`code`"} {
		if !strings.Contains(statement, want) {
			t.Errorf("statement missing %s:\n%s", want, statement)
		}
	}
}

// A DSL issue must stop the command: migrating from a half-valid model creates a
// table nobody designed.
func TestMigrateRefusesInvalidDSL(t *testing.T) {
	dir, cfgPath := writeMigrateFixture(t)
	// A model without a primary key fails lint.
	broken := strings.Replace(modelDSL, "primary: true, generator: snowflake", "generator: snowflake", 1)
	if err := os.WriteFile(filepath.Join(dir, "order.yaml"), []byte(broken), 0o600); err != nil {
		t.Fatalf("write dsl: %v", err)
	}

	cmd := newMigrateCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", cfgPath, "--dir", dir, "--dry-run"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected a failure, output:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "DSL issue") {
		t.Fatalf("err = %v", err)
	}
}

// Without a DSN the command must say so instead of connecting to something arbitrary.
func TestMigrateRequiresDSN(t *testing.T) {
	dir, cfgPath := writeMigrateFixture(t)

	cmd := newMigrateCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", cfgPath, "--dir", dir})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "DSN") {
		t.Fatalf("err = %v, want a DSN complaint; output:\n%s", err, out.String())
	}
}

func TestMigrateRejectsUnknownDriver(t *testing.T) {
	dir, cfgPath := writeMigrateFixture(t)
	if err := os.WriteFile(cfgPath, []byte("db:\n  driver: sqlite\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := newMigrateCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", cfgPath, "--dir", dir, "--dry-run"})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unsupported driver") {
		t.Fatalf("err = %v, want an unsupported-driver complaint", err)
	}
}
