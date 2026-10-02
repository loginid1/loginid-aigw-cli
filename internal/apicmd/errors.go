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

// appError is the API's error body.
type appError struct {
	Msg     string `json:"msg"`
	MsgCode string `json:"msgCode"`
}

func responseError(op *Operation, status int, body []byte) error {
	msg := strings.TrimSpace(string(body))

	var ae appError
	if json.Unmarshal(body, &ae) == nil && ae.Msg != "" {
		msg = ae.Msg
		if ae.MsgCode != "" {
			msg = ae.MsgCode + ": " + msg
		}
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
