package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	if e.Status == http.StatusForbidden && e.Scope != "" {
		s += " (requires scope " + e.Scope + ")"
	}

	return s
}

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

	if msg == "" {
		msg = http.StatusText(status)
	}

	return &APIError{Method: req.Method, Path: req.Path, Status: status, Message: msg, Scope: req.Scope}
}
