# typed

Typed handlers and automatic OpenAPI 3.1 docs for the [chi](https://github.com/go-chi/chi) router. The handler's input and output types are the documentation: no annotations, no code generation.

## Install

```
go get github.com/Brihith/typed
```

## Quick start

Imports omitted; see [examples/users](examples/users) for the full program.

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
	api := typed.New(r, typed.Config{Title: "Users API", Version: "1.0.0"})

	typed.Get(api, "/users/{id}", getUser, typed.Summary("Get a user"), typed.Tags("users"))
	typed.Post(api, "/users", createUser, typed.Status(http.StatusCreated), typed.Tags("users"))

	http.ListenAndServe(":8080", r) // also serves /openapi.json and /docs
}

func getUser(ctx context.Context, in *GetUserInput) (*User, error) {
	if in.ID != 1 {
		return nil, typed.NotFound("user not found")
	}
	return &User{ID: 1, Name: "Ada", Email: "ada@example.com"}, nil
}

func createUser(ctx context.Context, in *CreateUserInput) (*User, error) {
	// in.Body is decoded and validated; Role defaults to "member".
	return &User{ID: 2, Name: in.Body.Name, Email: in.Body.Email}, nil
}
```

A complete runnable version is in [examples/users](examples/users). Run it and open http://localhost:8080/docs.

Handlers have the form `func(ctx context.Context, in *In) (*Out, error)`. `Out` is a struct, or a slice for a JSON array response (a nil slice is written as `[]`). Use `struct{}` as `Out` for an empty 204 response.

## Input tags

Each exported field of `In` is read from one source.

| Tag / name      | Source                    | Notes                                              |
|-----------------|---------------------------|----------------------------------------------------|
| `path:"name"`   | URL path parameter        | always required                                    |
| `query:"name"`  | query string              | slices read repeated keys (`?tag=a&tag=b`)         |
| `header:"Name"` | request header            |                                                    |
| field `Body`    | JSON request body         | any JSON-encodable type                            |

Parameter types: `string`, `bool`, integers, floats, slices of those (query only), and anything implementing `encoding.TextUnmarshaler`. A pointer parameter is `nil` when absent.

Untagged embedded structs (not pointers) are flattened, so shared parameters such as pagination can live in their own struct. Any other exported field without one of these tags is a registration error.

## Validation tags

Tags apply to body fields and parameters. Each one validates the request and appears in the generated schema.

| Tag                       | Applies to          | Meaning                                              |
|---------------------------|---------------------|------------------------------------------------------|
| `required:"true"`         | any                 | non-zero value (non-nil pointer, non-empty string or slice) |
| `minLength` / `maxLength` | string              | rune count                                           |
| `pattern`                 | string              | Go regexp, compiled at registration                  |
| `format`                  | string              | `email`, `uuid`, `date-time`, `date`, `uri` are validated; others are documentation only |
| `enum:"a,b,c"`            | string, numbers     | allowed values                                       |
| `minimum` / `maximum`     | numbers             | inclusive bounds                                     |
| `minItems` / `maxItems`   | slices              | length bounds                                        |
| `default`                 | params, body fields | applied when absent or zero (slices: comma-separated list); shown in docs |
| `doc`                     | any                 | description in docs                                  |
| `example`                 | any                 | example in docs                                      |

Because Go cannot tell "missing" from "zero", `required` means non-zero. For non-pointer fields the zero value (`0`, `""`, `false`, an empty slice) counts as "not sent" and skips all rules. Use a pointer (`*int`, `*string`) to enforce rules on zero values or to require presence. A non-pointer nested struct is always validated; use `*Nested` for an optional object. For custom checks, implement `Validate() error` on the input type; return `typed.FieldError(location, message)` to control the response.

## Errors

Errors are RFC 9457 Problem Details with `Content-Type: application/problem+json`. All validation failures in a request are returned together:

```json
{
  "type": "about:blank",
  "title": "Unprocessable Entity",
  "status": 422,
  "detail": "validation failed",
  "errors": [{ "location": "body.email", "message": "must be a valid email", "value": "nope" }]
}
```

Malformed JSON and wrong content types give 400, oversized bodies 413, unparsable parameters and validation failures 422. A handler returning a `*typed.Error` (`typed.NotFound`, `typed.Conflict`, ...) controls the status; any other error becomes a 500 with a generic message, and the original is logged.

To change the error format, set `Config.ErrorHandler`:

```go
typed.New(r, typed.Config{
	Title:   "Users API",
	Version: "1.0.0",
	ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
		// write your own response
	},
})
```

## Sub-routers and middleware

```go
api.Route("/admin", func(admin *typed.API) {
	admin = admin.With(requireAdmin) // any chi middleware
	typed.Delete(admin, "/users/{id}", deleteUser)
})
```

Routes registered under `Route` appear in the spec with the prefix. Plain chi routes keep working next to typed ones; they are just not documented. `api.OpenAPI()` returns the spec, for example to write it to a file in CI.

## Status

Beta. The API may change before v1.

## License

MIT. See [LICENSE](LICENSE).
