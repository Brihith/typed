package typed

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseRules(t *testing.T) {
	type S struct {
		F string `required:"true" minLength:"2" maxLength:"5" pattern:"^a" enum:"x, y" minimum:"1.5" format:"email" default:"x" doc:"d" example:"e"`
	}
	f, _ := reflect.TypeOf(S{}).FieldByName("F")
	r, err := parseRules(f)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Required {
		t.Error("Required not set")
	}
	if r.MinLength == nil || *r.MinLength != 2 {
		t.Errorf("MinLength = %v", r.MinLength)
	}
	if r.MaxLength == nil || *r.MaxLength != 5 {
		t.Errorf("MaxLength = %v", r.MaxLength)
	}
	if r.Minimum == nil || *r.Minimum != 1.5 {
		t.Errorf("Minimum = %v", r.Minimum)
	}
	if r.Maximum != nil || r.MinItems != nil || r.MaxItems != nil {
		t.Error("unexpected rules set")
	}
	if !reflect.DeepEqual(r.Enum, []string{"x", "y"}) {
		t.Errorf("Enum = %#v", r.Enum)
	}
	if r.Pattern == nil || !r.Pattern.MatchString("abc") || r.Pattern.MatchString("bc") {
		t.Error("Pattern wrong")
	}
	if r.Format != "email" || r.Default != "x" || !r.HasDefault || r.Doc != "d" || r.Example != "e" {
		t.Errorf("got %+v", r)
	}
}

func TestParseRulesErrors(t *testing.T) {
	type S struct {
		A string `minLength:"abc"`
		B string `pattern:"("`
		C int    `default:"nope"`
		D []int  `default:"anything"`
	}
	typ := reflect.TypeOf(S{})
	for _, name := range []string{"A", "B", "C"} {
		f, _ := typ.FieldByName(name)
		_, err := parseRules(f)
		if err == nil {
			t.Errorf("%s: expected error", name)
			continue
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%s: error %q does not name the field", name, err)
		}
	}
	f, _ := typ.FieldByName("D")
	if _, err := parseRules(f); err != nil {
		t.Errorf("slice default should not be checked: %v", err)
	}
}

type testBase struct {
	ID int `json:"id"`
}

func TestStructFields(t *testing.T) {
	type S struct {
		testBase
		Name       string `json:"name,omitempty"`
		Skipped    string `json:"-"`
		unexported string
		Untagged   string
	}
	_ = S{}.unexported
	fields, err := structFields(reflect.TypeOf(S{}))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range fields {
		names = append(names, f.Name)
	}
	if want := []string{"id", "name", "Untagged"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if !reflect.DeepEqual(fields[0].Index, []int{0, 0}) {
		t.Errorf("id Index = %v", fields[0].Index)
	}
	if !fields[1].OmitEmpty || fields[0].OmitEmpty {
		t.Error("OmitEmpty wrong")
	}
	again, _ := structFields(reflect.TypeOf(S{}))
	if len(again) != len(fields) {
		t.Error("cached result differs")
	}
}

func TestParseScalar(t *testing.T) {
	ok := []struct {
		in   string
		typ  reflect.Type
		want any
	}{
		{"42", reflect.TypeOf(int64(0)), int64(42)},
		{"-1", reflect.TypeOf(int8(0)), int8(-1)},
		{"3.5", reflect.TypeOf(float32(0)), float32(3.5)},
		{"true", reflect.TypeOf(true), true},
		{"hi", reflect.TypeOf(""), "hi"},
		{"2026-01-02T03:04:05Z", reflect.TypeOf(time.Time{}), time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
	}
	for _, c := range ok {
		v, err := parseScalar(c.in, c.typ)
		if err != nil {
			t.Errorf("%q as %v: %v", c.in, c.typ, err)
			continue
		}
		if !reflect.DeepEqual(v.Interface(), c.want) {
			t.Errorf("%q as %v = %v, want %v", c.in, c.typ, v.Interface(), c.want)
		}
	}
	v, err := parseScalar("7", reflect.TypeOf((*int)(nil)))
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := v.Interface().(*int); !ok || p == nil || *p != 7 {
		t.Errorf("pointer parse = %v", v.Interface())
	}
	bad := []struct {
		in  string
		typ reflect.Type
	}{
		{"300", reflect.TypeOf(uint8(0))},
		{"x", reflect.TypeOf(0)},
		{"1", reflect.TypeOf(struct{}{})},
	}
	for _, c := range bad {
		if _, err := parseScalar(c.in, c.typ); err == nil {
			t.Errorf("%q as %v: expected error", c.in, c.typ)
		}
	}
}
