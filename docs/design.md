# typed — design spec (v0.1)

Date: 2026-10-16

## Goal

`typed` is an open-source Go library that layers typed handlers and automatic
OpenAPI 3.1 documentation on top of the chi router, in the spirit of FastAPI:
the handler's input and output types *are* the documentation. No annotations,
no comment parsing, no code generation step.

Audience: Go developers already using (or happy to use) chi. Released as a
public beta (`v0.x`); the author also uses it in their own APIs.

### Success criteria for v0.1

1. A developer can `go get` the module, write the "Users API" example below,
   run it, and see accurate docs at `/docs` with a working "Try it out".
2. Request decoding, validation and error responses need zero hand-written
   code for the common cases.
3. Renaming a field or changing a validation tag updates both runtime behavior
   and the docs, because both come from the same struct.
4. Plain chi routes and middleware keep working alongside typed routes.
5. Only dependency besides the standard library: `github.com/go-chi/chi/v5`.

## Developer experience

```go
type GetUserInput struct {
    ID      int  `path:"id" minimum:"1"`
    Verbose bool `query:"verbose" doc:"Include extra fields"`
}

type CreateUserInput struct {
    Body struct {
        Name  string `json:"name" required:"true" minLength:"1" maxLength:"100"`
        Email string `json:"email" required:"true" format:"email"`
        Role  string `json:"role,omitempty" enum:"admin,member" default:"member"`
    }
}

type User struct {
    ID    int    `json:"id"`
    Name  string `json:"name"`
    Email string `json:"email" example:"ada@example.com"`
}

func main() {
    r := chi.NewRouter()
    r.Use(middleware.Logger)

    api := typed.New(r, typed.Config{Title: "Users API", Version: "1.0.0"})

    typed.Get(api, "/users/{id}", getUser, typed.Summary("Get a user"), typed.Tags("users"))
    typed.Post(api, "/users", createUser, typed.Status(http.StatusCreated), typed.Tags("users"))

    api.Route("/admin", func(admin *typed.API) {
        admin = admin.With(requireAdmin) // chi middleware
        typed.Delete(admin, "/users/{id}", deleteUser)
    })

    http.ListenAndServe(":8080", r) // also serves /openapi.json and /docs
}

func getUser(ctx context.Context, in *GetUserInput) (*User, error) {
    if in.ID != 1 {
        return nil, typed.NotFound("user not found")
    }
    return &User{ID: 1, Name: "Ada", Email: "ada@example.com"}, nil
}
```

## Public API

### Construction

```go
type Config struct {
    Title        string // required; OpenAPI info.title
    Version      string // required; OpenAPI info.version
    Description  string // optional; OpenAPI info.description
    SpecPath     string // default "/openapi.json"; "-" disables
    DocsPath     string // default "/docs"; "-" disables
    MaxBodyBytes int64  // default 1 MiB
    ErrorHandler func(w http.ResponseWriter, r *http.Request, err error) // default writes Problem Details
}

func New(r chi.Router, cfg Config) *API
func (a *API) OpenAPI() *OpenAPI          // the built spec, e.g. to write to a file in CI
func (a *API) Router() chi.Router         // the underlying chi router for this API/sub-API
```

`New` panics if `Title` or `Version` is empty (programmer error at startup).

### Route registration

```go
type Handler[In, Out any] func(ctx context.Context, in *In) (*Out, error)

func Get[In, Out any](api *API, path string, h Handler[In, Out], opts ...RouteOption)
func Post[In, Out any](api *API, path string, h Handler[In, Out], opts ...RouteOption)
func Put[In, Out any](api *API, path string, h Handler[In, Out], opts ...RouteOption)
func Patch[In, Out any](api *API, path string, h Handler[In, Out], opts ...RouteOption)
func Delete[In, Out any](api *API, path string, h Handler[In, Out], opts ...RouteOption)
```

Package-level generic functions, because Go methods cannot have type
parameters.

