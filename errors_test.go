package typed

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewError(t *testing.T) {
	e := NewError(404, "x")
	if e.Status != 404 || e.Title != "Not Found" || e.Type != "about:blank" {
		t.Fatalf("unexpected error: %+v", e)
	}
	if got := e.Error(); got != "404 Not Found: x" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestFieldError(t *testing.T) {
	e := FieldError("body.email", "bad")
	if e.Status != http.StatusUnprocessableEntity || e.Detail != "validation failed" {
		t.Fatalf("unexpected error: %+v", e)
	}
	if len(e.Errors) != 1 || e.Errors[0] != (ErrorDetail{Location: "body.email", Message: "bad"}) {
		t.Fatalf("unexpected details: %+v", e.Errors)
	}
}

func TestWriteErrorTyped(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	writeError(rec, req, NotFound("nope"))
	if rec.Code != 404 {
		t.Fatalf("code = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type = %q", ct)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["status"] != float64(404) || m["detail"] != "nope" {
		t.Fatalf("body = %s", rec.Body)
	}
	if _, ok := m["errors"]; ok {
		t.Fatalf("errors key should be omitted: %s", rec.Body)
	}
}

func TestWriteErrorWrapped(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	writeError(rec, req, fmt.Errorf("wrap: %w", Conflict("dup")))
	if rec.Code != 409 {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestWriteErrorUnknown(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	writeError(rec, req, errors.New("db down"))
	if rec.Code != 500 {
		t.Fatalf("code = %d", rec.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["detail"] != "internal server error" {
		t.Fatalf("body = %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), "db down") {
		t.Fatalf("leaked internal error: %s", rec.Body)
	}
}
