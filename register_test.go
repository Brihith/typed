package typed

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func newTestAPI(t *testing.T, cfg Config) (*API, chi.Router) {
	t.Helper()
	if cfg.Title == "" {
		cfg.Title, cfg.Version = "Test", "1"
	}
	r := chi.NewRouter()
	return New(r, cfg), r
}

func do(r http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func mustPanic(t *testing.T, fn func(), substr string) {
	t.Helper()
	defer func() {
		t.Helper()
		v := recover()
		if v == nil {
			t.Fatalf("expected panic containing %q", substr)
		}
		msg, _ := v.(string)
		if !strings.HasPrefix(msg, "typed: ") || !strings.Contains(msg, substr) {
			t.Fatalf("panic = %v, want typed: prefix and %q", v, substr)
		}
	}()
	fn()
}

type getIn struct {
	ID      int  `path:"id"`
	Verbose bool `query:"verbose"`
}

type getOut struct {
	ID      int  `json:"id"`
	Verbose bool `json:"verbose"`
}

func echoHandler(_ context.Context, in *getIn) (*getOut, error) {
	return &getOut{ID: in.ID, Verbose: in.Verbose}, nil
}

func TestGetWithPathAndQuery(t *testing.T) {
	api, r := newTestAPI(t, Config{})
	Get(api, "/users/{id}", echoHandler)
	w := do(r, "GET", "/users/42?verbose=true", "")
	if w.Code != 200 {
		t.Fatalf("code = %d body %s", w.Code, w.Body)
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"id":42,"verbose":true}` {
		t.Fatalf("body = %s", got)
	}
}

type emptyIn struct{}
type okOut struct {
	OK bool `json:"ok"`
}

func TestPostStatusCreated(t *testing.T) {
	api, r := newTestAPI(t, Config{})
	Post(api, "/things", func(context.Context, *emptyIn) (*okOut, error) {
		return &okOut{OK: true}, nil
	}, Status(201))
	w := do(r, "POST", "/things", "")
	if w.Code != 201 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("code=%d ct=%q", w.Code, w.Header().Get("Content-Type"))
	}
}

func TestNoContent(t *testing.T) {
	api, r := newTestAPI(t, Config{})
	Delete(api, "/things", func(context.Context, *emptyIn) (*struct{}, error) {
		return &struct{}{}, nil
	})
	w := do(r, "DELETE", "/things", "")
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("code=%d body=%q", w.Code, w.Body)
	}
}

func TestHandlerTypedError(t *testing.T) {
	api, r := newTestAPI(t, Config{})
	Get(api, "/x", func(context.Context, *emptyIn) (*okOut, error) {
		return nil, NotFound("x")
	})
	w := do(r, "GET", "/x", "")
	if w.Code != 404 || w.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("code=%d ct=%q", w.Code, w.Header().Get("Content-Type"))
	}
}

func TestHandlerUnknownError(t *testing.T) {
	api, r := newTestAPI(t, Config{})
	Get(api, "/x", func(context.Context, *emptyIn) (*okOut, error) {
		return nil, errors.New("secret")
	})
	w := do(r, "GET", "/x", "")
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}
}

func TestNilOutput(t *testing.T) {
	api, r := newTestAPI(t, Config{})
	Get(api, "/x", func(context.Context, *emptyIn) (*okOut, error) { return nil, nil })
	if w := do(r, "GET", "/x", ""); w.Code != 500 {
		t.Fatalf("code = %d", w.Code)
	}
}

type hookIn struct {
	Name string `query:"name" minLength:"3"`
}

var hookCalls int

func (h *hookIn) Validate() error {
	hookCalls++
	return FieldError("query.name", "bad name")
}

func TestValidateHookRuns(t *testing.T) {
	api, r := newTestAPI(t, Config{})
	Get(api, "/x", func(context.Context, *hookIn) (*okOut, error) { return &okOut{}, nil })

	hookCalls = 0
	w := do(r, "GET", "/x?name=abcd", "")
	if w.Code != 422 || !strings.Contains(w.Body.String(), "query.name") || hookCalls != 1 {
		t.Fatalf("code=%d calls=%d body=%s", w.Code, hookCalls, w.Body)
	}

	hookCalls = 0
	w = do(r, "GET", "/x?name=a", "")
	if w.Code != 422 || hookCalls != 0 {
		t.Fatalf("code=%d calls=%d body=%s", w.Code, hookCalls, w.Body)
	}
}

func TestCustomErrorHandler(t *testing.T) {
	var got []error
	api, r := newTestAPI(t, Config{ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
		got = append(got, err)
		w.WriteHeader(418)
	}})
	boom := errors.New("boom")
	Get(api, "/users/{id}", echoHandler)
	Get(api, "/h", func(context.Context, *emptyIn) (*okOut, error) { return nil, boom })

	if w := do(r, "GET", "/users/abc", ""); w.Code != 418 {
		t.Fatalf("validation code = %d", w.Code)
	}
	if w := do(r, "GET", "/h", ""); w.Code != 418 {
		t.Fatalf("handler code = %d", w.Code)
	}
	if len(got) != 2 {
		t.Fatalf("got %d errors", len(got))
	}
	if pe, ok := got[0].(*Error); !ok || pe.Status != 422 {
		t.Fatalf("first error = %#v", got[0])
	}
	if got[1] != boom {
		t.Fatalf("second error = %v, want raw handler error", got[1])
	}
}

type regexIn struct {
	ID string `path:"id"`
}
type regexOut struct {
	ID string `json:"id"`
}

func TestRegexPathParam(t *testing.T) {
	api, r := newTestAPI(t, Config{})
	Get(api, "/users/{id:[0-9]+}", func(_ context.Context, in *regexIn) (*regexOut, error) {
		return &regexOut{ID: in.ID}, nil
	})
	w := do(r, "GET", "/users/7", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"7"`) {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}
	if _, ok := api.OpenAPI().Paths["/users/{id}"]; !ok {
		t.Fatalf("paths = %v", api.OpenAPI().Paths)
	}
	if w := do(r, "GET", "/users/abc", ""); w.Code != 404 {
		t.Fatalf("code = %d", w.Code)
	}
}

