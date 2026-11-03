package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An extension lands at the top level of the operation, where every OpenAPI tool looks for
// it — not nested under some "extensions" key.
func TestVendorExtensionsRenderOnTheOperation(t *testing.T) {
	server := New(WithTitle("ext"))
	server.Router().GET("/items", func(ctx *Context) error {
		return ctx.JSON(http.StatusOK, map[string]string{"ok": "yes"})
	}).Doc("list items").
		Extension("x-springdoc-paginated", true).
		Extension("x-gateway-cache", map[string]any{"ttl": 30})

	document := requestDocument(t, server, "/v3/api-docs")
	operation := operationAt(t, document, "/items", "get")
	if operation["x-springdoc-paginated"] != true {
		t.Fatalf("operation = %v", operation)
	}
	cache, ok := operation["x-gateway-cache"].(map[string]any)
	if !ok || cache["ttl"] != float64(30) {
		t.Fatalf("operation = %v", operation)
	}
	if _, nested := operation["extensions"]; nested {
		t.Fatalf("extensions must be flattened, got %v", operation)
	}
	// The rest of the operation is untouched.
	if operation["summary"] != "list items" {
		t.Fatalf("operation = %v", operation)
	}
}

// A name that is not a vendor extension is a wiring mistake and is refused immediately.
func TestExtensionRejectsANonExtensionName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a name without x- must panic at wiring time")
		}
	}()
	New(WithTitle("ext")).Router().GET("/x", func(*Context) error { return nil }).Extension("cache", 1)
}

// An operation without extensions renders exactly as before: the custom marshaller must not
// add an "extensions" field to every route in the document.
func TestOperationsWithoutExtensionsAreUnchanged(t *testing.T) {
	server := New(WithTitle("ext"))
	server.Router().GET("/plain", func(ctx *Context) error { return ctx.JSON(200, map[string]bool{"ok": true}) }).Doc("plain")

	document := requestDocument(t, server, "/v3/api-docs")
	operation := operationAt(t, document, "/plain", "get")
	if _, ok := operation["extensions"]; ok {
		t.Fatalf("operation = %v", operation)
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"extensions"`) {
		t.Fatalf("document carries an extensions key: %s", raw)
	}
}

func requestDocument(t *testing.T, server *Server, path string) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("docs status = %d: %s", recorder.Code, recorder.Body)
	}
	var document map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func operationAt(t *testing.T, document map[string]any, path, method string) map[string]any {
	t.Helper()
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatalf("document has no paths: %v", document)
	}
	item, ok := paths[path].(map[string]any)
	if !ok {
		t.Fatalf("no path %q in %v", path, paths)
	}
	operation, ok := item[method].(map[string]any)
	if !ok {
		t.Fatalf("no operation %s %s in %v", method, path, item)
	}
	return operation
}
