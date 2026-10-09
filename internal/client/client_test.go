package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/client"
)

func TestDoSendsRequest(t *testing.T) {
	t.Parallel()

	var (
		gotPath, gotQuery, gotAuth, gotType string
		gotBody                             map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.EscapedPath(), r.URL.RawQuery
		gotAuth, gotType = r.Header.Get("Authorization"), r.Header.Get("Content-Type")

		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)

	body, err := client.New(srv.URL+"/", "k-1").Do(t.Context(), &client.Request{
		Method:     http.MethodPost,
		Path:       "/projects/{projectId}/resources",
		PathParams: map[string]string{"projectId": "a/b"},
		Query:      map[string][]string{"limit": {"5"}},
		Body:       map[string]any{"name": "shop"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if string(body) != `{"ok":true}` {
		t.Errorf("body = %s", body)
	}

	// The slash in the id is escaped, so it cannot reach another path.
	if gotPath != "/projects/a%2Fb/resources" || gotQuery != "limit=5" {
		t.Errorf("path = %s?%s", gotPath, gotQuery)
	}

	if gotAuth != "Bearer k-1" || gotType != "application/json" || gotBody["name"] != "shop" {
		t.Errorf("auth %q, type %q, body %v", gotAuth, gotType, gotBody)
	}
}

func TestUnfilledPathParamIsNamedAlone(t *testing.T) {
	t.Parallel()

	_, err := client.New("http://unused.invalid", "k").Do(t.Context(), &client.Request{
		Method: http.MethodGet,
		Path:   "/v1/projects/{projectId}/resources/{resourceId}",
	})
	if err == nil || !strings.Contains(err.Error(), "parameter {projectId} has no value") {
		t.Errorf("want only {projectId} named, got %v", err)
	}
}

func TestDoRejectsUnfilledPathParam(t *testing.T) {
	t.Parallel()

	_, err := client.New("http://unused.invalid", "k").Do(t.Context(), &client.Request{
		Method: http.MethodGet,
		Path:   "/projects/{projectId}",
	})
	if err == nil || !strings.Contains(err.Error(), "{projectId}") {
		t.Fatalf("want an error naming {projectId}, got %v", err)
	}
}

func TestDoReturnsAPIError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"msg":"api key is missing a required scope","msgCode":"forbidden"}`)
	}))
	t.Cleanup(srv.Close)

	_, err := client.New(srv.URL, "k").Do(t.Context(), &client.Request{
		Method: http.MethodPost, Path: "/projects", Scope: "project:write",
	})

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Fatalf("want a 403 *APIError, got %v", err)
	}

	want := "POST /projects: HTTP 403: forbidden: api key is missing a required scope (requires scope project:write)"
	if err.Error() != want {
		t.Errorf("error = %q\n want %q", err.Error(), want)
	}
}

func TestDoDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	var calls []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	_, err := client.New(srv.URL, "k").Do(t.Context(), &client.Request{
		Method: http.MethodPost, Path: "/projects", Body: map[string]any{"name": "x"},
	})

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusFound || !strings.Contains(err.Error(), "unexpected redirect to /elsewhere") {
		t.Fatalf("want a 302 *APIError naming the target, got %v", err)
	}

	// Following it would have turned the POST into a GET and sent the key on.
	if len(calls) != 1 {
		t.Errorf("requests sent: %v", calls)
	}
}

func TestDoRejectsPathValuesThatMoveTheURL(t *testing.T) {
	t.Parallel()

	sent := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { sent = true }))
	t.Cleanup(srv.Close)

	for _, v := range []string{"", ".", "..", "a\nb", "a\x00"} {
		_, err := client.New(srv.URL, "k").Do(t.Context(), &client.Request{
			Method:     http.MethodDelete,
			Path:       "/projects/{projectId}",
			PathParams: map[string]string{"projectId": v},
		})
		if err == nil || !strings.Contains(err.Error(), "projectId") {
			t.Errorf("projectId %q: want an error naming projectId, got %v", v, err)
		}
	}

	// Escaped, not refused: a slash cannot add a segment.
	_, err := client.New(srv.URL, "k").Do(t.Context(), &client.Request{
		Method: http.MethodGet, Path: "/projects/{projectId}", PathParams: map[string]string{"projectId": "../x"},
	})
	if err != nil {
		t.Errorf("../x should be escaped and sent: %v", err)
	}

	if !sent {
		t.Error("the escaped request was not sent")
	}
}

func TestPlainHTTPOnlyDialsLoopback(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)

	// "localhost" by name works: it resolves to loopback.
	local := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	_, err := client.New(local, "k").Do(t.Context(), &client.Request{Method: http.MethodGet, Path: "/projects"})
	if err != nil {
		t.Fatalf("http to localhost: %v", err)
	}

	// An address off the machine is refused before any connection, so the key
	// cannot travel in clear text. 192.0.2.1 is reserved for documentation.
	_, err = client.New("http://192.0.2.1:1", "k").Do(t.Context(), &client.Request{Method: http.MethodGet, Path: "/projects"})
	if err == nil || !strings.Contains(err.Error(), "does not resolve to a loopback address") {
		t.Fatalf("http off the machine: %v", err)
	}
}

func TestServerTextIsMadeSafe(t *testing.T) {
	t.Parallel()

	// \u001b decodes to a real ESC, which would start a terminal escape.
	body := `{"msg":"bad\u001b[31m red\nline","msgCode":"bad_request"}` + strings.Repeat(" ", 10)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	_, err := client.New(srv.URL, "k").Do(t.Context(), &client.Request{Method: http.MethodGet, Path: "/projects"})
	if err == nil || strings.ContainsAny(err.Error(), "\x1b\n") || !strings.Contains(err.Error(), "bad[31m red line") {
		t.Errorf("error text %q", err)
	}

	long := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "<html>"+strings.Repeat("x", 5000))
	}))
	t.Cleanup(long.Close)

	_, err = client.New(long.URL, "k").Do(t.Context(), &client.Request{Method: http.MethodGet, Path: "/projects"})
	if err == nil || len(err.Error()) > 700 {
		t.Errorf("a 5000-byte error page made a %d-byte error", len(err.Error()))
	}
}

func TestResponseSizeIsBounded(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 11<<20))
	}))
	t.Cleanup(srv.Close)

	_, err := client.New(srv.URL, "k").Do(t.Context(), &client.Request{Method: http.MethodGet, Path: "/projects"})
	if err == nil || !strings.Contains(err.Error(), "larger than 10 MiB") {
		t.Errorf("an 11 MiB response: %v", err)
	}
}

func TestWriteTimeoutSaysOutcomeUnknown(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))

	t.Cleanup(func() { close(release); srv.Close() })

	for method, wantUnknown := range map[string]bool{http.MethodGet: false, http.MethodPost: true, http.MethodDelete: true} {
		c := client.New(srv.URL, "k")
		c.HTTP.Timeout = 50 * time.Millisecond

		_, err := c.Do(t.Context(), &client.Request{Method: method, Path: "/projects"})
		if err == nil || errors.Is(err, client.ErrOutcomeUnknown) != wantUnknown {
			t.Errorf("%s timeout: %v, want outcome-unknown=%v", method, err, wantUnknown)
		}
	}
}

func TestPlainHTTPUsesNoProxy(t *testing.T) {
	t.Parallel()

	tr, ok := client.New("http://127.0.0.1:1", "k").HTTP.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil {
		t.Errorf("plain-http transport %T, proxy set: %v", client.New("http://127.0.0.1:1", "k").HTTP.Transport, ok && tr.Proxy != nil)
	}
}

// Not parallel: it sets env vars.
func TestKeysFromEnvAndFlagsAreCleaned(t *testing.T) {
	t.Setenv("AIGW_API_KEY", "aigw_sk_abc\r\n") // read from a Windows file

	got, err := client.ResolveAPIKey("", false)
	if err != nil || got != "aigw_sk_abc" {
		t.Errorf("trailing CRLF: %q, %v", got, err)
	}

	t.Setenv("AIGW_API_KEY", "aigw_sk_a b")

	_, err = client.ResolveAPIKey("", false)
	if !errors.Is(err, client.ErrBadKey) || !strings.Contains(err.Error(), "AIGW_API_KEY") {
		t.Errorf("a key with a space: %v", err)
	}

	_, err = client.ResolveAPIKey("aigw_sk_\x01", false)
	if !errors.Is(err, client.ErrBadKey) || !strings.Contains(err.Error(), "--api-key") {
		t.Errorf("a flag key with a control character: %v", err)
	}
}

// Not parallel: it swaps the package resolver. CI found this: "localhost"
// resolved to ::1 first, the test server listened on 127.0.0.1 only, and the
// transport gave up after the first address.
func TestPlainHTTPTriesEveryLoopbackAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)

	restore := client.SetLookupIPAddr(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.IPv6loopback}, {IP: net.IPv4(127, 0, 0, 1)}}, nil
	})
	t.Cleanup(restore)

	base := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	_, err := client.New(base, "k").Do(t.Context(), &client.Request{Method: http.MethodGet, Path: "/projects"})
	if err != nil {
		t.Fatalf("::1 first, server on 127.0.0.1: %v", err)
	}
}
