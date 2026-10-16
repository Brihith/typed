package typed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Handler is a typed request handler.
type Handler[In, Out any] func(ctx context.Context, in *In) (*Out, error)

type routeConfig struct {
	status      int
	summary     string
	description string
	tags        []string
	operationID string
	deprecated  bool
}

// RouteOption customizes a route and its operation.
type RouteOption func(*routeConfig)

// Status sets the success status code.
func Status(code int) RouteOption { return func(c *routeConfig) { c.status = code } }

// Summary sets the operation summary.
func Summary(s string) RouteOption { return func(c *routeConfig) { c.summary = s } }

// Description sets the operation description.
func Description(s string) RouteOption { return func(c *routeConfig) { c.description = s } }

// Tags sets the operation tags.
func Tags(tags ...string) RouteOption { return func(c *routeConfig) { c.tags = tags } }

// OperationID overrides the generated operation ID.
func OperationID(id string) RouteOption { return func(c *routeConfig) { c.operationID = id } }

// Deprecated marks the operation as deprecated.
func Deprecated() RouteOption { return func(c *routeConfig) { c.deprecated = true } }

// Get registers a GET route.
func Get[In, Out any](api *API, path string, h Handler[In, Out], opts ...RouteOption) {
	register(api, http.MethodGet, path, h, opts)
}

// Post registers a POST route.
func Post[In, Out any](api *API, path string, h Handler[In, Out], opts ...RouteOption) {
	register(api, http.MethodPost, path, h, opts)
}

// Put registers a PUT route.
func Put[In, Out any](api *API, path string, h Handler[In, Out], opts ...RouteOption) {
	register(api, http.MethodPut, path, h, opts)
}

// Patch registers a PATCH route.
func Patch[In, Out any](api *API, path string, h Handler[In, Out], opts ...RouteOption) {
	register(api, http.MethodPatch, path, h, opts)
}

// Delete registers a DELETE route.
func Delete[In, Out any](api *API, path string, h Handler[In, Out], opts ...RouteOption) {
	register(api, http.MethodDelete, path, h, opts)
}

func register[In, Out any](api *API, method, path string, h Handler[In, Out], opts []RouteOption) {
	full := api.prefix + path
	fail := func(format string, args ...any) {
		panic(fmt.Sprintf("typed: %s %s: %s", method, full, fmt.Sprintf(format, args...)))
	}

	inType, outType := reflect.TypeFor[In](), reflect.TypeFor[Out]()
	if outType.Kind() != reflect.Struct {
		fail("output type %s must be a struct", outType)
	}
	plan, err := newInputPlan(inType)
	if err != nil {
		fail("%v", err)
	}
	patternParams, specSuffix, err := parsePattern(full)
	if err != nil {
		fail("%v", err)
	}
	planParams := plan.PathParams()
	slices.Sort(patternParams)
	slices.Sort(planParams)
	if !slices.Equal(patternParams, planParams) {
		fail("path parameters %v do not match input fields %v", patternParams, planParams)
	}

	noContent := outType == reflect.TypeFor[struct{}]()
	cfg := routeConfig{status: http.StatusOK}
	if noContent {
		cfg.status = http.StatusNoContent
	}
	for _, o := range opts {
		o(&cfg)
	}

	s := api.s
	specPath := specSuffix
	key := method + " " + specPath

	s.mu.Lock()
	if s.ops[key] {
		s.mu.Unlock()
		fail("duplicate route")
	}
	op, err := buildOperation(s.reg, plan, outType, noContent, &cfg, method, specPath)
	if err != nil {
		s.mu.Unlock()
		fail("%v", err)
	}
	s.ops[key] = true
	item := s.spec.Paths[specPath]
	if item == nil {
		item = &PathItem{}
		s.spec.Paths[specPath] = item
	}
	switch method {
	case http.MethodGet:
		item.Get = op
	case http.MethodPost:
		item.Post = op
	case http.MethodPut:
		item.Put = op
	case http.MethodPatch:
		item.Patch = op
	case http.MethodDelete:
		item.Delete = op
	}
	s.mu.Unlock()

	maxBody := s.cfg.MaxBodyBytes
	status := cfg.status
	api.router.MethodFunc(method, path, func(w http.ResponseWriter, r *http.Request) {
		in := new(In)
		if perr := plan.bind(r, reflect.ValueOf(in).Elem(), maxBody); perr != nil {
			api.handleError(w, r, perr)
			return
		}
		if err := runValidateHook(in); err != nil {
			api.handleError(w, r, err)
			return
		}
		out, err := h(r.Context(), in)
		if err != nil {
			api.handleError(w, r, err)
			return
		}
		if noContent {
			w.WriteHeader(status)
			return
		}
		if out == nil {
			api.handleError(w, r, InternalServerError("handler returned nil response"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(out)
	})
}

func buildOperation(reg *registry, plan *inputPlan, outType reflect.Type, noContent bool,
	cfg *routeConfig, method, specPath string) (*Operation, error) {
	params, err := plan.parameters(reg)
	if err != nil {
		return nil, err
	}
	body, err := plan.requestBody(reg)
	if err != nil {
		return nil, err
	}

	success := &Response{Description: http.StatusText(cfg.status)}
	if !noContent {
		sch, err := reg.schemaFor(outType)
		if err != nil {
			return nil, err
		}
		success.Content = map[string]*MediaType{"application/json": {Schema: sch}}
	}

	id := cfg.operationID
	if id == "" {
		id = defaultOperationID(method, specPath)
	}
	return &Operation{
		OperationID: id,
		Summary:     cfg.summary,
		Description: cfg.description,
		Tags:        cfg.tags,
		Deprecated:  cfg.deprecated,
		Parameters:  params,
		RequestBody: body,
		Responses: map[string]*Response{
			strconv.Itoa(cfg.status): success,
			"default": {
				Description: "Error",
				Content: map[string]*MediaType{
					"application/problem+json": {Schema: &Schema{Ref: "#/components/schemas/ErrorModel"}},
				},
			},
		},
	}, nil
}

// parsePattern returns the path parameter names of a chi pattern and the
// pattern with regular expressions removed from its parameters.
func parsePattern(p string) (names []string, spec string, err error) {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '*':
			return nil, "", fmt.Errorf("wildcard %q is not supported", "*")
		case '{':
			depth, j := 1, i+1
			for ; j < len(p) && depth > 0; j++ {
				switch p[j] {
				case '{':
					depth++
				case '}':
					depth--
				}
			}
			if depth != 0 {
				return nil, "", fmt.Errorf("unbalanced braces in path")
			}
			name, _, _ := strings.Cut(p[i+1:j-1], ":")
			names = append(names, name)
			b.WriteString("{" + name + "}")
			i = j - 1
		default:
			b.WriteByte(p[i])
		}
	}
	return names, b.String(), nil
}

func defaultOperationID(method, specPath string) string {
	parts := []string{strings.ToLower(method)}
	for _, seg := range strings.Split(specPath, "/") {
		if seg == "" {
			continue
		}
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			seg = "by-" + seg[1:len(seg)-1]
		}
		parts = append(parts, seg)
	}
	return strings.Join(parts, "-")
}
