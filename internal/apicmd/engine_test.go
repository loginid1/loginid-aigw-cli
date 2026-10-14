package apicmd_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/apicmd"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/client"
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
		got.method, got.path, got.auth = r.Method, r.URL.EscapedPath(), r.Header.Get("Authorization")

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

	if got.method != http.MethodPost || got.path != "/v1/projects/proj-9/resources" {
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
	if got.path != "/v1/projects/proj-9/resources/res%2F7" {
		t.Errorf("path = %q", got.path)
	}

	if got.method != http.MethodGet || got.body != nil {
		t.Errorf("got %s with body %v", got.method, got.body)
	}
}

// Not parallel: it clears AIGW_API_KEY, so a key in the developer's shell
// cannot turn the "no key" case into a real request.
func TestExitCodes(t *testing.T) {
	// The backend's own wording for each kind of 403.
	forbidden, _ := serve(t, http.StatusForbidden, `{"msg":"api key is missing a required scope","msgCode":"forbidden"}`)
	wrongRole, _ := serve(t, http.StatusForbidden, `{"msg":"insufficient project role","msgCode":"forbidden"}`)
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
		// A 403 for a role is not a scope problem: no exit 4, no scope hint.
		{"403 for a role", wrongRole.URL, []string{projects, "create", "--name", "x", apiKey, "k"}, 0, "insufficient project role"},
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

			if tc.code != apicmd.ExitMissingScope && err != nil && strings.Contains(err.Error(), "requires scope") {
				t.Errorf("error %v names a scope", err)
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

	if got.method != http.MethodDelete || out != "{\n  \"deleted\": true,\n  \"projectId\": \"p1\"\n}\n" {
		t.Errorf("got %s, output %q", got.method, out)
	}
}

func TestDeleteResultNamesEveryPathParam(t *testing.T) {
	t.Parallel()

	srv, _ := serve(t, http.StatusNoContent, "")

	out, err := runAigw(t, srv.URL, "resources", "delete", "r1", "--project-id", "p1", "--confirm", apiKey, "k")
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any

	err = json.Unmarshal([]byte(out), &got)
	if err != nil || !reflect.DeepEqual(got, map[string]any{"deleted": true, "projectId": "p1", "resourceId": "r1"}) {
		t.Errorf("output %q, %v", out, err)
	}
}

// A delete that answers with a body prints that body, not a made-up one.
func TestDeleteWithABodyPrintsIt(t *testing.T) {
	t.Parallel()

	srv, _ := serve(t, http.StatusOK, `{"id":"p1","gone":"yes"}`)

	out, err := runAigw(t, srv.URL, projects, "delete", "p1", "--confirm", apiKey, "k")
	if err != nil || strings.Contains(out, "deleted") || !strings.Contains(out, `"gone": "yes"`) {
		t.Errorf("output %q, %v", out, err)
	}
}

func TestDeletePromptNamesTheTargetAndWhatGoesWithIt(t *testing.T) {
	t.Parallel()

	srv, got := serve(t, http.StatusOK, `{"id":"p1","name":"checkout"}`)

	op := apicmd.OperationByID("projects#delete")
	req := &client.Request{Method: op.Method, Path: op.Path, PathParams: map[string]string{"projectId": "p1"}}

	prompt := apicmd.DeletePrompt(t.Context(), op, "projects delete p1", client.New(srv.URL, "k"), req)

	for _, want := range []string{
		`This runs "projects delete p1", deleting project "checkout".`,
		"everything the platform holds under it",
		"cannot be undone. Continue? [y/N] ",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}

	if got.method != http.MethodGet || got.path != "/v1/projects/p1" {
		t.Errorf("looked the name up with %s %s", got.method, got.path)
	}

	// No name when the lookup fails; the prompt still asks.
	down, _ := serve(t, http.StatusNotFound, `{"msgCode":"not_found","msg":"project not found"}`)

	prompt = apicmd.DeletePrompt(t.Context(), op, "projects delete p1", client.New(down.URL, "k"), req)
	if strings.Contains(prompt, "deleting") || !strings.Contains(prompt, "Continue?") {
		t.Errorf("prompt after a failed lookup:\n%s", prompt)
	}
}

func TestDataCannotOverwriteAFlag(t *testing.T) {
	t.Parallel()

	srv, got := serve(t, http.StatusOK, `{}`)

	cases := [][]string{
		{projects, "create", "--name", "a", "-d", "name=b"},
		{"resources", "create", "--project-id", "p", "--name", "n", "--exchange.audience", "a", "-d", "exchange[audience]=b"},
		{"resources", "create", "--project-id", "p", "--name", "n", "--oauth-client.redirect-uris", "https://a", "-d", "oauthClient[redirectUris][0]=https://b"},
		{projects, "domain-rules", "set", "d.example", "--project-id", "p", "--decision", "APPROVE", "-d", "decision=REJECT"},
	}

	for _, args := range cases {
		_, err := runAigw(t, srv.URL, append(args, apiKey, "k")...)
		if err == nil || !strings.Contains(err.Error(), "set the same field") {
			t.Errorf("%v: %v", args, err)
		}
	}

	if got.method != "" {
		t.Fatal("a request was sent")
	}

	// Different fields still mix.
	_, err := runAigw(t, srv.URL, "resources", "create", "--project-id", "p", "--name", "n",
		"-d", "exchange[audience]=b", apiKey, "k")
	if err != nil || got.body["name"] != "n" {
		t.Errorf("flag and -d on different fields: %v, body %v", err, got.body)
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
		"huge index":    "scopes[300000000][value]=x",
		"index alias":   "scopes[00][value]=x",
		"bad enum":      "kind=BOGUS",
		"dotted key":    "exchange.audience=x",
	}

	for name, d := range cases {
		_, err := runAigw(t, srv.URL, "resources", "create", "--project-id", "p", "-d", d, apiKey, "k")
		if err == nil {
			t.Errorf("%s: -d %q was accepted", name, d)
		}
	}

	// A flag with a value outside its enum.
	_, err := runAigw(t, srv.URL, "resources", "create", "--project-id", "p", "--kind", "BAD", apiKey, "k")
	if err == nil || !strings.Contains(err.Error(), "not one of") {
		t.Errorf("--kind BAD: %v", err)
	}

	if got.method != "" {
		t.Error("a request was sent for invalid input")
	}
}

func TestHelpMarksRequiredFields(t *testing.T) {
	t.Parallel()

	out, err := runAigw(t, "http://unused.invalid", "resources", "replace", "--help")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"# required fields",
		"aigw resources replace <resource-id> --project-id <project-id> --idps <idps> --kind EXCHANGE --name <name>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}

	// A field required only inside the optional exchange object is not
	// marked: the server checks it, as in the Stripe CLI.
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "--exchange.client-id") && strings.Contains(line, "(required)") {
			t.Errorf("conditionally required flag marked required: %q", line)
		}
	}
}

