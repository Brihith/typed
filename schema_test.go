package typed

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

type schemaUser struct {
	ID   int    `json:"id"`
	Name string `json:"name" required:"true" minLength:"1"`
}

type Page[T any] struct {
	Items []T `json:"items"`
}

type schemaNode struct {
	Children []schemaNode `json:"children"`
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSchemaScalars(t *testing.T) {
	tests := []struct {
		typ  reflect.Type
		want string
	}{
		{reflect.TypeOf(true), `{"type":"boolean"}`},
		{reflect.TypeOf(int32(0)), `{"type":"integer","format":"int32"}`},
		{reflect.TypeOf(0), `{"type":"integer","format":"int64"}`},
		{reflect.TypeOf(1.5), `{"type":"number"}`},
		{reflect.TypeOf(""), `{"type":"string"}`},
		{reflect.TypeOf(time.Time{}), `{"type":"string","format":"date-time"}`},
		{reflect.TypeOf([]byte(nil)), `{"type":"string","contentEncoding":"base64"}`},
		{reflect.TypeOf([]string(nil)), `{"type":"array","items":{"type":"string"}}`},
		{reflect.TypeOf(map[string]int(nil)), `{"type":"object","additionalProperties":{"type":"integer","format":"int64"}}`},
		{reflect.TypeOf((*string)(nil)), `{"type":["string","null"]}`},
		{reflect.TypeOf((*time.Time)(nil)), `{"type":["string","null"],"format":"date-time"}`},
		{reflect.TypeOf((*any)(nil)).Elem(), `{}`},
	}
	for _, tc := range tests {
		s, err := newRegistry().schemaFor(tc.typ)
		if err != nil {
			t.Fatalf("%s: %v", tc.typ, err)
		}
		if got := mustJSON(t, s); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.typ, got, tc.want)
		}
	}
}

func TestSchemaStructRef(t *testing.T) {
	g := newRegistry()
	s, err := g.schemaFor(reflect.TypeOf(schemaUser{}))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := mustJSON(t, s), `{"$ref":"#/components/schemas/schemaUser"}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	u := g.components()["schemaUser"]
	if u == nil || u.Properties["id"] == nil || u.Properties["name"] == nil {
		t.Fatalf("bad component: %s", mustJSON(t, u))
	}
	if !reflect.DeepEqual(u.Required, []string{"name"}) {
		t.Errorf("required = %v", u.Required)
	}
	if ml := u.Properties["name"].MinLength; ml == nil || *ml != 1 {
		t.Errorf("minLength = %v", ml)
	}
}

func TestSchemaEnumDefaultTyped(t *testing.T) {
	type q struct {
		Limit int `enum:"10,20" default:"10"`
	}
	g := newRegistry()
	if _, err := g.schemaFor(reflect.TypeOf(q{})); err != nil {
		t.Fatal(err)
	}
	got := mustJSON(t, g.components()["q"].Properties["Limit"])
	for _, want := range []string{`"enum":[10,20]`, `"default":10`} {
		if !strings.Contains(got, want) {
			t.Errorf("%s missing %s", got, want)
		}
	}
}

func TestSchemaSliceDefault(t *testing.T) {
	type q struct {
		Tags []string `json:"tags" default:"a,b"`
		Nums []int    `json:"nums" default:"1,2"`
	}
	g := newRegistry()
	if _, err := g.schemaFor(reflect.TypeOf(q{})); err != nil {
		t.Fatal(err)
	}
	props := g.components()["q"].Properties
	if got := mustJSON(t, props["tags"]); !strings.Contains(got, `"default":["a","b"]`) {
		t.Errorf("tags: %s", got)
	}
	if got := mustJSON(t, props["nums"]); !strings.Contains(got, `"default":[1,2]`) {
		t.Errorf("nums: %s", got)
	}
}

func TestSchemaRecursive(t *testing.T) {
	g := newRegistry()
	if _, err := g.schemaFor(reflect.TypeOf(schemaNode{})); err != nil {
		t.Fatal(err)
	}
	items := g.components()["schemaNode"].Properties["children"].Items
	if items.Ref != "#/components/schemas/schemaNode" {
		t.Errorf("items ref = %q", items.Ref)
	}
}

func TestSchemaAnonymousInline(t *testing.T) {
	g := newRegistry()
	s, err := g.schemaFor(reflect.TypeOf(struct {
		A string `json:"a"`
	}{}))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := mustJSON(t, s), `{"type":"object","properties":{"a":{"type":"string"}}}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if len(g.components()) != 0 {
		t.Errorf("components = %v", g.components())
	}
}

func TestSchemaGenericName(t *testing.T) {
	g := newRegistry()
	if _, err := g.schemaFor(reflect.TypeOf(Page[schemaUser]{})); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.components()["PageschemaUser"]; !ok {
		t.Errorf("components = %v", g.components())
	}
	g = newRegistry()
	if _, err := g.schemaFor(reflect.TypeOf(Page[[]string]{})); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.components()["Pagestring"]; !ok {
		t.Errorf("components = %v", g.components())
	}
}

func TestSchemaNullableRef(t *testing.T) {
	s, err := newRegistry().schemaFor(reflect.TypeOf(&schemaUser{}))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"anyOf":[{"$ref":"#/components/schemas/schemaUser"},{"type":"null"}]}`
	if got := mustJSON(t, s); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestSchemaSetName(t *testing.T) {
	g := newRegistry()
	g.setName(reflect.TypeOf(schemaUser{}), "Person")
	s, err := g.schemaFor(reflect.TypeOf(schemaUser{}))
	if err != nil {
		t.Fatal(err)
	}
	if s.Ref != "#/components/schemas/Person" {
		t.Errorf("ref = %q", s.Ref)
	}
	if g.components()["Person"] == nil {
		t.Error("Person not registered")
	}
}

func TestSchemaUnsupported(t *testing.T) {
	if _, err := newRegistry().schemaFor(reflect.TypeOf(make(chan int))); err == nil {
		t.Error("want error for chan")
	}
	if _, err := newRegistry().schemaFor(reflect.TypeOf(map[int]string{})); err == nil {
		t.Error("want error for non-string map key")
	}
}

func TestSchemaRefPropertyDocOnly(t *testing.T) {
	type outer struct {
		U schemaUser `json:"u" doc:"the user" minLength:"3"`
	}
	g := newRegistry()
	if _, err := g.schemaFor(reflect.TypeOf(outer{})); err != nil {
		t.Fatal(err)
	}
	want := `{"$ref":"#/components/schemas/schemaUser","description":"the user"}`
	if got := mustJSON(t, g.components()["outer"].Properties["u"]); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestSchemaSliceElementRules(t *testing.T) {
	type q struct {
		Tags []string `enum:"a,b" minItems:"1" doc:"tags"`
	}
	g := newRegistry()
	if _, err := g.schemaFor(reflect.TypeOf(q{})); err != nil {
		t.Fatal(err)
	}
	got := mustJSON(t, g.components()["q"].Properties["Tags"])
	want := `{"type":"array","description":"tags","items":{"type":"string","enum":["a","b"]},"minItems":1}`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

type textMarshalStruct struct{ A, B int }

func (textMarshalStruct) MarshalText() ([]byte, error) { return []byte("x"), nil }

func TestSchemaTextMarshaler(t *testing.T) {
	s, err := newRegistry().schemaFor(reflect.TypeOf(textMarshalStruct{}))
	if err != nil || s.Ref != "" || s.Type != "string" {
		t.Fatalf("got %+v %v", s, err)
	}
}
