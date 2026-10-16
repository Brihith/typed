// Package typed registers typed handlers on a chi router and generates an
// OpenAPI 3.1 document from their input and output types. See the README and
// examples/users for usage. For non-pointer fields the zero value counts as
// "not sent" and skips validation rules; use a pointer to enforce rules on
// zero values or to require presence.
package typed
