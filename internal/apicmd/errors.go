package apicmd

import (
	"errors"
	"fmt"
	"net/http"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/client"
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

// exitError gives err the exit code the CLI's conventions assign it: 3 when
// there is no usable key or the API rejects it, 4 when the key lacks a scope.
func exitError(err error) error {
	if errors.Is(err, client.ErrNotLoggedIn) {
		return &ExitError{Code: ExitNotAuthenticated, Err: fmt.Errorf(
			"not logged in: run `aigw login`, pass --%s, or set %s", flagAPIKey, client.EnvAPIKey)}
	}

	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusUnauthorized:
			return &ExitError{Code: ExitNotAuthenticated, Err: err}
		case http.StatusForbidden:
			return &ExitError{Code: ExitMissingScope, Err: err}
		}
	}

	return err
}
