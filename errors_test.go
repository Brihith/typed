package typed

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestWriteErrorTypedNil(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	var pe *Error
	writeError(rec, req, pe)
	if rec.Code != 500 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("code=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), `"detail":"internal server error"`) {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestWriteErrorNormalizes(t *testing.T) {
	for _, tc := range []struct {
		in         *Error
		code       int
		title, typ string
	}{
		{&Error{Detail: "x"}, 500, "Internal Server Error", "about:blank"},
		{&Error{Status: 42}, 500, "Internal Server Error", "about:blank"},
		{&Error{Status: 600}, 500, "Internal Server Error", "about:blank"},
		{&Error{Status: 418}, 418, "I'm a teapot", "about:blank"},
		{&Error{Status: 409, Title: "Taken", Type: "https://e/x"}, 409, "Taken", "https://e/x"},
	} {
		orig := *tc.in
		rec := httptest.NewRecorder()
		writeError(rec, httptest.NewRequest(http.MethodGet, "/", nil), tc.in)
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if rec.Code != tc.code || m["status"] != float64(tc.code) || m["title"] != tc.title || m["type"] != tc.typ {
			t.Errorf("%+v: code=%d body=%s", orig, rec.Code, rec.Body)
		}
		if !reflect.DeepEqual(*tc.in, orig) {
			t.Errorf("caller's error mutated: %+v -> %+v", orig, *tc.in)
		}
	}
}
