package typed

import (
	"net/http"
	"reflect"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
)

// Config configures an API.
type Config struct {
	// Title and Version are required and appear in the OpenAPI info object.
	Title       string
	Version     string
	Description string
	// SpecPath serves the OpenAPI document (default "/openapi.json").
	// DocsPath serves the documentation UI (default "/docs"). "-" disables
	// either.
	SpecPath string
	DocsPath string
	// MaxBodyBytes limits request bodies (default 1 MiB).
	MaxBodyBytes int64
	// ErrorHandler replaces the default problem+json error writer.
	ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)
}

// API registers typed routes on a chi router and builds the OpenAPI document.
type API struct {
	router chi.Router
	prefix string
	s      *state
}

// state is shared by an API and all of its sub-APIs.
type state struct {
	cfg  Config
	spec *OpenAPI
	reg  *registry
	ops  map[string]bool // keys are "METHOD path"
	mu   sync.Mutex
}

// New returns an API that registers its routes on r. It panics when the
// configuration is invalid.
func New(r chi.Router, cfg Config) *API {
	if cfg.Title == "" {
		panic("typed: Config.Title is required")
	}
	if cfg.Version == "" {
		panic("typed: Config.Version is required")
	}
	if cfg.SpecPath == "" {
		cfg.SpecPath = "/openapi.json"
	}
	if cfg.DocsPath == "" {
		cfg.DocsPath = "/docs"
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 1 << 20
	}

	reg := newRegistry()
	errType := reflect.TypeFor[Error]()
	reg.setName(errType, "ErrorModel")
	if _, err := reg.schemaFor(errType); err != nil {
		panic("typed: " + err.Error())
	}

	a := &API{
		router: r,
		s: &state{
			cfg: cfg,
			spec: &OpenAPI{
				OpenAPI: "3.1.0",
				Info:    Info{Title: cfg.Title, Version: cfg.Version, Description: cfg.Description},
				Paths:   map[string]*PathItem{},
			},
			reg: reg,
			ops: map[string]bool{},
		},
	}
	mountDocs(a)
	return a
}

// Router returns the underlying chi router.
func (a *API) Router() chi.Router { return a.router }

// OpenAPI returns the document describing the routes registered so far.
func (a *API) OpenAPI() *OpenAPI {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	if a.s.spec.Components == nil {
		a.s.spec.Components = &Components{}
	}
	a.s.spec.Components.Schemas = a.s.reg.components()
	return a.s.spec
}

// Route mounts a sub-router at pattern and passes fn an API for it. Routes
// registered on sub appear in the document under pattern.
func (a *API) Route(pattern string, fn func(sub *API)) {
	a.router.Route(pattern, func(r chi.Router) {
		fn(&API{router: r, prefix: strings.TrimSuffix(a.prefix+pattern, "/"), s: a.s})
	})
}

// With returns an API whose routes run behind the given middleware.
func (a *API) With(mw ...func(http.Handler) http.Handler) *API {
	return &API{router: a.router.With(mw...), prefix: a.prefix, s: a.s}
}

func (a *API) handleError(w http.ResponseWriter, r *http.Request, err error) {
	if a.s.cfg.ErrorHandler != nil {
		a.s.cfg.ErrorHandler(w, r, err)
		return
	}
	writeError(w, r, err)
}
