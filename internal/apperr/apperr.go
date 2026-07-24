// Package apperr defines the exit-code taxonomy the PRD mandates and a small
// typed error that carries a code up to main.
//
// Exit codes: 0 ok / 1 auth / 2 API / 3 partial. main() maps a returned error
// to one of these via Code().
package apperr

import "errors"

// Exit codes.
const (
	CodeOK      = 0
	CodeAuth    = 1
	CodeAPI     = 2
	CodePartial = 3
)

// Error wraps an underlying error with an exit code.
type Error struct {
	Code int
	Err  error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return "error"
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// Auth marks an authentication failure (exit 1) — the caller should run `auth`.
func Auth(err error) error { return &Error{Code: CodeAuth, Err: err} }

// API marks an upstream Spotify/Last.fm API failure (exit 2).
func API(err error) error { return &Error{Code: CodeAPI, Err: err} }

// Partial marks a run that persisted some progress but did not fully complete
// (exit 3).
func Partial(err error) error { return &Error{Code: CodePartial, Err: err} }

// Code extracts the exit code for an error, defaulting to CodeAPI for unknown
// non-nil errors and CodeOK for nil.
func Code(err error) int {
	if err == nil {
		return CodeOK
	}
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return CodeAPI
}
