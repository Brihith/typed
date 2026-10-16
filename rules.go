package typed

import (
	"encoding"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// rules holds the validation and documentation constraints declared on a
// struct field through tags.
type rules struct {
	Required             bool
	MinLength, MaxLength *int
	MinItems, MaxItems   *int
	Minimum, Maximum     *float64
	Pattern              *regexp.Regexp
	Format               string
	Enum                 []string
	Default              string
	HasDefault           bool
	Doc, Example         string
}

// parseRules reads the constraint tags of f.
func parseRules(f reflect.StructField) (rules, error) {
	var r rules
	fail := func(tag string, err error) (rules, error) {
		return rules{}, fmt.Errorf("typed: field %s: invalid %s tag: %w", f.Name, tag, err)
	}

	if v, ok := f.Tag.Lookup("required"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fail("required", err)
		}
		r.Required = b
	}
	for _, p := range []struct {
		tag string
		dst **int
	}{
		{"minLength", &r.MinLength}, {"maxLength", &r.MaxLength},
		{"minItems", &r.MinItems}, {"maxItems", &r.MaxItems},
	} {
		if v, ok := f.Tag.Lookup(p.tag); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fail(p.tag, err)
			}
			*p.dst = &n
		}
	}
	for _, p := range []struct {
		tag string
		dst **float64
	}{
		{"minimum", &r.Minimum}, {"maximum", &r.Maximum},
	} {
		if v, ok := f.Tag.Lookup(p.tag); ok {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return fail(p.tag, err)
			}
			*p.dst = &n
		}
	}
	if v, ok := f.Tag.Lookup("pattern"); ok {
		re, err := regexp.Compile(v)
		if err != nil {
			return fail("pattern", err)
		}
		r.Pattern = re
	}
	if v, ok := f.Tag.Lookup("enum"); ok {
		for _, s := range strings.Split(v, ",") {
			r.Enum = append(r.Enum, strings.TrimSpace(s))
		}
	}
	r.Format = f.Tag.Get("format")
	r.Doc = f.Tag.Get("doc")
	r.Example = f.Tag.Get("example")
	if v, ok := f.Tag.Lookup("default"); ok {
		r.Default, r.HasDefault = v, true
		if f.Type.Kind() != reflect.Slice {
			if _, err := parseScalar(v, f.Type); err != nil {
				return fail("default", err)
			}
		}
	}
	return r, nil
}

// fieldInfo describes one JSON-visible field of a struct.
type fieldInfo struct {
	Index     []int
	Name      string
	OmitEmpty bool
	Field     reflect.StructField
	Rules     rules
}

type fieldsResult struct {
	fields []fieldInfo
	err    error
}

var fieldsCache sync.Map // reflect.Type -> fieldsResult

// structFields lists the fields of struct type t following encoding/json
// conventions: exported fields only, json:"-" skipped, names from the json
// tag, and untagged embedded structs flattened.
func structFields(t reflect.Type) ([]fieldInfo, error) {
	if c, ok := fieldsCache.Load(t); ok {
		res := c.(fieldsResult)
		return res.fields, res.err
	}
	fields, err := walkFields(t, nil)
	if err != nil {
		fields = nil
	}
	fieldsCache.Store(t, fieldsResult{fields, err})
	return fields, err
}

func walkFields(t reflect.Type, prefix []int) ([]fieldInfo, error) {
	var out []fieldInfo
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		index := append(append([]int(nil), prefix...), i)

		if f.Anonymous && name == "" {
			et := f.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				sub, err := walkFields(et, index)
				if err != nil {
					return nil, err
				}
				out = append(out, sub...)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		r, err := parseRules(f)
		if err != nil {
			return nil, err
		}
		out = append(out, fieldInfo{
			Index:     index,
			Name:      name,
			OmitEmpty: hasOpt(opts, "omitempty"),
			Field:     f,
			Rules:     r,
		})
	}
	return out, nil
}

func hasOpt(opts, want string) bool {
	for _, o := range strings.Split(opts, ",") {
		if o == want {
			return true
		}
	}
	return false
}

var textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()

// parseScalar converts s into a value of type t. Types whose pointer
// implements encoding.TextUnmarshaler are handled first, then strings, bools,
// integers, floats and pointers to any of these.
func parseScalar(s string, t reflect.Type) (reflect.Value, error) {
	if reflect.PointerTo(t).Implements(textUnmarshalerType) {
		p := reflect.New(t)
		if err := p.Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s)); err != nil {
			return reflect.Value{}, err
		}
		return p.Elem(), nil
	}
	if t.Kind() == reflect.Pointer {
		inner, err := parseScalar(s, t.Elem())
		if err != nil {
			return reflect.Value{}, err
		}
		p := reflect.New(t.Elem())
		p.Elem().Set(inner)
		return p, nil
	}
	v := reflect.New(t).Elem()
	switch t.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return reflect.Value{}, err
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, t.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, t.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(s, t.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		v.SetFloat(n)
	default:
		return reflect.Value{}, fmt.Errorf("unsupported type %s", t)
	}
	return v, nil
}
