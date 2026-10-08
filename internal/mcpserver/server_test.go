package mcpserver_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/apicmd"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/mcpserver"
)

type captured struct {
	method, path, auth string
	body               map[string]any
}

// connect starts the server over in-memory transports, backed by a fake API
// that answers every request with status and reply.
func connect(t *testing.T, opts mcpserver.Options, status int, reply string) (*mcp.ClientSession, *captured) {
	t.Helper()

	got := &captured{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.auth = r.Method, r.URL.Path, r.Header.Get("Authorization")

		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got.body)

		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(api.Close)

	opts.BaseURL = api.URL

	// Most tests pin a key. "-" leaves it empty, so the handler resolves one
	// the way `aigw mcp` does.
	switch opts.APIKey {
	case "":
		opts.APIKey = "aigw_sk_test"
	case "-":
		opts.APIKey = ""
	}

	serverT, clientT := mcp.NewInMemoryTransports()

	_, err := mcpserver.New(apicmd.Operations, opts).Connect(t.Context(), serverT, nil)
	if err != nil {
		t.Fatal(err)
	}

	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(t.Context(), clientT, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = session.Close() })

	return session, got
}

func toolNames(t *testing.T, s *mcp.ClientSession) []string {
	t.Helper()

	res, err := s.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}

	return names
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder

	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}

	return b.String()
}

func TestToolSets(t *testing.T) {
	t.Parallel()

	all, _ := connect(t, mcpserver.Options{ExposeSecrets: true}, http.StatusOK, `{}`)
	if n := len(toolNames(t, all)); n != len(apicmd.Operations) {
		t.Errorf("with secrets exposed: %d tools, want one per operation (%d)", n, len(apicmd.Operations))
	}

	// One-time secrets stay out by default.
	def, _ := connect(t, mcpserver.Options{}, http.StatusOK, `{}`)
	names := toolNames(t, def)

	for _, hidden := range []string{"projects_proxy_clients_create", "resources_create"} {
		if slices.Contains(names, hidden) {
			t.Errorf("%s is offered without --expose-secrets", hidden)
		}
	}

	if !slices.Contains(names, "resources_signing_keys_create") {
		t.Errorf("default tools lack resources_signing_keys_create: %v", names)
	}

	ro, _ := connect(t, mcpserver.Options{ReadOnly: true}, http.StatusOK, `{}`)

	res, err := ro.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	gets := 0

	for _, op := range apicmd.Operations {
		if op.Method == http.MethodGet {
			gets++
		}
	}

	if len(res.Tools) != gets {
		t.Errorf("read-only server offers %d tools, want the %d GET operations", len(res.Tools), gets)
	}

	for _, tool := range res.Tools {
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("read-only server offers %s, which writes", tool.Name)
		}
	}
}

// Not parallel: it sets env vars. With a temporary base URL the key may come
// only from AIGW_TEMP_API_KEY, never from AIGW_API_KEY or the saved login.
func TestTemporaryBaseIgnoresOtherKeys(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AIGW_API_KEY", "aigw_sk_from_shell")
	t.Setenv("AIGW_TEMP_API_KEY", "")

	s, got := connect(t, mcpserver.Options{TemporaryBase: true, APIKey: "-"}, http.StatusOK, `[]`)

	res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: "projects_list", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}

	if !res.IsError || !strings.Contains(text(res), "AIGW_TEMP_API_KEY") {
		t.Errorf("IsError=%v text=%q, want a tool error asking for AIGW_TEMP_API_KEY", res.IsError, text(res))
	}

	if got.method != "" {
		t.Errorf("a request was sent with %q", got.auth)
	}
}

func TestAnnotations(t *testing.T) {
	t.Parallel()

	s, _ := connect(t, mcpserver.Options{}, http.StatusOK, `{}`)

	res, err := s.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tool := range res.Tools {
		a := tool.Annotations

		switch tool.Name {
		case "projects_get":
			if !a.ReadOnlyHint {
				t.Errorf("projects_get not marked read-only")
			}
		case "projects_delete":
			// DestructiveHint unset means destructive, per the spec.
			if a.ReadOnlyHint || a.DestructiveHint != nil {
				t.Errorf("projects_delete not marked destructive: %+v", a)
			}
		case "projects_update":
			if a.DestructiveHint == nil || *a.DestructiveHint {
				t.Errorf("projects_update (PATCH) marked destructive: %+v", a)
			}
		}

		if !strings.Contains(tool.Description, "Requires API key scope: project:") {
			t.Errorf("%s description does not name its scope", tool.Name)
		}
	}
}

