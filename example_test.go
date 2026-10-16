package typed_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Brihith/typed"
	"github.com/go-chi/chi/v5"
)

var update = flag.Bool("update", false, "rewrite golden files")

type getUserInput struct {
	ID      int  `path:"id" minimum:"1"`
	Verbose bool `query:"verbose" doc:"Include extra fields"`
}

type createUserInput struct {
	Body struct {
		Name  string `json:"name" required:"true" minLength:"1" maxLength:"100"`
		Email string `json:"email" required:"true" format:"email"`
		Role  string `json:"role,omitempty" enum:"admin,member" default:"member"`
	}
}

type deleteUserInput struct {
	ID int `path:"id" minimum:"1"`
}

type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email" example:"ada@example.com"`
}

func getUser(ctx context.Context, in *getUserInput) (*User, error) { return &User{}, nil }
func createUser(ctx context.Context, in *createUserInput) (*User, error) {
	return &User{}, nil
}
func deleteUser(ctx context.Context, in *deleteUserInput) (*struct{}, error) {
	return &struct{}{}, nil
}

func buildExampleAPI() (*chi.Mux, *typed.API) {
	r := chi.NewRouter()
	api := typed.New(r, typed.Config{Title: "Users API", Version: "1.0.0"})
	typed.Get(api, "/users/{id}", getUser, typed.Summary("Get a user"), typed.Tags("users"))
	typed.Post(api, "/users", createUser, typed.Status(http.StatusCreated), typed.Tags("users"))
	api.Route("/admin", func(admin *typed.API) {
		typed.Delete(admin, "/users/{id}", deleteUser)
	})
	return r, api
}

func TestGoldenSpec(t *testing.T) {
	_, api := buildExampleAPI()
	got, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	const path = "testdata/openapi.golden.json"
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("spec differs from %s (run with -update to rewrite)\ngot:\n%s", path, got)
	}
}

func TestSpecStructure(t *testing.T) {
	_, api := buildExampleAPI()
	raw, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v, want 3.1.0", doc["openapi"])
	}

	schemas := map[string]any{}
	if c, ok := doc["components"].(map[string]any); ok {
		if s, ok := c["schemas"].(map[string]any); ok {
			schemas = s
		}
	}
	const prefix = "#/components/schemas/"
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				if ref, ok := e.(string); ok && k == "$ref" {
					name, found := strings.CutPrefix(ref, prefix)
					if _, exists := schemas[name]; !found || !exists {
						t.Errorf("unresolved $ref %q", ref)
					}
				}
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(doc)

	paths, _ := doc["paths"].(map[string]any)
	if len(paths) == 0 {
		t.Fatal("no paths")
	}
	for p, item := range paths {
		for method, op := range item.(map[string]any) {
			resp, _ := op.(map[string]any)["responses"].(map[string]any)
			if _, ok := resp["default"]; !ok {
				t.Errorf("%s %s has no default response", method, p)
			}
		}
	}
}
