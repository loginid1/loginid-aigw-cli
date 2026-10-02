package apicmd_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/apicmd"
)

const (
	apiKey   = "--api-key"
	projects = "projects"
)

type captured struct {
	method, path, auth string
	body               map[string]any
}

// serve starts a fake API that records the one request it gets and answers
// with status and reply.
func serve(t *testing.T, status int, reply string) (*httptest.Server, *captured) {
	t.Helper()

	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.auth = r.Method, r.URL.Path, r.Header.Get("Authorization")

		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 && json.Unmarshal(raw, &got.body) != nil {
			t.Errorf("request body is not JSON: %s", raw)
		}

		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)

	return srv, got
}

// runAigw runs args against a command tree whose requests go to base.
func runAigw(t *testing.T, base string, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	root := &cobra.Command{Use: "aigw", SilenceUsage: true, SilenceErrors: true}
	apicmd.AddAllWithBase(root, base)
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)

	err := root.Execute()

	return out.String(), err
}

func TestResourcesCreateBuildsRequest(t *testing.T) {
	t.Parallel()

	srv, got := serve(t, http.StatusCreated, `{"resource":{"id":"res-1"}}`)

	out, err := runAigw(t, srv.URL, "resources", "create",
		apiKey, "k-123",
		"--project-id", "proj-9",
		"--name", "staples-checkout",
		"--kind", "EXCHANGE",
		"--exchange.send-resource-param=false",
		"--idps", "google", "--idps", "apple",
		"-d", "scopes[0][value]=payments:read",
		"-d", "scopes[0][displayName]=Read payments",
		"-d", "scopes[1][value]=payments:write",
	)
	if err != nil {
		t.Fatalf("resources create: %v", err)
	}

	if got.method != http.MethodPost || got.path != "/projects/proj-9/resources" {
		t.Errorf("got %s %s", got.method, got.path)
	}

	if got.auth != "Bearer k-123" {
		t.Errorf("Authorization = %q", got.auth)
	}

	want := map[string]any{
		"name":     "staples-checkout",
		"kind":     "EXCHANGE",
		"exchange": map[string]any{"sendResourceParam": false},
		"idps":     []any{"google", "apple"},
		"scopes": []any{
			map[string]any{"value": "payments:read", "displayName": "Read payments"},
			map[string]any{"value": "payments:write"},
		},
	}
	if !reflect.DeepEqual(got.body, want) {
		t.Errorf("body\n got %v\nwant %v", got.body, want)
	}

	if !strings.Contains(out, `"id": "res-1"`) {
		t.Errorf("want indented JSON output, got %q", out)
	}
}

func TestPositionalIDIsTheLastPathSegment(t *testing.T) {
	t.Parallel()

	srv, got := serve(t, http.StatusOK, `{}`)

	_, err := runAigw(t, srv.URL, "resources", "get", "res/7", "--project-id", "proj-9", apiKey, "k")
	if err != nil {
		t.Fatal(err)
	}

	// The slash is escaped, so an id cannot walk into another path.
	if got.path != "/projects/proj-9/resources/res/7" && got.path != "/projects/proj-9/resources/res%2F7" {
		t.Errorf("path = %q", got.path)
	}

	if got.method != http.MethodGet || got.body != nil {
		t.Errorf("got %s with body %v", got.method, got.body)
	}
}

// Not parallel: it clears AIGW_API_KEY, so a key in the developer's shell
// cannot turn the "no key" case into a real request.
func TestExitCodes(t *testing.T) {
	forbidden, _ := serve(t, http.StatusForbidden, `{"msg":"Forbidden","msgCode":"forbidden"}`)
	unauthorized, _ := serve(t, http.StatusUnauthorized, `{"msg":"Unauthorized","msgCode":"unauthorized"}`)
	notFound, _ := serve(t, http.StatusNotFound, `{"msg":"Not found","msgCode":"not_found"}`)

	cases := []struct {
		name     string
		base     string
		args     []string
		code     int
		contains string
	}{
		{"no key", forbidden.URL, []string{projects, "list"}, apicmd.ExitNotAuthenticated, "not logged in"},
		{"401", unauthorized.URL, []string{projects, "list", apiKey, "k"}, apicmd.ExitNotAuthenticated, "HTTP 401"},
		{"403 names the scope", forbidden.URL, []string{projects, "create", "--name", "x", apiKey, "k"}, apicmd.ExitMissingScope, "project:write"},
		{"404", notFound.URL, []string{projects, "get", "p1", apiKey, "k"}, 0, "not_found: Not found"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AIGW_API_KEY", "")
			// An empty config dir, so a real `aigw login` on this machine is not used.
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())

			_, err := runAigw(t, tc.base, tc.args...)

			// 0 here means a plain error, which main turns into exit 1.
			code := 0

			var exitErr *apicmd.ExitError
			if errors.As(err, &exitErr) {
				code = exitErr.Code
			}

			if code != tc.code {
				t.Errorf("exit code = %d, want %d (err: %v)", code, tc.code, err)
			}

			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("error %v does not mention %q", err, tc.contains)
			}
		})
	}
}

func TestDeleteNeedsConfirmWithoutTerminal(t *testing.T) {
	t.Parallel()

	srv, got := serve(t, http.StatusNoContent, "")

	_, err := runAigw(t, srv.URL, projects, "delete", "p1", apiKey, "k")
	if err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("want a --confirm error, got %v", err)
	}

	if got.method != "" {
		t.Fatal("request was sent without confirmation")
	}

	out, err := runAigw(t, srv.URL, projects, "delete", "p1", "--confirm", apiKey, "k")
	if err != nil {
		t.Fatal(err)
	}

	if got.method != http.MethodDelete || out != "" {
		t.Errorf("got %s, output %q", got.method, out)
	}
}

func TestDataRejectsBadInput(t *testing.T) {
	t.Parallel()

	srv, got := serve(t, http.StatusCreated, `{}`)

	cases := map[string]string{
		"unknown field": "nam=x",
		"bad type":      "exchange[sendResourceParam]=maybe",
		"skipped index": "scopes[1][value]=x",
		"not key=value": "name",
		"malformed key": "scopes[0[value]=x",
	}

	for name, d := range cases {
		_, err := runAigw(t, srv.URL, "resources", "create", "--project-id", "p", "-d", d, apiKey, "k")
		if err == nil {
			t.Errorf("%s: -d %q was accepted", name, d)
		}
	}

	if got.method != "" {
		t.Error("a request was sent for invalid -d input")
	}
}
