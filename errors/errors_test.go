package errors

import (
	"errors"
	"net/http"
	"testing"
)

func TestErrorChain(t *testing.T) {
	cause := errors.New("boom")
	err := New(CodeNotFound, "user not found").WithStatus(http.StatusNotFound).WithCause(cause)
	if !IsCode(err, CodeNotFound) {
		t.Fatal("IsCode should match")
	}
	if CodeOf(err) != CodeNotFound {
		t.Fatalf("CodeOf = %q", CodeOf(err))
	}
	if !errors.Is(err, cause) {
		t.Fatal("cause should be unwrapable")
	}
	if CodeOf(nil) != "" {
		t.Fatal("CodeOf(nil) must be empty")
	}
	if CodeOf(cause) != CodeInternal {
		t.Fatalf("unknown error code = %q", CodeOf(cause))
	}
}
