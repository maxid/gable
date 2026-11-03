package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func pageFor(t *testing.T, target string, allowed ...string) (Page, error) {
	t.Helper()
	server := New(WithTitle("page"))
	var parsed Page
	var failure error
	server.Router().GET("/list", func(ctx *Context) error {
		parsed, failure = ctx.Page(allowed...)
		if failure != nil {
			return failure
		}
		return ctx.JSON(200, parsed)
	}).Doc("list")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	if recorder.Code >= 400 && failure == nil {
		t.Fatalf("status = %d with no error", recorder.Code)
	}
	return parsed, failure
}

// Defaults, clamping and both spellings of every parameter.
func TestPageParsesAndClamps(t *testing.T) {
	page, err := pageFor(t, "/list")
	if err != nil {
		t.Fatal(err)
	}
	if page.Number != 1 || page.Size != DefaultPageSize || page.Offset() != 0 {
		t.Fatalf("defaults = %+v", page)
	}

	page, err = pageFor(t, "/list?page=3&size=50&sort=name&order=desc", "name", "created_at")
	if err != nil {
		t.Fatal(err)
	}
	if page.Number != 3 || page.Size != 50 || page.Sort != "name" || !page.Desc {
		t.Fatalf("parsed = %+v", page)
	}
	if page.Offset() != 100 || page.Limit() != 50 {
		t.Fatalf("offset/limit = %d/%d", page.Offset(), page.Limit())
	}

	// The vamis aliases the existing frontend sends.
	page, err = pageFor(t, "/list?page=2&perPage=15&orderBy=created_at&orderDir=DESC", "created_at")
	if err != nil {
		t.Fatal(err)
	}
	if page.Number != 2 || page.Size != 15 || page.Sort != "created_at" || !page.Desc {
		t.Fatalf("aliases = %+v", page)
	}

	// Out of range values are clamped rather than rejected: a page size of 10000 is a
	// performance problem, not a client bug worth a 400.
	page, err = pageFor(t, "/list?page=0&size=10000")
	if err != nil {
		t.Fatal(err)
	}
	if page.Number != 1 || page.Size != MaxPageSize {
		t.Fatalf("clamped = %+v", page)
	}
}

// A sort column outside the whitelist is a client error named after the allowed values,
// never a silently ignored sort.
func TestPageRejectsUnknownSort(t *testing.T) {
	_, err := pageFor(t, "/list?sort=password_hash", "name", "created_at")
	if err == nil {
		t.Fatal("an unknown sort must fail")
	}
	message := err.Error()
	if !strings.Contains(message, "invalid sort") || !strings.Contains(message, "name, created_at") {
		t.Fatalf("message = %q", message)
	}
	// Case does not matter for a whitelisted column.
	if _, err := pageFor(t, "/list?sort=NAME", "name"); err != nil {
		t.Fatalf("case-insensitive whitelist = %v", err)
	}
	// No whitelist means no restriction (the handler knows its own columns).
	if _, err := pageFor(t, "/list?sort=anything"); err != nil {
		t.Fatalf("unrestricted sort = %v", err)
	}
}

func TestPageRejectsUnknownOrder(t *testing.T) {
	_, err := pageFor(t, "/list?order=random")
	if err == nil || !strings.Contains(err.Error(), "invalid order") {
		t.Fatalf("err = %v", err)
	}
}