func TestErrorBodyShapes(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ body, want string }{
		"AppError": {
			`{"msg":"project not found","msgCode":"not_found"}`,
			"HTTP 400: not_found: project not found",
		},
		// What Goa sends for a request that fails validation, as seen
		// against the real server.
		"Goa validation": {
			`{"name":"missing_field","id":"QuIrpdzS","message":"\"kind\" is missing from body","temporary":false,"timeout":false,"fault":false}`,
			`HTTP 400: missing_field: "kind" is missing from body`,
		},
		"not JSON": {"upstream timeout", "HTTP 400: upstream timeout"},
		"empty":    {"", "HTTP 400: Bad Request"},
	}

	for name, tc := range cases {
		srv, _ := serve(t, http.StatusBadRequest, tc.body)

		_, err := runAigw(t, srv.URL, "projects", "list", apiKey, "k")
		if err == nil || !strings.HasSuffix(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want suffix %q", name, err, tc.want)
		}
	}
}

// Every -d example printed in help must work when copied as written.
func TestEveryDataExampleWorks(t *testing.T) {
	t.Parallel()

	exampleRe := regexp.MustCompile(`e\.g\. -d "([^"]+)"`)

	for _, op := range apicmd.Operations {
		if !op.HasBody {
			continue
		}

		srv, got := serve(t, http.StatusOK, `{}`)

		args := slices.Clone(op.Command)
		for range op.Args {
			args = append(args, "id-1")
		}

		help, err := runAigw(t, srv.URL, append(slices.Clone(args), "--help")...)
		if err != nil {
			t.Fatalf("%s --help: %v", op.ID, err)
		}

		m := exampleRe.FindStringSubmatch(help)
		if m == nil {
			t.Errorf("%s: no -d example in help", op.ID)

			continue
		}

		for _, p := range op.Params {
			if p.In == apicmd.InPath {
				args = append(args, "--"+p.Flag, "id-2")
			}
		}

		_, err = runAigw(t, srv.URL, append(args, "-d", m[1], apiKey, "k")...)
		if err != nil {
			t.Errorf("%s: -d %q from help fails: %v", op.ID, m[1], err)
		}

		if got.method == "" {
			t.Errorf("%s: -d %q from help sent no request", op.ID, m[1])
		}
	}
}

