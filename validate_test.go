package typed

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func validate(v any) []ErrorDetail {
	var errs []ErrorDetail
	validateStruct(reflect.ValueOf(v), "body", &errs)
	return errs
}

func TestValidateRules(t *testing.T) {
	type req struct {
		Name string `json:"name" required:"true"`
	}
	type short struct {
		S string `json:"s" minLength:"3"`
	}
	type long struct {
		S string `json:"s" maxLength:"2"`
	}
	type pat struct {
		S string `json:"s" pattern:"^[a-z]+$"`
	}
	type enum struct {
		S string `json:"s" enum:"a,b"`
	}
	type enumNum struct {
		N float64 `json:"n" enum:"1.0,2"`
	}
	type min struct {
		N int `json:"n" minimum:"5"`
	}
	type max struct {
		N float64 `json:"n" maximum:"1.5"`
	}
	type minItems struct {
		L []int `json:"l" minItems:"2"`
	}
	type maxItems struct {
		L []int `json:"l" maxItems:"1"`
	}
	type email struct {
		E string `json:"e" format:"email"`
	}
	type uuid struct {
		U string `json:"u" format:"uuid"`
	}
	type dt struct {
		D string `json:"d" format:"date-time"`
	}
	type date struct {
		D string `json:"d" format:"date"`
	}
	type uri struct {
		U string `json:"u" format:"uri"`
	}
	type valid struct {
		Name  string   `json:"name" required:"true" minLength:"1"`
		Email string   `json:"email" format:"email"`
		Age   uint8    `json:"age" minimum:"18" maximum:"99"`
		Tags  []string `json:"tags" enum:"a,b" maxItems:"3"`
	}

	tests := []struct {
		name string
		in   any
		want []ErrorDetail
	}{
		{"required", req{}, []ErrorDetail{{Location: "body.name", Message: "is required"}}},
		{"minLength", short{"ab"}, []ErrorDetail{{"body.s", "must be at least 3 characters", "ab"}}},
		{"runes", short{"日本"}, []ErrorDetail{{"body.s", "must be at least 3 characters", "日本"}}},
		{"maxLength", long{"abc"}, []ErrorDetail{{"body.s", "must be at most 2 characters", "abc"}}},
		{"pattern", pat{"A1"}, []ErrorDetail{{"body.s", "must match pattern ^[a-z]+$", "A1"}}},
		{"enum", enum{"c"}, []ErrorDetail{{"body.s", "must be one of: a, b", "c"}}},
		{"enum number value", enumNum{1}, nil},
		{"enum number bad", enumNum{3}, []ErrorDetail{{"body.n", "must be one of: 1.0, 2", float64(3)}}},
		{"minimum", min{3}, []ErrorDetail{{"body.n", "must be >= 5", 3}}},
		{"maximum", max{2}, []ErrorDetail{{"body.n", "must be <= 1.5", float64(2)}}},
		{"minItems", minItems{[]int{1}}, []ErrorDetail{{"body.l", "must have at least 2 items", []int{1}}}},
		{"maxItems", maxItems{[]int{1, 2}}, []ErrorDetail{{"body.l", "must have at most 1 items", []int{1, 2}}}},
		{"email", email{"nope"}, []ErrorDetail{{"body.e", "must be a valid email", "nope"}}},
		{"email display name", email{"A <a@b.co>"}, []ErrorDetail{{"body.e", "must be a valid email", "A <a@b.co>"}}},
		{"email ok", email{"a@b.co"}, nil},
		{"uuid", uuid{"123"}, []ErrorDetail{{"body.u", "must be a valid uuid", "123"}}},
		{"uuid ok", uuid{"123e4567-e89b-12d3-a456-426614174000"}, nil},
		{"date-time", dt{"2024-01-01"}, []ErrorDetail{{"body.d", "must be a valid date-time", "2024-01-01"}}},
		{"date-time ok", dt{"2024-01-01T10:00:00Z"}, nil},
		{"date", date{"01/02/2024"}, []ErrorDetail{{"body.d", "must be a valid date", "01/02/2024"}}},
		{"uri", uri{"/relative"}, []ErrorDetail{{"body.u", "must be a valid uri", "/relative"}}},
		{"uri ok", uri{"https://x.dev/a"}, nil},
		{"valid", valid{Name: "Ann", Email: "ann@x.io", Age: 30, Tags: []string{"a"}}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := validate(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v\nwant %#v", got, tc.want)
			}
		})
	}
}

func TestValidateCollectsAll(t *testing.T) {
	type in struct {
		A string `json:"a" required:"true"`
		B int    `json:"b" minimum:"5"`
	}
	got := validate(in{B: 1})
	if len(got) != 2 || got[0].Location != "body.a" || got[1].Location != "body.b" {
		t.Errorf("got %#v", got)
	}
}