func TestRouteAndWith(t *testing.T) {
	api, r := newTestAPI(t, Config{})
	api.Route("/admin", func(sub *API) {
		mw := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("X-Mw", "1")
				next.ServeHTTP(w, req)
			})
		}
		Get(sub.With(mw), "/users/{id}", echoHandler)
		sub.With(mw).Route("/deep", func(d *API) {
			Get(d, "/users/{id}", echoHandler)
		})
	})
	w := do(r, "GET", "/admin/users/3", "")
	if w.Code != 200 || w.Header().Get("X-Mw") != "1" {
		t.Fatalf("code=%d hdr=%q", w.Code, w.Header().Get("X-Mw"))
	}
	paths := api.OpenAPI().Paths
	if paths["/admin/users/{id}"] == nil || paths["/admin/deep/users/{id}"] == nil {
		t.Fatalf("paths = %v", paths)
	}
	if w := do(r, "GET", "/admin/deep/users/3", ""); w.Code != 200 || w.Header().Get("X-Mw") != "1" {
		t.Fatalf("deep code=%d hdr=%q", w.Code, w.Header().Get("X-Mw"))
	}
}

func TestStartupPanics(t *testing.T) {
	type noField struct{}
	type extraField struct {
		ID int `path:"id"`
	}
	ok := func(context.Context, *getIn) (*okOut, error) { return &okOut{}, nil }

	mustPanic(t, func() {
		api, _ := newTestAPI(t, Config{})
		Get(api, "/users/{id}", func(context.Context, *noField) (*okOut, error) { return nil, nil })
	}, "path parameters")
	mustPanic(t, func() {
		api, _ := newTestAPI(t, Config{})
		Get(api, "/users", func(context.Context, *extraField) (*okOut, error) { return nil, nil })
	}, "path parameters")
	mustPanic(t, func() {
		api, _ := newTestAPI(t, Config{})
		Get(api, "/users/{id}", func(context.Context, *getIn) (*int, error) { return nil, nil })
	}, "must be a struct")
	mustPanic(t, func() {
		api, _ := newTestAPI(t, Config{})
		Get(api, "/users/{id}", ok)
		Get(api, "/users/{id}", ok)
	}, "duplicate")
	mustPanic(t, func() {
		api, _ := newTestAPI(t, Config{})
		Get(api, "/files/*", func(context.Context, *emptyIn) (*okOut, error) { return nil, nil })
	}, "wildcard")
	mustPanic(t, func() { New(chi.NewRouter(), Config{Version: "1"}) }, "Title")
	mustPanic(t, func() { New(chi.NewRouter(), Config{Title: "t"}) }, "Version")
}

func TestOperationSpec(t *testing.T) {
	api, _ := newTestAPI(t, Config{})
	Get(api, "/users/{id}", echoHandler, Summary("Get a user"), Tags("users"))
	Get(api, "/", func(context.Context, *emptyIn) (*okOut, error) { return nil, nil })
	spec := api.OpenAPI()
	if spec.OpenAPI != "3.1.0" {
		t.Fatalf("version = %q", spec.OpenAPI)
	}
	op := spec.Paths["/users/{id}"].Get
	if op.OperationID != "get-users-by-id" || op.Summary != "Get a user" || len(op.Tags) != 1 || op.Tags[0] != "users" {
		t.Fatalf("op = %+v", op)
	}
	if op.Responses["200"] == nil || op.Responses["default"] == nil {
		t.Fatalf("responses = %v", op.Responses)
	}
	if ref := op.Responses["default"].Content["application/problem+json"].Schema.Ref; ref != "#/components/schemas/ErrorModel" {
		t.Fatalf("default ref = %q", ref)
	}
	if spec.Components.Schemas["ErrorModel"] == nil {
		t.Fatal("ErrorModel missing from components")
	}
	if id := spec.Paths["/"].Get.OperationID; id != "get" {
		t.Fatalf("root id = %q", id)
	}
}

func TestPlainChiRoutesStillWork(t *testing.T) {
	api, r := newTestAPI(t, Config{})
	Get(api, "/users/{id}", echoHandler)
	r.Get("/raw", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "raw") })
	if w := do(r, "GET", "/raw", ""); w.Code != 200 || w.Body.String() != "raw" {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}
	if _, ok := api.OpenAPI().Paths["/raw"]; ok {
		t.Fatal("/raw must not be in the spec")
	}
}