func TestDeleteRefusesDevNull(t *testing.T) {
	t.Parallel()

	srv, got := serve(t, http.StatusNoContent, "")

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()

	var out bytes.Buffer

	root := &cobra.Command{Use: "aigw", SilenceUsage: true, SilenceErrors: true}
	apicmd.AddAllWithBase(root, srv.URL)
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(devNull)
	root.SetArgs([]string{projects, "delete", "p1", apiKey, "k"})

	// /dev/null is a character device, but not a terminal: refuse, don't prompt.
	err = root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--confirm") || strings.Contains(out.String(), "Continue?") {
		t.Errorf("err %v, output %q", err, out.String())
	}

	if got.method != "" {
		t.Error("a request was sent")
	}
}

// A typo in a subcommand fails, instead of printing help and exiting 0.
func TestUnknownSubcommandFails(t *testing.T) {
	t.Parallel()

	_, err := runAigw(t, "http://unused.invalid", projects, "lsit")
	if err == nil || !strings.Contains(err.Error(), `unknown command "lsit"`) || !strings.Contains(err.Error(), "did you mean list") {
		t.Errorf("projects lsit: %v", err)
	}

	_, err = runAigw(t, "http://unused.invalid", projects)
	if err != nil {
		t.Errorf("a bare noun should show its help: %v", err)
	}
}

// A success reply that is not JSON did not come from the API: most often a
// wrong base URL that reached a web app serving its page for every path.
// Found against eval, where aigw.eval.loginid.ai/v1/projects returned the
// console's HTML and the CLI printed it and exited 0.
func TestNonJSONSuccessIsAnError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<!doctype html><title>Console</title>\x1b[31m")
	}))
	t.Cleanup(srv.Close)

	out, err := runAigw(t, srv.URL, projects, "list", apiKey, "k")
	if err == nil || !strings.Contains(err.Error(), "check the base URL") || !strings.Contains(err.Error(), "a web page (text/html)") {
		t.Fatalf("want a not-the-API error, got %v", err)
	}

	if strings.Contains(out, "<!doctype") || strings.ContainsRune(err.Error(), '\x1b') {
		t.Errorf("the page leaked into the output: %q / %q", out, err)
	}
}

func TestJSONOutputEndsWithOneNewline(t *testing.T) {
	t.Parallel()

	srv, _ := serve(t, http.StatusOK, "{\"id\":\"p1\"}\n") // Goa ends bodies with a newline

	out, err := runAigw(t, srv.URL, projects, "get", "p1", apiKey, "k")
	if err != nil {
		t.Fatal(err)
	}

	if out != "{\n  \"id\": \"p1\"\n}\n" {
		t.Errorf("output %q", out)
	}
}
