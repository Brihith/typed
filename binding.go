package typed

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"

	"github.com/go-chi/chi/v5"
)

// paramField is one In field bound from the path, query string or headers.
type paramField struct {
	Index []int
	Name  string
	In    string // "path", "query" or "header"
	Type  reflect.Type
	Rules rules
}

// inputPlan describes how to fill a handler's In struct from a request.
type inputPlan struct {
	Params    []paramField
	BodyIndex []int // nil when the input has no body
	BodyType  reflect.Type
}

var paramLocations = []string{"path", "query", "header"}

// newInputPlan inspects the In struct type t. Untagged embedded structs are
// flattened so their tagged fields become parameters.
func newInputPlan(t reflect.Type) (*inputPlan, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("typed: input type %s must be a struct", t)
	}
	p := &inputPlan{}
	if err := p.addFields(t, nil, map[string]bool{}); err != nil {
		return nil, err
	}
	return p, nil
}

// addFields adds the fields of struct type t, found at index prefix within
// the In struct, to p. Body is recognized only at the top level.
func (p *inputPlan) addFields(t reflect.Type, prefix []int, seen map[string]bool) error {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		f.Index = append(append([]int(nil), prefix...), i)
		if err := p.addField(f, prefix == nil, seen); err != nil {
			return err
		}
	}
	return nil
}

func (p *inputPlan) addField(f reflect.StructField, top bool, seen map[string]bool) error {
	hasTag := false
	for _, l := range paramLocations {
		if _, ok := f.Tag.Lookup(l); ok {
			hasTag = true
		}
	}
	isBody := top && f.Name == "Body"
	if f.Anonymous && !hasTag && !isBody {
		switch {
		case f.Type.Kind() == reflect.Pointer && f.Type.Elem().Kind() == reflect.Struct:
			return fmt.Errorf("typed: field %s: embedded pointer structs are not supported", f.Name)
		case f.Type.Kind() == reflect.Struct:
			return p.addFields(f.Type, f.Index, seen)
		}
	}
	if !f.IsExported() {
		return nil
	}
	var loc, name string
	for _, l := range paramLocations {
		v, ok := f.Tag.Lookup(l)
		if !ok {
			continue
		}
		if loc != "" {
			return fmt.Errorf("typed: field %s: tags %s and %s are mutually exclusive", f.Name, loc, l)
		}
		if v == "" {
			return fmt.Errorf("typed: field %s: empty %s tag", f.Name, l)
		}
		loc, name = l, v
	}
	if isBody {
		if loc != "" {
			return fmt.Errorf("typed: field Body cannot carry a %s tag", loc)
		}
		p.BodyIndex, p.BodyType = f.Index, f.Type
		return nil
	}
	if loc == "" {
		return fmt.Errorf("typed: field %s has no path, query or header tag", f.Name)
	}
	if !supportedParamType(f.Type, loc == "query") {
		return fmt.Errorf("typed: field %s: unsupported %s parameter type %s", f.Name, loc, f.Type)
	}
	if loc == "header" {
		name = http.CanonicalHeaderKey(name)
	}
	if key := loc + "." + name; seen[key] {
		return fmt.Errorf("typed: field %s: duplicate %s parameter %q", f.Name, loc, name)
	} else {
		seen[key] = true
	}
	r, err := parseRules(f)
	if err != nil {
		return err
	}
	p.Params = append(p.Params, paramField{Index: f.Index, Name: name, In: loc, Type: f.Type, Rules: r})
	return nil
}

