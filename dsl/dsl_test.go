package dsl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxid/gable/orm"
)

const modelYAML = `apiVersion: gable/v1
kind: Model
metadata:
  name: task
  title: Task
spec:
  table: dsl_task
  fields:
    - {name: id, type: int64, primary: true, generator: snowflake}
    - {name: tenant_id, type: int64, tenant: true}
    - {name: title, type: string, size: 128, required: true, label: Title}
    - {name: status, type: string, size: 16, enum: [todo, doing, done], default: todo}
    - {name: created_at, type: time, autoCreate: true}
    - {name: updated_at, type: time, autoUpdate: true}
`

const tableYAML = `apiVersion: gable/v1
kind: Table
metadata:
  name: task.table
  title: Tasks
spec:
  model: task
  columns:
    - {field: title, sortable: true}
    - {field: status}
`

const formYAML = `apiVersion: gable/v1
kind: Form
metadata:
  name: task.form
  title: Task form
spec:
  model: task
  fields:
    - {field: title, controlType: INPUT, required: true}
    - {field: status, controlType: SELECT}
`

const pageYAML = `apiVersion: gable/v1
kind: Page
metadata:
  name: task.page
  title: Task board
spec:
  table: task.table
  form: task.form
`

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func validSet(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "task.model.yaml", modelYAML)
	write(t, dir, "task.table.yaml", tableYAML)
	write(t, dir, "task.form.yaml", formYAML)
	write(t, dir, "task.page.yaml", pageYAML)
	return dir
}

func TestLoadLintAndPreview(t *testing.T) {
	dir := validSet(t)
	set, issues, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("load issues = %v", issues)
	}
	if issues := set.Lint(); len(issues) != 0 {
		t.Fatalf("lint issues = %v", issues)
	}
	if len(set.Models) != 1 || len(set.Tables) != 1 || len(set.Forms) != 1 || len(set.Pages) != 1 {
		t.Fatalf("set = %+v", set)
	}

	ddl, err := set.ModelDDL("task", orm.MySQL())
	if err != nil {
		t.Fatal(err)
	}
	wantDDL := "CREATE TABLE IF NOT EXISTS `dsl_task` (`id` BIGINT PRIMARY KEY, `tenant_id` BIGINT NOT NULL, `title` VARCHAR(128), `status` VARCHAR(16), `created_at` DATETIME, `updated_at` DATETIME)"
	if ddl != wantDDL {
		t.Fatalf("ddl:\n%s\nwant:\n%s", ddl, wantDDL)
	}

	table, err := set.IR(KindTable, "task.table")
	if err != nil {
		t.Fatal(err)
	}
	if table["kind"] != "TableDef" {
		t.Fatalf("table ir = %+v", table)
	}
	spec := table["spec"].(map[string]any)
	if spec["runtimeApi"] != "/dsl/data/task" {
		t.Fatalf("runtime api = %v", spec["runtimeApi"])
	}
	columns := spec["columns"].([]map[string]any)
	if len(columns) != 2 || columns[0]["name"] != "title" || columns[0]["sortable"] != true {
		t.Fatalf("columns = %+v", columns)
	}

	form, err := set.IR(KindForm, "task.form")
	if err != nil {
		t.Fatal(err)
	}
	formSpec := form["spec"].(map[string]any)
	fields := formSpec["fields"].([]map[string]any)
	if form["kind"] != "FormDef" || len(fields) != 2 || fields[1]["controlType"] != "SELECT" {
		t.Fatalf("form = %+v", form)
	}

	page, err := set.IR(KindPage, "task.page")
	if err != nil {
		t.Fatal(err)
	}
	pageSpec := page["spec"].(map[string]any)
	if page["kind"] != "UiPage" || pageSpec["type"] != "page" || len(pageSpec["body"].([]any)) != 2 {
		t.Fatalf("page = %+v", page)
	}

	preview, err := set.Preview(KindModel, "task", orm.Postgres())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.(map[string]any)["ddl"].(string), `"dsl_task"`) {
		t.Fatalf("preview = %+v", preview)
	}
}

func TestLintRejectsBrokenResources(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"missing-pk", `apiVersion: gable/v1
kind: Model
metadata: {name: broken}
spec:
  table: broken
  fields: [{name: id, type: int64}]
`, "primary"},
		{"bad-type", `apiVersion: gable/v1
kind: Model
metadata: {name: broken}
spec:
  table: broken
  fields: [{name: id, type: int64, primary: true}, {name: x, type: decimal}]
`, "unsupported type"},
		{"table-unknown-model", `apiVersion: gable/v1
kind: Table
metadata: {name: broken.table}
spec: {model: nope}
`, "unknown model"},
		{"form-unknown-field", `apiVersion: gable/v1
kind: Form
metadata: {name: broken.form}
spec: {model: task, fields: [{field: nope}]}
`, "unknown model field"},
		{"page-empty", `apiVersion: gable/v1
kind: Page
metadata: {name: broken.page}
spec: {}
`, "needs a table or a form"},
		{"wrong-version", `apiVersion: nope/v1
kind: Model
metadata: {name: broken}
spec: {table: broken, fields: [{name: id, type: int64, primary: true}]}
`, "apiVersion"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := validSet(t)
			write(t, dir, tc.name+".yaml", tc.content)
			set, issues, err := LoadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			issues = append(issues, set.Lint()...)
			if len(issues) == 0 || !strings.Contains(issues.Error(), tc.want) {
				t.Fatalf("issues = %v, want %q", issues, tc.want)
			}
		})
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	set, issues, err := LoadDir(validSet(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	out := t.TempDir()
	written, err := set.ApplyDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 4 {
		t.Fatalf("written = %v", written)
	}
	again, err := set.ApplyDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second apply wrote %v", again)
	}
}

func TestDuplicateNames(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.yaml", modelYAML)
	write(t, dir, "b.yaml", modelYAML)
	_, issues, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) == 0 || !strings.Contains(issues.Error(), "duplicate model") {
		t.Fatalf("issues = %v", issues)
	}
}
