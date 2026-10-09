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
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/config"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/credentials"
)

// Env vars that supply a key without `aigw login`.
const (
	// EnvAPIKey is used for the real API.
	EnvAPIKey = "AIGW_API_KEY" //nolint:gosec // the name of an env var, not a credential
	// EnvTempAPIKey is the only env var used when the base URL is overridden.
	EnvTempAPIKey = "AIGW_TEMP_API_KEY" //nolint:gosec // the name of an env var, not a credential
)

const (
	requestTimeout   = 30 * time.Second
	maxResponseBytes = 10 << 20
)

// ErrOutcomeUnknown marks a write that timed out after it was sent.
var ErrOutcomeUnknown = errors.New("the change may or may not have been applied: check its current state before retrying")

func isTimeout(err error) bool {
	var netErr net.Error

	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
}

// ErrBadKey means a key was given but cannot work; the error names its source.
var ErrBadKey = errors.New("unusable API key")

// checked trims a key from the command line or the environment, where a
// Windows file read with $(cat key.txt) leaves a trailing \r, and refuses one
// login would refuse, naming where it came from.
func checked(key, source string) (string, error) {
	key = strings.TrimSpace(key)

	err := credentials.CheckKey(key)
	if err != nil {
		return "", fmt.Errorf("%w from %s: %w", ErrBadKey, source, err)
	}

	return key, nil
}

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
	var transport http.RoundTripper
	if strings.HasPrefix(base, "http://") {
		transport = loopbackOnlyTransport()
	}

	return &Client{
		BaseURL: strings.TrimSuffix(base, "/"),
		APIKey:  apiKey,
		HTTP: &http.Client{
			Transport: transport,
			Timeout:   requestTimeout,
			// Never follow a redirect. Go would turn a POST into a GET on a
			// 302 and report the GET's answer as success, and it would send
			// the key along, even from https to http. The API does not
			// redirect, so a 3xx is an error (see Do).
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// ResolveAPIKey returns explicit when set. Otherwise, for the real API, it
// returns $AIGW_API_KEY, else the key `aigw login` saved; for a temporary base
// URL, only $AIGW_TEMP_API_KEY, so neither of the real keys can be sent there.
// It returns ErrNotLoggedIn when there is none.
func ResolveAPIKey(explicit string, temporaryBase bool) (string, error) {
	if explicit != "" {
		return checked(explicit, "--api-key")
	}

	if temporaryBase {
		if v := os.Getenv(EnvTempAPIKey); v != "" {
			return checked(v, EnvTempAPIKey)
		}

		return "", ErrNotLoggedIn
	}

	if v := os.Getenv(EnvAPIKey); v != "" {
		return checked(v, EnvAPIKey)
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

// Do sends req and returns the response body, which is empty for a 204. Any
// status of 300 or above comes back as an *APIError.
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
		// A write that timed out may still have been applied: the server can
		// finish after we stop waiting. Retrying blindly could create a
		// second project or signing key.
		if req.Method != http.MethodGet && isTimeout(err) {
			return nil, fmt.Errorf("%s %s: %w; %w", req.Method, req.Path, err, ErrOutcomeUnknown)
		}

		return nil, fmt.Errorf("%s %s: %w", req.Method, req.Path, err)
	}
	defer resp.Body.Close()

	// Bounded: an API answer is small, and under MCP the whole body goes into
	// the model's context.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if len(respBody) > maxResponseBytes {
		return nil, fmt.Errorf("%s %s: response is larger than %d MiB", req.Method, req.Path, maxResponseBytes>>20)
	}

	if resp.StatusCode >= http.StatusMultipleChoices {
		apiErr := newAPIError(req, resp.StatusCode, respBody)
		if loc := resp.Header.Get("Location"); resp.StatusCode < http.StatusBadRequest && loc != "" {
			apiErr.Message = "unexpected redirect to " + loc
		}

		return nil, apiErr
	}

	return respBody, nil
}

func (c *Client) url(req *Request) (string, error) {
	path := req.Path

	for name, value := range req.PathParams {
		err := checkPathValue(value)
		if err != nil {
			return "", fmt.Errorf("%s %s: %s: %w", req.Method, req.Path, name, err)
		}

		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(value))
	}

	if i := strings.Index(path, "{"); i >= 0 {
		// Name just this parameter, up to its closing brace, not the rest of
		// the path with any later parameters in it.
		name := path[i:]
		if j := strings.Index(name, "}"); j >= 0 {
			name = name[:j+1]
		}

		return "", fmt.Errorf("%s %s: path parameter %s has no value", req.Method, req.Path, name)
	}

	u := c.BaseURL + path
	if len(req.Query) > 0 {
		u += "?" + req.Query.Encode()
	}

	return u, nil
}

// checkPathValue refuses IDs that would change which URL a request reaches.
// PathEscape leaves "." and ".." alone, and a proxy that normalizes paths
// would turn DELETE /projects/p/resources/.. into DELETE /projects/p. An empty
// value leaves "//" in the path.
func checkPathValue(v string) error {
	switch v {
	case "":
		return errors.New("is empty")
	case ".", "..":
		return fmt.Errorf("%q is not a valid ID", v)
	}

	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%q contains a control character", v)
		}
	}

	return nil
}

// loopbackOnlyTransport is for plain-http bases, which only a temporary
// override can set. It dials only loopback addresses, whatever a hostname
// resolves to, so a DNS answer cannot carry the key off the machine in clear
// text, and it uses no proxy.
func loopbackOnlyTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // DefaultTransport is always one
	t.Proxy = nil

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}

		for _, ip := range ips {
			if ip.IP.IsLoopback() {
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			}
		}

		return nil, fmt.Errorf("refusing plain http to %s: it does not resolve to a loopback address", host)
	}

	return t
}
