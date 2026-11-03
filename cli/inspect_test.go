package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactDSN(t *testing.T) {
	cases := map[string]string{
		"root:secret@tcp(127.0.0.1:3306)/app?parseTime=true": "root:***@tcp(127.0.0.1:3306)/app?parseTime=true",
		"postgres://postgres:secret@127.0.0.1:5432/app":      "postgres://postgres:***@127.0.0.1:5432/app",
		"user@tcp(127.0.0.1:3306)/app":                       "user@tcp(127.0.0.1:3306)/app",
		"":                                                   "",
	}
	for dsn, want := range cases {
		if got := redactDSN(dsn); got != want {
			t.Fatalf("redactDSN(%q) = %q, want %q", dsn, got, want)
		}
	}
}

// inspect is what an operator pastes into a ticket, so the password must never appear.
func TestInspectReportsConfigWithoutSecrets(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "gable.yaml")
	content := `app:
  name: demo
  env: production
http:
  addr: ":9090"
db:
  driver: mysql
  dsn: "root:hunter2@tcp(10.0.0.1:3306)/demo?parseTime=true"
tenant:
  mode: single
  root_id: 3
  root_code: acme
namespace:
  root: shop
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	dslDir := t.TempDir()
	writeCLI(t, dslDir, "task.model.yaml", cliModel)

	cmd := newInspectCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", configPath, "--dir", dslDir})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"demo (production)", ":9090", "root:***@tcp(10.0.0.1:3306)/demo", "single, root 3/acme", "shop", "1 models"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "hunter2") {
		t.Fatalf("the password leaked:\n%s", text)
	}

	// JSON mode is the one a script reads.
	cmd = newInspectCommand()
	out.Reset()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", configPath, "--dir", dslDir, "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var report inspectReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if report.App.Name != "demo" || report.DSL == nil || !report.DSL.Valid || report.DSL.Models != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.DB.DSN == "" || strings.Contains(report.DB.DSN, "hunter2") {
		t.Fatalf("dsn = %q", report.DB.DSN)
	}
}

// A DSL directory with problems is reported as data, not as a failed command: inspect is
// how you find out what is wrong.
func TestInspectShowsDSLIssues(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "gable.yaml")
	if err := os.WriteFile(configPath, []byte("app: {name: demo}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dslDir := t.TempDir()
	writeCLI(t, dslDir, "bad.model.yaml", "apiVersion: gable/v1\nkind: Model\nmetadata: {name: bad}\nspec: {fields: []}\n")

	cmd := newInspectCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", configPath, "--dir", dslDir, "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var report inspectReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.DSL == nil || report.DSL.Valid || len(report.DSL.Issues) == 0 {
		t.Fatalf("report = %+v", report)
	}
}

// With no configuration file and no explicit --config, inspect answers from the defaults
// instead of failing: "what would this binary do out of the box" is a real question.
func TestInspectFallsBackToDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	cmd := newInspectCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var report inspectReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.App.Name != "app" || report.HTTP.Addr == "" {
		t.Fatalf("report = %+v", report)
	}
}