// supportedParamType reports whether t can be parsed from a single string, or
// from several strings when slices is true.
func supportedParamType(t reflect.Type, slices bool) bool {
	if slices && t.Kind() == reflect.Slice {
		// []byte would be documented as base64 but bound as a list.
		return t.Elem().Kind() != reflect.Uint8 && supportedParamType(t.Elem(), false)
	}
	if reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return true
	}
	switch t.Kind() {
	case reflect.Pointer:
		return supportedParamType(t.Elem(), false)
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// PathParams returns the names of the path parameters.
func (p *inputPlan) PathParams() []string {
	var names []string
	for _, pf := range p.Params {
		if pf.In == "path" {
			names = append(names, pf.Name)
		}
	}
	return names
}

// bind fills dst, the addressable In struct, from r.
func (p *inputPlan) bind(r *http.Request, dst reflect.Value, maxBody int64) *Error {
	var parseErrs, valErrs []ErrorDetail
	present := make([]bool, len(p.Params))
	var query map[string][]string

	for i, pf := range p.Params {
		loc := pf.In + "." + pf.Name
		var raws []string
		switch pf.In {
		case "path":
			raws = []string{chi.URLParam(r, pf.Name)}
		case "query":
			if query == nil {
				query = r.URL.Query()
			}
			raws = query[pf.Name]
		case "header":
			raws = r.Header.Values(pf.Name)
		}
		fv := dst.FieldByIndex(pf.Index)
		switch {
		case len(raws) > 0:
			present[i] = true
		case pf.Rules.HasDefault:
			present[i] = true
			if pf.Type.Kind() == reflect.Slice {
				raws = strings.Split(pf.Rules.Default, ",")
			} else {
				raws = []string{pf.Rules.Default}
			}
		default:
			continue
		}
		if err := setParam(fv, raws, loc, &parseErrs); err != nil {
			present[i] = false
		}
	}
	if len(parseErrs) > 0 {
		return validationError(parseErrs)
	}

	if p.BodyIndex != nil {
		if err := p.readBody(r, dst.FieldByIndex(p.BodyIndex), maxBody); err != nil {
			return err
		}
		applyDefaults(dst.FieldByIndex(p.BodyIndex))
	}

	for i, pf := range p.Params {
		validatePresent(dst.FieldByIndex(pf.Index), pf.Rules, pf.In+"."+pf.Name, &valErrs, present[i])
	}
	if p.BodyIndex != nil {
		validatePresent(dst.FieldByIndex(p.BodyIndex), rules{}, "body", &valErrs, true)
	}
	if len(valErrs) > 0 {
		return validationError(valErrs)
	}
	return nil
}

func validationError(details []ErrorDetail) *Error {
	e := UnprocessableEntity("validation failed")
	e.Errors = details
	return e
}

// setParam parses raws into fv, appending a detail to errs for each value
// that does not parse. Scalars use the first value.
func setParam(fv reflect.Value, raws []string, loc string, errs *[]ErrorDetail) error {
	bad := func(raw string) error {
		*errs = append(*errs, ErrorDetail{Location: loc, Message: "invalid value", Value: raw})
		return errors.New("invalid value")
	}
	if fv.Kind() == reflect.Slice && !reflect.PointerTo(fv.Type()).Implements(textUnmarshalerType) {
		s := reflect.MakeSlice(fv.Type(), 0, len(raws))
		var failed error
		for _, raw := range raws {
			ev, err := parseScalar(raw, fv.Type().Elem())
			if err != nil {
				failed = bad(raw)
				continue
			}
			s = reflect.Append(s, ev)
		}
		if failed == nil {
			fv.Set(s)
		}
		return failed
	}
	v, err := parseScalar(raws[0], fv.Type())
	if err != nil {
		return bad(raws[0])
	}
	fv.Set(v)
	return nil
}

// readBody decodes the JSON request body into fv. An empty body leaves fv
// untouched.
func (p *inputPlan) readBody(r *http.Request, fv reflect.Value, maxBody int64) *Error {
	var data []byte
	if r.Body != nil {
		var err error
		data, err = io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBody))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				return NewError(http.StatusRequestEntityTooLarge, "request body too large")
			}
			return BadRequest("could not read request body: " + err.Error())
		}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil || !(mt == "application/json" || (strings.HasPrefix(mt, "application/") && strings.HasSuffix(mt, "+json"))) {
			return BadRequest("content type must be application/json")
		}
	}
	if err := json.Unmarshal(data, fv.Addr().Interface()); err != nil {
		return jsonError(err)
	}
	return nil
}

// jsonError describes a decode failure without exposing Go type names.
func jsonError(err error) *Error {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		return BadRequest("invalid JSON: " + syn.Error())
	case errors.As(err, &typ):
		loc := "body"
		if typ.Field != "" {
			loc += "." + typ.Field
		}
		return BadRequest(fmt.Sprintf("invalid JSON: %s must be %s", loc, jsonKind(typ.Type)))
	}
	return BadRequest("invalid JSON")
}

func jsonKind(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.String:
		return "string"
	case reflect.Slice, reflect.Array:
		return "array"
	}
	return "object"
}

// parameters returns the OpenAPI parameters of the input.
func (p *inputPlan) parameters(g *registry) ([]*Parameter, error) {
	var out []*Parameter
	for _, pf := range p.Params {
		t := pf.Type
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		s, err := g.schemaFor(t)
		if err != nil {
			return nil, fmt.Errorf("typed: parameter %s: %w", pf.Name, err)
		}
		applyRules(s, pf.Rules, t)
		out = append(out, &Parameter{
			Name:        pf.Name,
			In:          pf.In,
			Description: pf.Rules.Doc,
			Required:    pf.In == "path" || pf.Rules.Required,
			Schema:      s,
		})
	}
	return out, nil
}

// requestBody returns the OpenAPI request body, or nil when the input has none.
func (p *inputPlan) requestBody(g *registry) (*RequestBody, error) {
	if p.BodyIndex == nil {
		return nil, nil
	}
	s, err := g.schemaFor(p.BodyType)
	if err != nil {
		return nil, fmt.Errorf("typed: request body: %w", err)
	}
	return &RequestBody{
		Required: true,
		Content:  map[string]*MediaType{"application/json": {Schema: s}},
	}, nil
}
