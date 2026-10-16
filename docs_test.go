package typed

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSpecEndpoint(t *testing.T) {
	api, r := newTestAPI(t, Config{Title: "Spec API", Version: "2"})
	Get(api, "/users/{id}", echoHandler)
	w := do(r, "GET", "/openapi.json", "")
	if w.Code != 200 {
		t.Fatalf("code = %d body %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	var doc struct {
		OpenAPI string `json:"openapi"`
		Info    struct {
			Title string `json:"title"`
		} `json:"info"`
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.1.0" || doc.Info.Title != "Spec API" {
		t.Fatalf("doc = %+v", doc)
	}
	if _, ok := doc.Paths["/users/{id}"]; !ok {
		t.Fatalf("paths = %v", doc.Paths)
	}
}

func TestDocsEndpoint(t *testing.T) {
	_, r := newTestAPI(t, Config{})
	w := do(r, "GET", "/docs", "")
	if w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
	body := w.Body.String()
	for _, want := range []string{"swagger-ui-dist@5.17.14", `url: "/openapi.json"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q", want)
		}
	}
}

func TestCustomAndDisabledPaths(t *testing.T) {
	_, r := newTestAPI(t, Config{SpecPath: "/spec.json", DocsPath: "-"})
	if w := do(r, "GET", "/spec.json", ""); w.Code != 200 {
		t.Fatalf("spec code = %d", w.Code)
	}
	if w := do(r, "GET", "/openapi.json", ""); w.Code != 404 {
		t.Fatalf("default spec code = %d", w.Code)
	}
	if w := do(r, "GET", "/docs", ""); w.Code != 404 {
		t.Fatalf("docs code = %d", w.Code)
	}
}

func TestDocsTitleEscaped(t *testing.T) {
	_, r := newTestAPI(t, Config{Title: "<x>", Version: "1"})
	if body := do(r, "GET", "/docs", "").Body.String(); !strings.Contains(body, "&lt;x&gt;") {
		t.Fatalf("title not escaped: %s", body)
	}
}
