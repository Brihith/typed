package typed

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// Error is an RFC 9457 problem details response.
type Error struct {
	Type   string        `json:"type"`
	Title  string        `json:"title"`
	Status int           `json:"status"`
	Detail string        `json:"detail,omitempty"`
	Errors []ErrorDetail `json:"errors,omitempty"`
}

// ErrorDetail describes a single problem, such as one invalid field.
type ErrorDetail struct {
	Location string `json:"location"`
	Message  string `json:"message"`
	Value    any    `json:"value,omitempty"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("%d %s: %s", e.Status, e.Title, e.Detail)
}

// NewError returns a problem with the given HTTP status and detail.
func NewError(status int, detail string) *Error {
	return &Error{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	}
}

// BadRequest returns a 400 error.
func BadRequest(detail string) *Error { return NewError(http.StatusBadRequest, detail) }

// Unauthorized returns a 401 error.
func Unauthorized(detail string) *Error { return NewError(http.StatusUnauthorized, detail) }

// Forbidden returns a 403 error.
func Forbidden(detail string) *Error { return NewError(http.StatusForbidden, detail) }

// NotFound returns a 404 error.
func NotFound(detail string) *Error { return NewError(http.StatusNotFound, detail) }

// Conflict returns a 409 error.
func Conflict(detail string) *Error { return NewError(http.StatusConflict, detail) }

// UnprocessableEntity returns a 422 error.
func UnprocessableEntity(detail string) *Error {
	return NewError(http.StatusUnprocessableEntity, detail)
}

// InternalServerError returns a 500 error.
func InternalServerError(detail string) *Error {
	return NewError(http.StatusInternalServerError, detail)
}

// FieldError returns a 422 validation error for a single field.
func FieldError(location, message string) *Error {
	e := UnprocessableEntity("validation failed")
	e.Errors = []ErrorDetail{{Location: location, Message: message}}
	return e
}

// writeError is the default error writer. A *Error is written with missing
// fields filled in (an invalid status becomes 500); any other error, including
// a nil *Error, is logged and reported as a generic 500.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var pe *Error
	if !errors.As(err, &pe) || pe == nil {
		slog.ErrorContext(r.Context(), "typed: handler error",
			"err", err, "method", r.Method, "path", r.URL.Path)
		pe = InternalServerError("internal server error")
	}
	out := *pe
	if out.Status < 100 || out.Status > 599 {
		out.Status = http.StatusInternalServerError
	}
	if out.Type == "" {
		out.Type = "about:blank"
	}
	if out.Title == "" {
		out.Title = http.StatusText(out.Status)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(out.Status)
	_ = json.NewEncoder(w).Encode(&out)
}