Route options: `Status(code)`, `Summary(s)`, `Description(s)`, `Tags(...)`,
`OperationID(s)`, `Deprecated()`.

Defaults: status 200 (204 when `Out` is `struct{}`), summary empty, operation ID
derived from method + path (`get-users-by-id`).

Registration-time checks (panic with a clear message, since these are bugs
caught at startup):

- every `{param}` in the path has a matching `path:"param"` field, and vice
  versa;
- `In` / `Out` are struct types;
- unsupported field types in parameters;
- duplicate method + path.

### Sub-routers and middleware

```go
func (a *API) Route(pattern string, fn func(sub *API)) // wraps chi.Route, prefixes spec paths
func (a *API) With(mw ...func(http.Handler) http.Handler) *API // wraps chi.With
```

Sub-APIs share the parent's spec. Plain `r.Get(...)` routes on the chi router
still work; they just don't appear in the docs.

## Request input (`In`)

Each exported field of `In` is one of:

| Tag / name       | Source                         | Notes |
|------------------|--------------------------------|-------|
| `path:"name"`    | `chi.URLParam(r, "name")`      | always required |
| `query:"name"`   | `r.URL.Query()`                | slices read repeated keys (`?tag=a&tag=b`) |
| `header:"Name"`  | `r.Header.Get`                 | |
| field `Body`     | JSON request body              | any JSON-encodable type |

Supported parameter types: `string`, `bool`, all int/uint/float kinds, slices of
those (query only), and any type implementing `encoding.TextUnmarshaler`
(covers `time.Time`, UUID types). A pointer parameter is `nil` when absent.

Body decoding: `Content-Type` must be `application/json` (or absent); body size
limited to 1 MiB by default (`Config.MaxBodyBytes`); unknown fields are allowed.

`In` may be `struct{}` for handlers with no input.

## Validation

Tags apply to body fields (at any depth) and to parameters. Each tag both
validates and appears in the generated schema.

| Tag | Applies to | Meaning |
|---|---|---|
| `required:"true"` | any | non-zero value (non-nil for pointers, non-empty for strings/slices) |
| `minLength` / `maxLength` | string | rune count |
| `pattern` | string | Go `regexp` syntax, compiled at registration |
| `format` | string | `email`, `uuid`, `date-time`, `date`, `uri` are validated; others are documented only |
| `enum:"a,b,c"` | string, numbers | allowed values |
| `minimum` / `maximum` | numbers | inclusive bounds |
| `minItems` / `maxItems` | slices | length bounds |
| `default` | params, body fields | applied when the value is absent/zero; shown in docs |
| `doc` | any | description in docs |
| `example` | any | example in docs |

Note on `required`: because Go cannot distinguish "missing" from "zero" for
value types, `required` means non-zero. Fields where zero is a valid value
should use a pointer (`*int` with `required:"true"` means "must be present").
For non-pointer fields the zero value (0, "", false, empty slice) counts as
"not sent" and skips all rules, so use a pointer to enforce rules on zero
values. A non-pointer nested struct is always validated; use `*Nested` for an
optional object.

Custom validation: if `*In` implements `interface{ Validate() error }`, it is
called after the built-in checks pass. Returning a `*typed.Error` (for example
via `typed.FieldError(location, message)`) controls the response; any other
error becomes a 422 with the error's message.

All validation failures in one request are collected and returned together.

## Responses

- `Out` is encoded as the JSON response body with the route's status code and
  `Content-Type: application/json`.
- `Out` of `struct{}` writes no body (default status 204).
- Returning `(nil, nil)` for a non-empty `Out` is a server error (500).

## Errors

Default format is RFC 9457 Problem Details, `Content-Type:
application/problem+json`:

```json
{
  "type": "about:blank",
  "title": "Unprocessable Entity",
  "status": 422,
  "detail": "validation failed",
  "errors": [{ "location": "body.email", "message": "must be a valid email", "value": "nope" }]
}
```

