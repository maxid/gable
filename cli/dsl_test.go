package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cliModel = `apiVersion: gable/v1
kind: Model
metadata: {name: task, title: Task}
spec:
  table: cli_task
  fields:
    - {name: id, type: int64, primary: true, generator: snowflake}
    - {name: title, type: string, size: 128, required: true}
`

const cliTable = `apiVersion: gable/v1
kind: Table
metadata: {name: task.table, title: Tasks}
spec:
  model: task
  columns:
    - {field: title, sortable: true}
`

func writeCLI(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runDSL(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newDSLCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if err != nil {
		return errOut.String(), err
	}
	return out.String(), nil
}

func TestDSLCLI(t *testing.T) {
	dir := t.TempDir()
	writeCLI(t, dir, "task.model.yaml", cliModel)
	writeCLI(t, dir, "task.table.yaml", cliTable)

	out, err := runDSL(t, "lint", "--dir", dir)
	if err != nil {
		t.Fatal(err, out)
	}
	if !strings.Contains(out, "OK: 1 models, 1 tables") {
		t.Fatalf("lint out = %q", out)
	}

	out, err = runDSL(t, "preview", "Model/task", "--dir", dir, "--format", "json")
	if err != nil {
		t.Fatal(err, out)
	}
	if !strings.Contains(out, `"ddl"`) || !strings.Contains(out, "cli_task") {
		t.Fatalf("model preview = %s", out)
	}

	out, err = runDSL(t, "preview", "Table/task.table", "--dir", dir)
	if err != nil {
		t.Fatal(err, out)
	}
	if !strings.Contains(out, "lcdp.ir/v1") || !strings.Contains(out, "TableDef") {
		t.Fatalf("table preview = %s", out)
	}

	outDir := t.TempDir()
	out, err = runDSL(t, "apply", "--dir", dir, "--out", outDir)
	if err != nil {
		t.Fatal(err, out)
	}
	if !strings.Contains(out, "wrote") {
		t.Fatalf("apply out = %q", out)
	}
	if _, err := os.Stat(filepath.Join(outDir, "Model-task.yaml")); err != nil {
		t.Fatal(err)
	}

	out, err = runDSL(t, "apply", "--dir", dir, "--out", outDir)
	if err != nil {
		t.Fatal(err, out)
	}
	if !strings.Contains(out, "no changes") {
		t.Fatalf("second apply = %q", out)
	}
}

func TestDSLCLILintFailure(t *testing.T) {
	dir := t.TempDir()
	writeCLI(t, dir, "task.model.yaml", "apiVersion: gable/v1\nkind: Model\nmetadata: {name: task}\nspec: {fields: []}\n")
	if _, err := runDSL(t, "lint", "--dir", dir); err == nil {
		t.Fatal("expected lint error")
	}
}
