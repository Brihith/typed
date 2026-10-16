package typed

import (
	"encoding"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"
)

var (
	timeType          = reflect.TypeOf(time.Time{})
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
)

// registry turns Go types into schemas, collecting named structs as
// reusable components.
type registry struct {
	schemas map[string]*Schema
	names   map[reflect.Type]string
	owners  map[string]reflect.Type
}

func newRegistry() *registry {
	return &registry{
		schemas: map[string]*Schema{},
		names:   map[reflect.Type]string{},
		owners:  map[string]reflect.Type{},
	}
}

// components returns the registered named schemas.
func (g *registry) components() map[string]*Schema { return g.schemas }

// setName forces the components name of the named struct t. It must be
// called before the first schemaFor(t).
func (g *registry) setName(t reflect.Type, name string) {
	g.names[t] = name
	g.owners[name] = t
}

var (
	qualifier = regexp.MustCompile(`[\w./~-]+\.`)
	nonAlnum  = regexp.MustCompile(`[^A-Za-z0-9]+`)
)

// nameFor picks the components name for t, resolving a collision with a
// different type by prefixing the package name.
func (g *registry) nameFor(t reflect.Type) (string, error) {
	name := t.Name()
	if base, args, ok := strings.Cut(name, "["); ok {
		args = qualifier.ReplaceAllString(args, "")
		name = nonAlnum.ReplaceAllString(base+args, "")
	}
	if o, taken := g.owners[name]; taken && o != t {
		pkg := t.PkgPath()
		pkg = nonAlnum.ReplaceAllString(pkg[strings.LastIndex(pkg, "/")+1:], "")
		if pkg != "" {
			pkg = strings.ToUpper(pkg[:1]) + pkg[1:]
		}
		name = pkg + name
		if o, taken := g.owners[name]; taken && o != t {
			return "", fmt.Errorf("typed: schema name %q is used by more than one type", name)
		}
	}
	return name, nil
}

// schemaFor returns the schema describing t.
func (g *registry) schemaFor(t reflect.Type) (*Schema, error) {
	if t == timeType {
		return &Schema{Type: "string", Format: "date-time"}, nil
	}
	if k := t.Kind(); k != reflect.Pointer && k != reflect.Interface &&
		(t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType)) {
		// encoding/json writes these as JSON strings.
		return &Schema{Type: "string"}, nil
	}
	switch t.Kind() {
	case reflect.Bool:
		return &Schema{Type: "boolean"}, nil
	case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint64:
		return &Schema{Type: "integer", Format: "int64"}, nil
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return &Schema{Type: "integer", Format: "int32"}, nil
	case reflect.Float32, reflect.Float64:
		return &Schema{Type: "number"}, nil
	case reflect.String:
		return &Schema{Type: "string"}, nil
	case reflect.Interface:
		return &Schema{}, nil
	case reflect.Pointer:
		return g.pointerSchema(t)
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return &Schema{Type: "string", ContentEncoding: "base64"}, nil
		}
		items, err := g.schemaFor(t.Elem())
		if err != nil {
			return nil, err
		}
		return &Schema{Type: "array", Items: items}, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("typed: unsupported type %s", t)
		}
		v, err := g.schemaFor(t.Elem())
		if err != nil {
			return nil, err
		}
		return &Schema{Type: "object", AdditionalProperties: v}, nil
	case reflect.Struct:
		return g.structSchema(t)
	}
	return nil, fmt.Errorf("typed: unsupported type %s", t)
}

func (g *registry) pointerSchema(t reflect.Type) (*Schema, error) {
	s, err := g.schemaFor(t.Elem())
	if err != nil {
		return nil, err
	}
	if s.Ref != "" {
		return &Schema{AnyOf: []*Schema{s, {Type: "null"}}}, nil
	}
	if typ, ok := s.Type.(string); ok {
		s.Type = []string{typ, "null"}
	}
	return s, nil
}

func (g *registry) structSchema(t reflect.Type) (*Schema, error) {
	if t.Name() == "" {
		return g.objectSchema(t)
	}
	name, ok := g.names[t]
	if !ok {
		var err error
		if name, err = g.nameFor(t); err != nil {
			return nil, err
		}
		g.setName(t, name)
	}
	ref := &Schema{Ref: "#/components/schemas/" + name}
	if _, done := g.schemas[name]; done {
		return ref, nil
	}
	// Reserve the entry before walking fields so recursive types terminate.
	g.schemas[name] = &Schema{Type: "object"}
	obj, err := g.objectSchema(t)
	if err != nil {
		delete(g.schemas, name)
		return nil, err
	}
	g.schemas[name] = obj
	return ref, nil
}

func (g *registry) objectSchema(t reflect.Type) (*Schema, error) {
	fields, err := structFields(t)
	if err != nil {
		return nil, err
	}
	obj := &Schema{Type: "object"}
	for _, f := range fields {
		fs, err := g.schemaFor(f.Field.Type)
		if err != nil {
			return nil, err
		}
		applyRules(fs, f.Rules, f.Field.Type)
		if obj.Properties == nil {
			obj.Properties = map[string]*Schema{}
		}
		obj.Properties[f.Name] = fs
		if f.Rules.Required {
			obj.Required = append(obj.Required, f.Name)
		}
	}
	return obj, nil
}

// applyRules copies the constraints in r onto s. Enum, default and example
// values are converted to t so numbers and booleans render as JSON values.
// For a reference or nullable-reference schema only the description applies.
func applyRules(s *Schema, r rules, t reflect.Type) {
	s.Description = r.Doc
	if s.Ref != "" || len(s.AnyOf) > 0 {
		return
	}
	s.MinItems, s.MaxItems = r.MinItems, r.MaxItems
	if s.Items != nil {
		// Element-level constraints describe the items of an array.
		et := t
		for et.Kind() == reflect.Pointer {
			et = et.Elem()
		}
		applyRules(s.Items, elementRules(r), et.Elem())
	} else {
		s.MinLength, s.MaxLength = r.MinLength, r.MaxLength
		s.Minimum, s.Maximum = r.Minimum, r.Maximum
		if r.Pattern != nil {
			s.Pattern = r.Pattern.String()
		}
		if r.Format != "" {
			s.Format = r.Format
		}
		for _, e := range r.Enum {
			s.Enum = append(s.Enum, typedValue(e, t))
		}
	}
	if r.HasDefault {
		s.Default = typedValue(r.Default, t)
	}
	if r.Example != "" {
		s.Examples = []any{typedValue(r.Example, t)}
	}
}

// typedValue parses str as t, falling back to the raw string when t is not a
// plain scalar or the text does not parse.
func typedValue(str string, t reflect.Type) any {
	v, err := parseScalar(str, t)
	if err != nil {
		return str
	}
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return v.Interface()
	}
	return str
}
