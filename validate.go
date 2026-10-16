package typed

import (
	"errors"
	"net/mail"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// validator is implemented by request types that carry their own checks.
type validator interface{ Validate() error }

// runValidateHook calls in.Validate when in implements validator. A *Error is
// returned as-is (a nil *Error means no error); any other error becomes a 422
// problem.
func runValidateHook(in any) error {
	v, ok := in.(validator)
	if !ok && in != nil && reflect.TypeOf(in).Kind() != reflect.Pointer {
		// Validate may have a pointer receiver while in is a plain value.
		p := reflect.New(reflect.TypeOf(in))
		p.Elem().Set(reflect.ValueOf(in))
		v, ok = p.Interface().(validator)
	}
	if !ok {
		return nil
	}
	err := v.Validate()
	if err == nil {
		return nil
	}
	var pe *Error
	if errors.As(err, &pe) {
		if pe == nil {
			return nil
		}
		return pe
	}
	return UnprocessableEntity(err.Error())
}

// validateStruct checks every field of the struct v (or pointer to struct)
// and appends one detail per violation to errs. A nil embedded pointer leaves
// its promoted fields absent.
func validateStruct(v reflect.Value, loc string, errs *[]ErrorDetail) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	fields, err := structFields(v.Type())
	if err != nil {
		// Registration already validated the type, so this can't happen at request time.
		return
	}
	for _, f := range fields {
		fv, err := v.FieldByIndexErr(f.Index)
		if err != nil {
			continue
		}
		validateValue(fv, f.Rules, loc+"."+f.Name, errs)
	}
}

// validateValue checks v against r, then recurses into its contents.
func validateValue(v reflect.Value, r rules, loc string, errs *[]ErrorDetail) {
	validatePresent(v, r, loc, errs, false)
}

// validatePresent is validateValue for a value known to be present, as a
// slice element or map value is, so zero values are not skipped as absent.
func validatePresent(v reflect.Value, r rules, loc string, errs *[]ErrorDetail, present bool) {
	add := func(msg string, val any) {
		*errs = append(*errs, ErrorDetail{Location: loc, Message: msg, Value: val})
	}
	explicit := present // present, or behind a non-nil pointer: a zero value was still sent
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			if r.Required {
				add("is required", nil)
			}
			return
		}
		explicit = true
		v = v.Elem()
	}
	if !explicit && v.Kind() != reflect.Struct && v.IsZero() {
		if r.Required {
			add("is required", nil)
		}
		return
	}

	switch {
	case v.Type() == timeType:
		if r.Required && v.IsZero() {
			add("is required", nil)
		}
	case v.Kind() == reflect.Struct:
		if r.Required && !explicit && v.IsZero() {
			add("is required", nil)
			return
		}
		validateStruct(v, loc, errs)
	case v.Kind() == reflect.String:
		checkString(v, r, add)
	case isNumber(v.Kind()):
		checkNumber(v, r, add)
	case v.Kind() == reflect.Slice || v.Kind() == reflect.Array:
		n := v.Len()
		if r.MinItems != nil && n < *r.MinItems {
			add("must have at least "+strconv.Itoa(*r.MinItems)+" items", v.Interface())
		}
		if r.MaxItems != nil && n > *r.MaxItems {
			add("must have at most "+strconv.Itoa(*r.MaxItems)+" items", v.Interface())
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return
		}
		er := elementRules(r)
		for i := 0; i < n; i++ {
			validatePresent(v.Index(i), er, loc+"["+strconv.Itoa(i)+"]", errs, true)
		}
	case v.Kind() == reflect.Map:
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		for _, k := range keys {
			validatePresent(v.MapIndex(k), rules{}, loc+"."+k.String(), errs, true)
		}
	}
}

// elementRules extracts the constraints that apply to each slice element.
func elementRules(r rules) rules {
	return rules{
		MinLength: r.MinLength, MaxLength: r.MaxLength,
		Minimum: r.Minimum, Maximum: r.Maximum,
		Pattern: r.Pattern, Format: r.Format, Enum: r.Enum,
	}
}

func isNumber(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func checkString(v reflect.Value, r rules, add func(string, any)) {
	s := v.String()
	if r.MinLength != nil || r.MaxLength != nil {
		n := utf8.RuneCountInString(s)
		if r.MinLength != nil && n < *r.MinLength {
			add("must be at least "+strconv.Itoa(*r.MinLength)+" characters", v.Interface())
		}
		if r.MaxLength != nil && n > *r.MaxLength {
			add("must be at most "+strconv.Itoa(*r.MaxLength)+" characters", v.Interface())
		}
	}
	if r.Pattern != nil && !r.Pattern.MatchString(s) {
		add("must match pattern "+r.Pattern.String(), v.Interface())
	}
	if r.Format != "" && !validFormat(r.Format, s) {
		add("must be a valid "+r.Format, v.Interface())
	}
	checkEnum(v, s, r, add)
}

func checkNumber(v reflect.Value, r rules, add func(string, any)) {
	var f float64
	switch {
	case v.CanInt():
		f = float64(v.Int())
	case v.CanUint():
		f = float64(v.Uint())
	default:
		f = v.Float()
	}
	if r.Minimum != nil && f < *r.Minimum {
		add("must be >= "+formatFloat(*r.Minimum), v.Interface())
	}
	if r.Maximum != nil && f > *r.Maximum {
		add("must be <= "+formatFloat(*r.Maximum), v.Interface())
	}
	checkEnum(v, numberString(v), r, add)
}

// checkEnum reports a violation unless got equals one of r.Enum. Numeric
// entries are parsed into v's type so values are compared, not spellings.
func checkEnum(v reflect.Value, got string, r rules, add func(string, any)) {
	if len(r.Enum) == 0 {
		return
	}
	for _, e := range r.Enum {
		if v.Kind() == reflect.String {
			if e == got {
				return
			}
			continue
		}
		pv, err := parseScalar(e, v.Type())
		if err == nil && numberString(pv) == got {
			return
		}
	}
	add("must be one of: "+strings.Join(r.Enum, ", "), v.Interface())
}

func numberString(v reflect.Value) string {
	switch {
	case v.CanInt():
		return strconv.FormatInt(v.Int(), 10)
	case v.CanUint():
		return strconv.FormatUint(v.Uint(), 10)
	case v.CanFloat():
		return strconv.FormatFloat(v.Float(), 'f', -1, 64)
	}
	return ""
}

func formatFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func validFormat(format, s string) bool {
	switch format {
	case "email":
		a, err := mail.ParseAddress(s)
		return err == nil && a.Address == s
	case "uuid":
		return uuidPattern.MatchString(s)
	case "date-time":
		_, err := time.Parse(time.RFC3339, s)
		return err == nil
	case "date":
		_, err := time.Parse("2006-01-02", s)
		return err == nil
	case "uri":
		u, err := url.Parse(s)
		return err == nil && u.Scheme != ""
	}
	return true
}

// applyDefaults sets zero-valued fields that declare a default, recursing
// into nested structs. Nil pointers, including embedded ones, are left nil.
func applyDefaults(v reflect.Value) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct || v.Type() == timeType {
		return
	}
	fields, err := structFields(v.Type())
	if err != nil {
		// Registration already validated the type, so this can't happen at request time.
		return
	}
	for _, f := range fields {
		fv, err := v.FieldByIndexErr(f.Index)
		if err != nil || !fv.CanSet() {
			continue
		}
		if f.Rules.HasDefault && fv.IsZero() {
			if dv, err := parseScalar(f.Rules.Default, fv.Type()); err == nil {
				fv.Set(dv)
			}
		}
		applyDefaults(fv)
	}
}