func TestCallSplitsArguments(t *testing.T) {
	t.Parallel()

	s, got := connect(t, mcpserver.Options{}, http.StatusOK, `{"id":"res-1","name":"shop2"}`)

	res, err := s.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "resources_update",
		Arguments: map[string]any{"projectId": "proj-9", "resourceId": "res-1", "name": "shop2"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if res.IsError {
		t.Fatalf("tool error: %s", text(res))
	}

	if got.method != http.MethodPatch || got.path != "/projects/proj-9/resources/res-1" {
		t.Errorf("sent %s %s", got.method, got.path)
	}

	// Path parameters go in the URL, never in the body.
	if len(got.body) != 1 || got.body["name"] != "shop2" {
		t.Errorf("body = %v, want only name", got.body)
	}

	if got.auth != "Bearer aigw_sk_test" {
		t.Errorf("Authorization = %q", got.auth)
	}

	if !strings.Contains(text(res), `"name":"shop2"`) {
		t.Errorf("result = %s", text(res))
	}
}

func TestErrorsReachTheModel(t *testing.T) {
	t.Parallel()

	forbidden, _ := connect(t, mcpserver.Options{}, http.StatusForbidden, `{"msg":"api key is missing a required scope","msgCode":"forbidden"}`)
	ok, okAPI := connect(t, mcpserver.Options{}, http.StatusOK, `{}`)

	cases := []struct {
		name string
		s    *mcp.ClientSession
		tool string
		args map[string]any
		want string
	}{
		{"API 403", forbidden, "projects_delete", map[string]any{"projectId": "p"}, "requires scope project:write"},
		{"missing path argument", ok, "projects_get", map[string]any{}, `missing properties: ["projectId"]`},
		{"unknown argument", ok, "projects_get", map[string]any{"projectId": "p", "bogus": 1}, `additional properties ["bogus"]`},
		// The cases the QA review found going through to the API.
		{"typo in a body field", ok, "projects_update", map[string]any{"projectId": "p", "nme": "x"}, `additional properties ["nme"]`},
		{"bad enum", ok, "resources_update", map[string]any{"projectId": "p", "resourceId": "r", "kind": "BAD"}, "enum"},
		{"wrong type", ok, "projects_get", map[string]any{"projectId": 5}, "type"},
		{"path value that moves the URL", ok, "projects_get", map[string]any{"projectId": ".."}, `".." is not a valid ID`},
		{"typo one level down", ok, "resources_auth_settings_update", map[string]any{
			"projectId": "p", "resourceId": "r",
			"options": map[string]any{"allowOneTimeCodeFallback": true, "showScopeDescriptions": true, "showVerifiedAgent": true, "shwoLogo": true},
		}, `additional properties ["shwoLogo"]`},
	}

	for _, tc := range cases {
		res, err := tc.s.CallTool(t.Context(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
		if err != nil {
			t.Errorf("%s: protocol error %v; want a tool error the model can read", tc.name, err)

			continue
		}

		if !res.IsError || !strings.Contains(text(res), tc.want) {
			t.Errorf("%s: IsError=%v text=%q, want %q", tc.name, res.IsError, text(res), tc.want)
		}
	}

	if okAPI.method != "" {
		t.Errorf("a request was sent for invalid arguments: %s %s", okAPI.method, okAPI.path)
	}
}

func TestNoContentResult(t *testing.T) {
	t.Parallel()

	s, got := connect(t, mcpserver.Options{}, http.StatusNoContent, "")

	res, err := s.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "projects_delete", Arguments: map[string]any{"projectId": "p1"},
	})
	if err != nil || res.IsError {
		t.Fatalf("delete: %v %s", err, text(res))
	}

	if got.method != http.MethodDelete || !strings.HasPrefix(text(res), "Done.") {
		t.Errorf("sent %s, result %q", got.method, text(res))
	}
}

func TestEveryInputSchemaCompiles(t *testing.T) {
	t.Parallel()

	// New panics on a schema that does not compile; offering every tool
	// compiles every schema.
	s, _ := connect(t, mcpserver.Options{ExposeSecrets: true}, http.StatusOK, `{}`)
	if n := len(toolNames(t, s)); n != len(apicmd.Operations) {
		t.Fatalf("%d tools for %d operations", n, len(apicmd.Operations))
	}
}
