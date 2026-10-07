// Package client sends requests to the devcon API.
//
// It is the request layer, in the place pkg/requests holds in the Stripe CLI:
// it knows URLs, auth, JSON and the API's error bodies, and nothing about how
// the inputs arrived. The cobra commands in internal/apicmd fill a Request from
// flags; any other front end, such as an MCP server, fills it from its own
// input and gets the same behavior.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/config"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/credentials"
)

// EnvAPIKey names the env var that supplies a key without `aigw login`.
const EnvAPIKey = "AIGW_API_KEY" //nolint:gosec // the name of an env var, not a credential

const requestTimeout = 30 * time.Second

// ErrNotLoggedIn means no key was given and none is saved.
var ErrNotLoggedIn = errors.New("not logged in")

// Client calls one API base URL with one key.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// New returns a client for base, authenticating with apiKey.
func New(base, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimSuffix(base, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: requestTimeout},
	}
}

// ResolveAPIKey returns explicit when set, else $AIGW_API_KEY, else the key
// `aigw login` saved. It returns ErrNotLoggedIn when there is none.
func ResolveAPIKey(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}

	if v := os.Getenv(EnvAPIKey); v != "" {
		return v, nil
	}

	dir, err := config.Dir()
	if err != nil {
		return "", err
	}

	key, err := credentials.Load(dir, config.DefaultProfile)
	if errors.Is(err, credentials.ErrNotLoggedIn) {
		return "", ErrNotLoggedIn
	}

	return key, err
}

// Request is one API call, described independently of where its inputs came
// from.
type Request struct {
	Method string
	// Path is the URL template from the spec, e.g. "/projects/{projectId}".
	Path string
	// PathParams fills the template. Values are escaped here.
	PathParams map[string]string
	Query      url.Values
	// Body is sent as JSON when non-nil.
	Body any
	// Scope is the scope the operation needs, named in a 403 error.
	Scope string
}

// Do sends req and returns the response body, which is empty for a 204. A
// status of 400 or above comes back as an *APIError.
func (c *Client) Do(ctx context.Context, req *Request) ([]byte, error) {
	u, err := c.url(req)
	if err != nil {
		return nil, err
	}

	var body io.Reader

	if req.Body != nil {
		raw, encErr := json.Marshal(req.Body)
		if encErr != nil {
			return nil, fmt.Errorf("encode request body: %w", encErr)
		}

		body = bytes.NewReader(raw)
	}

	// TEMP: c.BaseURL can come from AIGW_TEMP_API_BASE; see apicmd.AddAll.
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, u, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", req.Method, req.Path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, newAPIError(req, resp.StatusCode, respBody)
	}

	return respBody, nil
}

func (c *Client) url(req *Request) (string, error) {
	path := req.Path

	for name, value := range req.PathParams {
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(value))
	}

	if i := strings.Index(path, "{"); i >= 0 {
		return "", fmt.Errorf("%s %s: path parameter %s has no value", req.Method, req.Path, path[i:])
	}

	u := c.BaseURL + path
	if len(req.Query) > 0 {
		u += "?" + req.Query.Encode()
	}

	return u, nil
}
