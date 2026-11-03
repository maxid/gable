package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/maxid/gable/dsl"
)

const genSource = `package models

import "time"

type User struct {
	ID        int64      ` + "`gable:\"pk\"`" + `
	TenantID  int64      ` + "`gable:\"tenant\"`" + `
	Username  string     ` + "`gable:\"unique,size=64\"`" + `
	Nickname  string     ` + "`json:\"nick\" gable:\"size=32,column=nickname\"`" + `
	Active    bool
	CreatedAt time.Time  ` + "`gable:\"auto_create\"`" + `
	DeletedAt *time.Time ` + "`gable:\"soft_delete\"`" + `
	Roles     []string   ` + "`gable:\"size=64\"`" + `
	Orders    []Order    ` + "`gable:\"has_many=orders,foreign_key=user_id\"`" + `
	Hidden    string     ` + "`gable:\"-\"`" + `
}

type Order struct {
	ID     int64 ` + "`gable:\"pk,table=shop_order\"`" + `
	UserID int64 ` + "`gable:\"column=user_id\"`" + `
	Total  float64
}

type payload struct {
	Whatever string
}
`

func writeGenPackage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models.go"), []byte(genSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The generator emits what the ORM would map: exported fields become columns, `-` and
// relations do not, tags become flags, and the result is a valid Model resource.
func TestGenDSLFromStruct(t *testing.T) {
	dir := writeGenPackage(t)
	models, err := generateModels(dir, "app_")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d, want the two tagged structs", len(models))
	}
	user := models[0]
	if user.Doc.Metadata.Name != "user" || user.Doc.Spec.Table != "app_user" {
		t.Fatalf("user = %+v", user.Doc)
	}
	byName := map[string]dsl.FieldSpec{}
	for _, field := range user.Doc.Spec.Fields {
		byName[field.Name] = field
	}
	for _, want := range []string{"id", "tenant_id", "username", "nickname", "active", "created_at"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("field %q missing from %v", want, byName)
		}
	}
	if _, ok := byName["hidden"]; ok {
		t.Fatal("gable:\"-\" must not be generated")
	}
	if _, ok := byName["orders"]; ok {
		t.Fatal("a relation must not become a column")
	}
	if !byName["id"].Primary || !byName["tenant_id"].Tenant || !byName["username"].Unique {
		t.Fatalf("flags lost: %+v", byName)
	}
	if byName["username"].Size != 64 || byName["nickname"].Size != 32 {
		t.Fatalf("sizes = %+v", byName["username"])
	}
	if !byName["created_at"].AutoCreate || byName["created_at"].Type != "time" {
		t.Fatalf("created_at = %+v", byName["created_at"])
	}
	if byName["active"].Type != "bool" {
		t.Fatalf("untagged exported field = %+v", byName["active"])
	}

	// Warnings name what could not be represented instead of dropping it silently.
	joined := strings.Join(user.Warnings, "\n")
	if !strings.Contains(joined, "soft_delete") || !strings.Contains(joined, "Roles") {
		t.Fatalf("warnings = %v", user.Warnings)
	}

	order := models[1]
	if order.Doc.Spec.Table != "app_shop_order" {
		t.Fatalf("table tag ignored: %q", order.Doc.Spec.Table)
	}
	// Every generated resource must be a document the loader accepts.
	for _, model := range models {
		raw, err := yaml.Marshal(model.Doc)
		if err != nil {
			t.Fatal(err)
		}
		set, issues := dsl.LoadBytes("generated", raw)
		if len(issues) != 0 {
			t.Fatalf("%s: %v", model.Doc.Metadata.Name, issues)
		}
		if lint := set.Lint(); len(lint) != 0 {
			t.Fatalf("%s: %v", model.Doc.Metadata.Name, lint)
		}
	}
}

// Writing twice leaves the files alone, so `gen` in CI is a no-op when nothing changed.
func TestGenDSLWriteIsIdempotent(t *testing.T) {
	dir := writeGenPackage(t)
	out := t.TempDir()

	cmd := newGenCommand()
	var buffer bytes.Buffer
	cmd.SetOut(&buffer)
	cmd.SetErr(&buffer)
	cmd.SetArgs([]string{"dsl", "--pkg", dir, "--out", out})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buffer.String(), "wrote") {
		t.Fatalf("first run wrote nothing: %q", buffer.String())
	}
	files, err := os.ReadDir(out)
	if err != nil || len(files) != 2 {
		t.Fatalf("files = %v, %v", files, err)
	}

	buffer.Reset()
	cmd = newGenCommand()
	cmd.SetOut(&buffer)
	cmd.SetErr(&buffer)
	cmd.SetArgs([]string{"dsl", "--pkg", dir, "--out", out})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buffer.String(), "wrote") {
		t.Fatalf("second run rewrote files: %q", buffer.String())
	}
}

func TestGenDSLRejectsAPackageWithoutTags(t *testing.T) {
	dir := t.TempDir()
	writeCLI(t, dir, "plain.go", "package plain\n\ntype Plain struct{ A string }\n")
	if _, err := generateModels(dir, ""); err == nil || !strings.Contains(err.Error(), "no structs with gable tags") {
		t.Fatalf("err = %v", err)
	}
	if _, err := generateModels(filepath.Join(dir, "missing"), ""); err == nil {
		t.Fatal("a missing directory must be an error")
	}
}
