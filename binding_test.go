package typed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

type paramsIn struct {
	ID    int        `path:"id"`
	Limit int        `query:"limit" default:"20"`
	Tags  []string   `query:"tag"`
	Token string     `header:"X-Token"`
	Since *time.Time `query:"since"`
}

type bodyIn struct {
	Body struct {
		Name string `json:"name" required:"true" minLength:"2"`
	}
}

func newReq(method, target, body string, pathParams map[string]string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	if pathParams != nil {
		rctx := chi.NewRouteContext()
		for k, v := range pathParams {
			rctx.URLParams.Add(k, v)
		}
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}
	return r
}

func mustPlan(t *testing.T, typ reflect.Type) *inputPlan {
	t.Helper()
	p, err := newInputPlan(typ)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func bindTo[T any](t *testing.T, r *http.Request, max int64) (T, *Error) {
	t.Helper()
	var in T
	p := mustPlan(t, reflect.TypeOf(in))
	return in, p.bind(r, reflect.ValueOf(&in).Elem(), max)
}

func TestBindParams(t *testing.T) {
	r := newReq("GET", "/x?limit=5&tag=a&tag=b&since=2024-01-02T03:04:05Z", "", map[string]string{"id": "7"})
	r.Header.Set("x-token", "secret")
	in, err := bindTo[paramsIn](t, r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if in.ID != 7 || in.Limit != 5 || in.Token != "secret" || !reflect.DeepEqual(in.Tags, []string{"a", "b"}) {
		t.Fatalf("unexpected: %+v", in)
	}
	if in.Since == nil || in.Since.Year() != 2024 {
		t.Fatalf("since = %v", in.Since)
	}

	in, err = bindTo[paramsIn](t, newReq("GET", "/x", "", map[string]string{"id": "1"}), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if in.Limit != 20 || in.Since != nil || in.Tags != nil {
		t.Fatalf("defaults: %+v", in)
	}
}

func TestQuerySliceDefault(t *testing.T) {
	type q struct {
		IDs []int `query:"id" default:"1,2"`
	}
	in, err := bindTo[q](t, newReq("GET", "/x", "", nil), 1<<20)
	if err != nil || !reflect.DeepEqual(in.IDs, []int{1, 2}) {
		t.Fatalf("in=%+v err=%+v", in, err)
	}
	ps, perr := mustPlan(t, reflect.TypeOf(q{})).parameters(newRegistry())
	if perr != nil || !reflect.DeepEqual(ps[0].Schema.Default, []any{1, 2}) {
		t.Fatalf("default = %#v err=%v", ps[0].Schema.Default, perr)
	}
}

func TestBindBodyElementDefaults(t *testing.T) {
	type item struct {
		Role string `json:"role" default:"member"`
	}
	type in struct {
		Body struct {
			Items []item   `json:"items"`
			Tags  []string `json:"tags" default:"a,b"`
		}
	}
	got, err := bindTo[in](t, newReq("POST", "/x", `{"items":[{}]}`, nil), 1<<20)
	if err != nil || len(got.Body.Items) != 1 || got.Body.Items[0].Role != "member" ||
		!reflect.DeepEqual(got.Body.Tags, []string{"a", "b"}) {
		t.Fatalf("got %+v err=%+v", got.Body, err)
	}
}

func TestBindParamParseErrors(t *testing.T) {
	_, err := bindTo[paramsIn](t, newReq("GET", "/x?limit=abc", "", map[string]string{"id": "x"}), 1<<20)
	if err == nil || err.Status != 422 || len(err.Errors) != 2 {
		t.Fatalf("got %+v", err)
	}
	if err.Errors[0].Location != "path.id" || err.Errors[1].Location != "query.limit" {
		t.Fatalf("locations: %+v", err.Errors)
	}
	for _, d := range err.Errors {
		if d.Message != "invalid value" {
			t.Fatalf("message: %+v", d)
		}
	}
}

func TestBindBodyJSON(t *testing.T) {
	for _, ct := range []string{"", "application/json", "application/json; charset=utf-8", "application/merge-patch+json"} {
		r := newReq("POST", "/x", `{"name":"bob"}`, nil)
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		in, err := bindTo[bodyIn](t, r, 1<<20)
		if err != nil || in.Body.Name != "bob" {
			t.Fatalf("ct %q: %+v %v", ct, in, err)
		}
	}
}

func TestBindBodyBadContentType(t *testing.T) {
	r := newReq("POST", "/x", `{"name":"bob"}`, nil)
	r.Header.Set("Content-Type", "text/plain")
	if _, err := bindTo[bodyIn](t, r, 1<<20); err == nil || err.Status != 400 {
		t.Fatalf("got %+v", err)
	}
}

func TestBindBodyMalformed(t *testing.T) {
	_, err := bindTo[bodyIn](t, newReq("POST", "/x", `{"name":`, nil), 1<<20)
	if err == nil || err.Status != 400 || !strings.HasPrefix(err.Detail, "invalid JSON") {
		t.Fatalf("got %+v", err)
	}
}

func TestBindBodyTooLarge(t *testing.T) {
	_, err := bindTo[bodyIn](t, newReq("POST", "/x", `{"name":"`+strings.Repeat("a", 100)+`"}`, nil), 10)
	if err == nil || err.Status != 413 {
		t.Fatalf("got %+v", err)
	}
}

func TestBindEmptyBodyRequired(t *testing.T) {
	_, err := bindTo[bodyIn](t, newReq("POST", "/x", "", nil), 1<<20)
	if err == nil || err.Status != 422 || len(err.Errors) != 1 ||
		err.Errors[0].Location != "body.name" || err.Errors[0].Message != "is required" {
		t.Fatalf("got %+v", err)
	}
}

func TestBindValidationParamsAndBody(t *testing.T) {
	type in struct {
		Limit int `query:"limit" minimum:"1" maximum:"100"`
		Body  struct {
			Name string `json:"name" minLength:"2"`
		}
	}
	_, err := bindTo[in](t, newReq("POST", "/x?limit=500", `{"name":"a"}`, nil), 1<<20)
	if err == nil || err.Status != 422 || len(err.Errors) != 2 ||
		err.Errors[0].Location != "query.limit" || err.Errors[1].Location != "body.name" {
		t.Fatalf("got %+v", err)
	}
	// A present zero value still violates the minimum.
	_, err = bindTo[in](t, newReq("POST", "/x?limit=0", "", nil), 1<<20)
	if err == nil || len(err.Errors) != 1 || err.Errors[0].Location != "query.limit" {
		t.Fatalf("zero: %+v", err)
	}
}

func TestBindRequiredParams(t *testing.T) {
	type in struct {
		Q string `query:"q" required:"true"`
		H string `header:"X-H" required:"true"`
	}
	_, err := bindTo[in](t, newReq("GET", "/x", "", nil), 1<<20)
	if err == nil || len(err.Errors) != 2 || err.Errors[0].Location != "query.q" ||
		err.Errors[0].Message != "is required" || err.Errors[1].Location != "header.X-H" {
		t.Fatalf("got %+v", err)
	}
}

func TestNewInputPlanErrors(t *testing.T) {
	type both struct {
		A string `path:"a" query:"a"`
	}
	type hdrSlice struct {
		A []int `header:"a"`
	}
	type mapQuery struct {
		A map[string]string `query:"a"`
	}
	type bytesQuery struct {
		A []byte `query:"a"`
	}
	type dupQuery struct {
		A string `query:"a"`
		B int    `query:"a"`
	}
	type dupHeader struct {
		A string `header:"x-id"`
		B string `header:"X-Id"`
	}
	type embeddedPtr struct {
		*PageParams
	}
	type untagged struct {
		Extra string
	}
	type deepUntagged struct {
		PageParams
		inner
	}
	for name, typ := range map[string]reflect.Type{
		"embedded ptr":  reflect.TypeOf(embeddedPtr{}),
		"untagged":      reflect.TypeOf(untagged{}),
		"deep untagged": reflect.TypeOf(deepUntagged{}),
		"bytes query":   reflect.TypeOf(bytesQuery{}),
		"dup query":     reflect.TypeOf(dupQuery{}),
		"dup header":    reflect.TypeOf(dupHeader{}),
		"non-struct":    reflect.TypeOf(1),
		"both":          reflect.TypeOf(both{}),
		"hdr slice":     reflect.TypeOf(hdrSlice{}),
		"map query":     reflect.TypeOf(mapQuery{}),
	} {
		if _, err := newInputPlan(typ); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// PageParams is embedded in input structs to share pagination parameters.
type PageParams struct {
	Limit int `query:"limit" minimum:"1" default:"10"`
}

type inner struct {
	Missing string
}

type embeddedIn struct {
	PageParams
	Q      string `query:"q"`
	hidden int
}

func TestBindEmbeddedParams(t *testing.T) {
	in, err := bindTo[embeddedIn](t, newReq("GET", "/x?q=z", "", nil), 1<<20)
	if err != nil || in.Limit != 10 || in.Q != "z" {
		t.Fatalf("default: in=%+v err=%+v", in, err)
	}
	in, err = bindTo[embeddedIn](t, newReq("GET", "/x?limit=5", "", nil), 1<<20)
	if err != nil || in.Limit != 5 {
		t.Fatalf("bound: in=%+v err=%+v", in, err)
	}
	_, err = bindTo[embeddedIn](t, newReq("GET", "/x?limit=0", "", nil), 1<<20)
	if err == nil || err.Status != 422 || len(err.Errors) != 1 || err.Errors[0].Location != "query.limit" {
		t.Fatalf("validation: %+v", err)
	}
	ps, perr := mustPlan(t, reflect.TypeOf(embeddedIn{})).parameters(newRegistry())
	if perr != nil || len(ps) != 2 || ps[0].Name != "limit" || ps[0].In != "query" {
		t.Fatalf("params = %+v err=%v", ps, perr)
	}
	if s := ps[0].Schema; s.Default != 10 || s.Minimum == nil || *s.Minimum != 1 {
		t.Fatalf("limit schema = %+v", s)
	}
}

func TestParametersSpec(t *testing.T) {
	p := mustPlan(t, reflect.TypeOf(paramsIn{}))
	ps, err := p.parameters(newRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 5 {
		t.Fatalf("got %d params", len(ps))
	}
	by := map[string]*Parameter{}
	for _, x := range ps {
		by[x.Name] = x
	}
	if id := by["id"]; id.In != "path" || !id.Required {
		t.Errorf("id: %+v", id)
	}
	if by["limit"].Schema.Default != 20 {
		t.Errorf("limit default: %v", by["limit"].Schema.Default)
	}
	if tag := by["tag"].Schema; tag.Type != "array" || tag.Items.Type != "string" {
		t.Errorf("tag: %+v", tag)
	}
	if got := p.PathParams(); !reflect.DeepEqual(got, []string{"id"}) {
		t.Errorf("PathParams = %v", got)
	}
}

func TestRequestBodySpec(t *testing.T) {
	g := newRegistry()
	rb, err := mustPlan(t, reflect.TypeOf(bodyIn{})).requestBody(g)
	if err != nil || rb == nil || !rb.Required || rb.Content["application/json"] == nil {
		t.Fatalf("got %+v %v", rb, err)
	}
	rb, _ = mustPlan(t, reflect.TypeOf(paramsIn{})).requestBody(g)
	if rb != nil {
		t.Fatal("expected nil body")
	}
}

func TestBindBodyTypeError(t *testing.T) {
	_, err := bindTo[bodyIn](t, newReq("POST", "/x", `{"name": 5}`, nil), 1<<20)
	if err == nil || err.Status != 400 || err.Detail != "invalid JSON: body.name must be string" {
		t.Fatalf("got %+v", err)
	}
	if strings.Contains(err.Detail, "Go") || strings.Contains(err.Detail, "int") {
		t.Fatalf("leaks internals: %q", err.Detail)
	}
	_, err = bindTo[bodyIn](t, newReq("POST", "/x", `[1]`, nil), 1<<20)
	if err == nil || err.Detail != "invalid JSON: body must be object" {
		t.Fatalf("got %+v", err)
	}
}