func TestValidateNestedSliceLocation(t *testing.T) {
	type in struct {
		Items []struct {
			Name string `json:"name" required:"true"`
		} `json:"items"`
	}
	var v in
	v.Items = make([]struct {
		Name string `json:"name" required:"true"`
	}, 3)
	v.Items[0].Name, v.Items[1].Name = "a", "b"
	got := validate(v)
	want := []ErrorDetail{{Location: "body.items[2].name", Message: "is required"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
}

func TestValidateSliceElementRules(t *testing.T) {
	type in struct {
		Tags []string `json:"tags" enum:"a,b"`
	}
	got := validate(in{Tags: []string{"a", "x"}})
	want := []ErrorDetail{{"body.tags[1]", "must be one of: a, b", "x"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
}

func TestValidateMapAndPointerStruct(t *testing.T) {
	type inner struct {
		N string `json:"n" required:"true"`
	}
	type in struct {
		M map[string]inner `json:"m"`
		P *inner           `json:"p"`
	}
	got := validate(in{M: map[string]inner{"k": {}}, P: &inner{}})
	want := []ErrorDetail{
		{Location: "body.m.k.n", Message: "is required"},
		{Location: "body.p.n", Message: "is required"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
}

func TestValidateNilEmbeddedPointer(t *testing.T) {
	type base struct {
		ID string `json:"id" required:"true"`
	}
	type in struct {
		*base
		N int `json:"n"`
	}
	if got := validate(in{}); got != nil {
		t.Errorf("got %#v", got)
	}
	applyDefaults(reflect.ValueOf(&in{}).Elem()) // must not panic
}

func TestValidatePointerRequired(t *testing.T) {
	type in struct {
		N *int `json:"n" required:"true"`
	}
	got := validate(in{})
	want := []ErrorDetail{{Location: "body.n", Message: "is required"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("nil: got %#v", got)
	}
	zero := 0
	if got := validate(in{N: &zero}); got != nil {
		t.Errorf("pointer to 0: got %#v", got)
	}
}

func TestValidateOptionalZeroSkipped(t *testing.T) {
	type in struct {
		Email string `json:"email" format:"email"`
	}
	if got := validate(in{}); got != nil {
		t.Errorf("got %#v", got)
	}
}

func TestApplyDefaults(t *testing.T) {
	type inner struct {
		Limit int `json:"limit" default:"10"`
	}
	type in struct {
		Role  string `json:"role" default:"member"`
		Other string `json:"other" default:"x"`
		Inner inner  `json:"inner"`
	}
	v := in{Other: "set"}
	applyDefaults(reflect.ValueOf(&v))
	if v.Role != "member" || v.Inner.Limit != 10 || v.Other != "set" {
		t.Errorf("got %+v", v)
	}
}

type hooked struct{ err error }

func (h hooked) Validate() error { return h.err }

func TestRunValidateHook(t *testing.T) {
	fe := FieldError("body.x", "bad")
	if got := runValidateHook(hooked{fe}); got != error(fe) {
		t.Errorf("got %v want same *Error", got)
	}
	var pe *Error
	if err := runValidateHook(hooked{errors.New("nope")}); !errors.As(err, &pe) ||
		pe.Status != 422 || pe.Detail != "nope" {
		t.Errorf("got %#v", err)
	}
	if err := runValidateHook(struct{}{}); err != nil {
		t.Errorf("no hook: got %v", err)
	}
	if err := runValidateHook(hooked{}); err != nil {
		t.Errorf("nil error: got %v", err)
	}
	var typedNil *Error
	if err := runValidateHook(hooked{typedNil}); err != nil {
		t.Errorf("typed nil *Error: got %#v", err)
	}
}

func TestValidateZeroSliceElements(t *testing.T) {
	type s struct {
		Tags []string `json:"tags" enum:"a,b"`
	}
	got := validate(s{Tags: []string{"a", ""}})
	want := []ErrorDetail{{"body.tags[1]", "must be one of: a, b", ""}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
	type n struct {
		N []int `json:"n" minimum:"1"`
	}
	got = validate(n{N: []int{0}})
	want = []ErrorDetail{{"body.n[0]", "must be >= 1", 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
}

func TestValidateRequiredSkipsOtherChecks(t *testing.T) {
	type in struct {
		S string `json:"s" required:"true" minLength:"3"`
	}
	got := validate(in{})
	want := []ErrorDetail{{Location: "body.s", Message: "is required"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}
}

func TestValidateInterfaceField(t *testing.T) {
	type in struct {
		V any `json:"v"`
	}
	type req struct {
		V any `json:"v" required:"true"`
	}
	if got := validate(in{}); got != nil {
		t.Errorf("got %#v", got)
	}
	want := []ErrorDetail{{Location: "body.v", Message: "is required"}}
	if got := validate(req{}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}
}

type hiddenBase struct {
	ID string `json:"id" required:"true"`
}

func TestValidateUnexportedEmbedded(t *testing.T) {
	type in struct {
		hiddenBase
	}
	got := validate(in{})
	want := []ErrorDetail{{Location: "body.id", Message: "is required"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}
}

func TestValidateTimeLeaf(t *testing.T) {
	type in struct {
		At time.Time `json:"at" format:"date-time"`
	}
	type req struct {
		At time.Time `json:"at" required:"true"`
	}
	if got := validate(in{}); got != nil {
		t.Errorf("zero: %#v", got)
	}
	if got := validate(in{At: time.Now()}); got != nil {
		t.Errorf("set: %#v", got)
	}
	if got := validate(req{}); len(got) != 1 || got[0].Message != "is required" {
		t.Errorf("required: %#v", got)
	}
}
