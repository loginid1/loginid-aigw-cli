package apicmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Exit codes, as the CLI's conventions fix them. 0 and 1 are left to main.
const (
	ExitNotAuthenticated = 3
	ExitMissingScope     = 4
)

// ExitError carries an exit code other than 1 up to main.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }

func (e *ExitError) Unwrap() error { return e.Err }

// errorBody reads both error shapes the API sends. Its own handlers answer
// with an AppError (msg, msgCode). Goa, which validates requests before any
// handler runs, answers with its default error (name, message), e.g. for a
// missing required field.
type errorBody struct {
	Msg     string `json:"msg"`
	MsgCode string `json:"msgCode"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// text renders the body as "code: message", or "" when it is neither shape.
func (e errorBody) text() string {
	code, msg := e.MsgCode, e.Msg
	if msg == "" {
		code, msg = e.Name, e.Message
	}

	if msg == "" {
		return ""
	}

	if code == "" {
		return msg
	}

	return code + ": " + msg
}

func responseError(op *Operation, status int, body []byte) error {
	msg := strings.TrimSpace(string(body))

	var eb errorBody
	if json.Unmarshal(body, &eb) == nil && eb.text() != "" {
		msg = eb.text()
	}

	if msg == "" {
		msg = http.StatusText(status)
	}

	err := fmt.Errorf("%s %s: HTTP %d: %s", op.Method, op.Path, status, msg)

	switch status {
	case http.StatusUnauthorized:
		return &ExitError{Code: ExitNotAuthenticated, Err: err}
	case http.StatusForbidden:
		// Name the scope, not a role: the scope is what the key must be
		// issued with, and it is the one thing the user can act on.
		return &ExitError{Code: ExitMissingScope, Err: fmt.Errorf("%w (this command needs scope %s)", err, op.Scope)}
	default:
		return err
	}
}
