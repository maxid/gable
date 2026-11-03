package dsl

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gopkg.in/yaml.v3"

	"github.com/maxid/gable/orm"
	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

// secondSet is the same fixture with one more model, so a reload is observable.
func secondSet(t *testing.T) *Set {
	t.Helper()
	dir := validSet(t)
	write(t, dir, "note.model.yaml", `apiVersion: gable/v1
kind: Model
metadata: {name: note, title: Note}
spec:
  table: dsl_note
  fields:
    - {name: id, type: int64, primary: true}
    - {name: tenant_id, type: int64, tenant: true}
    - {name: body, type: string, size: 64}
`)
	set, issues, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if issues := set.Lint(); len(issues) > 0 {
		t.Fatal(issues)
	}
	return set
}

// A reload swaps what the API serves without restarting it, and a removed model stops being
// reachable — that is what unloading means for a data API driven by resources.
func TestRuntimeReloadSwapsAndUnloads(t *testing.T) {
	runtime, mock := newRuntimeMock(t)
	server := web.New(web.WithTitle("reload"))
	server.UseTenant(tenant.NewSingleResolver(tenant.DefaultConfig()))
	runtime.Routes(server.Router(), "/dsl/data")

	mock.ExpectQuery("SELECT COUNT(*) FROM `dsl_note` WHERE `tenant_id` = ?").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectQuery("SELECT `id`, `tenant_id`, `body` FROM `dsl_note` WHERE `tenant_id` = ? ORDER BY `id` ASC LIMIT 20 OFFSET 0").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "body"}))

	// Before the reload the model does not exist.
	if recorder := perform(server, http.MethodGet, "/dsl/data/note", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("before reload = %d: %s", recorder.Code, recorder.Body)
	}

	if err := runtime.Reload(secondSet(t)); err != nil {
		t.Fatal(err)
	}
	if recorder := perform(server, http.MethodGet, "/dsl/data/note", nil); recorder.Code != http.StatusOK {
		t.Fatalf("after reload = %d: %s", recorder.Code, recorder.Body)
	}

	// Reloading a set without the model unloads it on the next request.
	original, issues, err := LoadDir(validSet(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if err := runtime.Reload(original); err != nil {
		t.Fatal(err)
	}
	if recorder := perform(server, http.MethodGet, "/dsl/data/note", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("after unload = %d: %s", recorder.Code, recorder.Body)
	}
}

// A set that does not lint is refused, and the previous snapshot keeps answering: a bad edit
// must not become an outage.
func TestRuntimeReloadRejectsABrokenSet(t *testing.T) {
	runtime, _ := newRuntimeMock(t)
	broken := NewSet()
	broken.Models["broken"] = &ModelDoc{
		APIVersion: APIVersion,
		Kind:       KindModel,
		Metadata:   Metadata{Name: "broken"},
		Spec:       ModelSpec{Fields: []FieldSpec{{Name: "id", Type: "int64"}}},
	}

	err := runtime.Reload(broken)
	if err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("err = %v", err)
	}
	// The fixtures are still served.
	if runtime.Set().Models["task"] == nil {
		t.Fatal("a rejected reload must keep the previous set")
	}
	if err := runtime.Reload(nil); err == nil {
		t.Fatal("a nil set must be refused")
	}
}

// Requests read one snapshot: a reload during a request cannot change the model it is
// querying, and many readers plus occasional swaps must be race-free.
func TestRuntimeReloadIsRaceFree(t *testing.T) {
	runtime, _ := newRuntimeMock(t)
	withNote := secondSet(t)
	withoutNote, _, err := LoadDir(validSet(t))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if runtime.Set() == nil {
						t.Error("snapshot is nil")
						return
					}
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		if err := runtime.Reload(withNote); err != nil {
			t.Fatal(err)
		}
		if err := runtime.Reload(withoutNote); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}

// Metadata flags are part of the contract: a bad status is a lint error, and readonly is
// carried through load.
func TestMetadataFlags(t *testing.T) {
	dir := validSet(t)
	write(t, dir, "task.api.yaml", `apiVersion: gable/v1
kind: Api
metadata: {name: task.close, readonly: true, status: deprecated, tags: [admin, task], sort: 5}
spec: {handler: tasks.close, path: task/close}
`)
	set, issues, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) > 0 {
		t.Fatalf("load issues = %v", issues)
	}
	metadata := set.APIs["task.close"].Metadata
	if !metadata.Readonly || !metadata.Deprecated() || len(metadata.Tags) != 2 || metadata.Sort != 5 {
		t.Fatalf("metadata = %+v", metadata)
	}

	write(t, dir, "bad.api.yaml", `apiVersion: gable/v1
kind: Api
metadata: {name: task.bad, status: retired}
spec: {handler: x, path: task/bad}
`)
	bad, _, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if issues := bad.Lint(); len(issues) == 0 || !strings.Contains(issues.Error(), "unsupported status") {
		t.Fatalf("issues = %v", issues)
	}
}

// A stored readonly resource cannot be overwritten by a push, but creating one with the flag
// is how the flag gets there in the first place.
func TestReadonlyStoredResourceIsProtected(t *testing.T) {
	documents, set := validDocuments(t)
	store, mock := newFixedStore(t, orm.MySQL())

	// Mark the stored document readonly the way a person would: by editing the document.
	var stored ModelDoc
	if err := yaml.Unmarshal([]byte(documents["Model/task"]), &stored); err != nil {
		t.Fatal(err)
	}
	stored.Metadata.Readonly = true
	raw, err := yaml.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	readonly := string(raw)
	rows := sqlmock.NewRows([]string{"kind", "name", "document"}).
		AddRow("Model", "task", readonly)
	mock.ExpectQuery("SELECT `kind`, `name`, `document` FROM `gable_dsl_resource` ORDER BY `kind`, `name`").
		WillReturnRows(rows)

	// The desired set differs (that is why it would write), so the conflict must be reported
	// instead of overwriting the stored document.
	written, err := store.Save(context.Background(), set)
	if err == nil || !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("err = %v, written = %v", err, written)
	}
	if len(written) != 0 {
		t.Fatalf("written = %v, want nothing", written)
	}

	mock.ExpectQuery("SELECT `document` FROM `gable_dsl_revision` WHERE `kind` = ? AND `name` = ? AND `revision` = ?").
		WithArgs("Model", "task", int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(documents["Model/task"]))
	mock.ExpectQuery("SELECT `kind`, `name`, `document` FROM `gable_dsl_resource` ORDER BY `kind`, `name`").
		WillReturnRows(sqlmock.NewRows([]string{"kind", "name", "document"}).AddRow("Model", "task", readonly))
	if _, err := store.Restore(context.Background(), KindModel, "task", 1); err == nil ||
		!strings.Contains(err.Error(), "readonly") {
		t.Fatalf("restore err = %v", err)
	}
}

// perform drives a handler through the full server stack.
func perform(server *web.Server, method, target string, body *bytes.Buffer) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		reader = body
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(method, target, reader))
	return recorder
}