```go
type Error struct {
    Status int           `json:"status"`
    Title  string        `json:"title"`
    Detail string        `json:"detail,omitempty"`
    Type   string        `json:"type"`
    Errors []ErrorDetail `json:"errors,omitempty"`
}
type ErrorDetail struct {
    Location string `json:"location"` // "path.id", "query.limit", "body.items[2].name"
    Message  string `json:"message"`
    Value    any    `json:"value,omitempty"`
}
func (e *Error) Error() string
```

Helpers: `NewError(status, detail)`, `BadRequest`, `Unauthorized`, `Forbidden`,
`NotFound`, `Conflict`, `UnprocessableEntity`, `InternalServerError`,
`FieldError(location, message)`.

Mapping:

| Situation | Status |
|---|---|
| malformed JSON, wrong content type | 400 |
| body too large | 413 |
| parameter can't be parsed (`?limit=abc`) | 422 |
| validation failure | 422 |
| handler returns `*typed.Error` | its status |
| handler returns any other error | 500, generic detail (original error logged via `log/slog`, not sent to client) |

`Config.ErrorHandler` replaces the writer for all of the above; it receives the
`*typed.Error` (or the raw handler error) and writes whatever shape the team
uses.

Every operation in the spec documents its success response plus a `default`
error response referencing the `ErrorModel` schema.

## OpenAPI generation

Output: OpenAPI 3.1.0 with JSON Schema 2020-12 schemas.

Go type → schema:

| Go | Schema |
|---|---|
| `bool` | `boolean` |
| int kinds | `integer` (`format: int32` / `int64`) |
| float kinds | `number` |
| `string` | `string` |
| `time.Time` | `string`, `format: date-time` |
| `[]byte` | `string`, `contentEncoding: base64` |
| slice/array | `array` with `items` |
| `map[string]T` | `object` with `additionalProperties` |
| pointer `*T` | schema of `T` plus `null` in `type` |
| named struct | `$ref: #/components/schemas/Name` |
| anonymous struct | inline `object` |
| `any` / interface | `{}` |

Struct handling: JSON tag names, `json:"-"` skipped, embedded structs flattened
like `encoding/json`, `required` list from `required:"true"`. Recursive types
work through `$ref`. Schema name collisions across packages are resolved by
prefixing the package name; generic instantiations are sanitized
(`Page[main.User]` → `PageUser`).

The spec is built during registration and marshaled once on first request to
`SpecPath`.

`DocsPath` serves a small HTML page loading Swagger UI from jsDelivr at a pinned
version, pointed at `SpecPath`.

## Package layout

```
typed/                     module root = package typed
├── go.mod                 module github.com/Brihith/typed, go 1.24
├── api.go                 Config, New, API, Route, With
├── register.go            Get/Post/Put/Patch/Delete, route options
├── binding.go             fill In from path/query/header/body
├── validate.go            tag rules, Validate() hook
├── schema.go              Go type → JSON Schema, components registry
├── openapi.go             OpenAPI document types
├── errors.go              Error, helpers, default error handler
├── docs.go                spec + Swagger UI handlers
├── *_test.go
└── examples/users/main.go the example from this spec
```


## Testing

- Unit tests per file: schema generation for each type mapping; binding for
  each source and type; every validation tag, pass and fail.
- Integration tests using `httptest` against a real chi router: success,
  400/413/422/500 paths, custom `Validate()`, custom `ErrorHandler`, sub-routers
  and middleware.
- Golden test: the example API's generated `openapi.json` compared to a
  checked-in file.
- The spec is checked to be valid OpenAPI 3.1 in the golden test (structure
  assertions; no external validator dependency).
- `go vet` and `go test -race` clean.

## Out of scope for v0.1

Planned for later releases, not built now:

- security schemes / auth documentation
- multipart forms and file uploads; non-JSON request or response bodies
- response headers and multiple success status codes per route
- cookie parameters
- streaming / SSE
- OpenAPI 3.0 output
- documenting plain chi routes
