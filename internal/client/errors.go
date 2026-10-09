package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

// APIError is a response with a status of 300 or above. Redirects count: the
// client never follows them.
type APIError struct {
	Method, Path string
	Status       int
	// Message is the server's "code: message", or the raw body when it is
	// neither error shape the API sends.
	Message string
	// Scope is the scope the operation needs. It is named on a 403, because
	// it is the one thing the user can act on.
	Scope string
}

func (e *APIError) Error() string {
	s := fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.Path, e.Status, e.Message)
	if e.MissingScope() && e.Scope != "" {
		s += " (requires scope " + e.Scope + ")"
	}

	return s
}

// MissingScope reports whether the API refused the key for lacking a scope.
// Not every 403 means that: the API also answers 403 for a project role that
// is too low, or a project of the wrong type, and naming a scope there would
// send the user to the wrong fix.
func (e *APIError) MissingScope() bool {
	return e.Status == http.StatusForbidden && strings.Contains(e.Message, missingScopeMessage)
}

// missingScopeMessage is how the devcon API words a scope refusal
// (loginid-aigw internal/api/rest/devcon/impl/auth.go).
const missingScopeMessage = "missing a required scope"

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

func newAPIError(req *Request, status int, body []byte) *APIError {
	msg := strings.TrimSpace(string(body))

	var eb errorBody
	if json.Unmarshal(body, &eb) == nil && eb.text() != "" {
		msg = eb.text()
	}

	msg = Printable(msg)
	if len(msg) > maxMessageBytes {
		msg = strings.ToValidUTF8(msg[:maxMessageBytes], "") + "…"
	}

	if msg == "" {
		msg = http.StatusText(status)
	}

	return &APIError{Method: req.Method, Path: req.Path, Status: status, Message: msg, Scope: req.Scope}
}

// maxMessageBytes bounds the server text an error carries, so an HTML error
// page does not fill the terminal.
const maxMessageBytes = 500

// Printable makes server text safe for a terminal on one line: control
// characters, including the ESC that starts a terminal escape sequence, are
// dropped, and line breaks become spaces. JSON decoding has already turned any
// \u001b in the body into a real ESC by this point.
func Printable(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, s)
}
