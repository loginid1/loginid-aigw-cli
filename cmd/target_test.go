package cmd_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/cmd"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/apicmd"
)

// Not parallel: these set env vars.
func TestResolveTarget(t *testing.T) {
	cases := []struct {
		value     string
		ok        bool
		temporary bool
	}{
		{"", true, false},
		{"https://staging.example", true, true},
		{"https://staging.example/api", true, true},
		{"http://localhost:8085", true, true},
		{"http://127.0.0.1:8085", true, true},
		{"http://[::1]:8085", true, true},
		{"http://evil.example", false, false},             // plain http off-host
		{"http://dev.localhost:8085", false, false},       // resolved through DNS
		{"http://evillocalhost:80", false, false},         // not localhost at all
		{"https://user:pw@staging.example", false, false}, // userinfo
		{"https://staging.example/x?y=1", false, false},   // the API path would land in the query
		{"https://staging.example/x#z", false, false},     // or in the fragment
		{"https://staging.example/x?", false, false},
		{"staging.example", false, false}, // no scheme
	}

	for _, tc := range cases {
		t.Setenv("AIGW_TEMP_API_BASE", tc.value)

		got, err := apicmd.ResolveTarget()
		if (err == nil) != tc.ok || (err == nil && got.Temporary != tc.temporary) {
			t.Errorf("%q: target %+v, err %v", tc.value, got, err)
		}

		if tc.value == "" && got.BaseURL != apicmd.DefaultAPIBase {
			t.Errorf("unset: base %q", got.BaseURL)
		}
	}
}

// A temporary base gets a key only from --api-key or AIGW_TEMP_API_KEY: never
// the saved login, and never AIGW_API_KEY, which a cloned repo's .mcp.json
// could pair with its own AIGW_TEMP_API_BASE.
func TestTemporaryBaseGetsOnlyItsOwnKey(t *testing.T) {
	keyring.MockInit()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AIGW_API_KEY", "")
	t.Setenv("AIGW_TEMP_API_KEY", "")

	_, err := run(t, "aigw_sk_saved", "login", "--with-key-stdin")
	if err != nil {
		t.Fatal(err)
	}

	var auths []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("AIGW_TEMP_API_BASE", srv.URL)
	t.Setenv("AIGW_API_KEY", "aigw_sk_from_shell")

	out, err := run(t, "", "projects", "list")
	if !strings.Contains(out, "warning: AIGW_TEMP_API_BASE is set") {
		t.Errorf("no warning in %q", out)
	}

	if cmd.ExitCode(err) != apicmd.ExitNotAuthenticated || !strings.Contains(err.Error(), "AIGW_TEMP_API_KEY") {
		t.Fatalf("want exit 3 asking for AIGW_TEMP_API_KEY, got %d: %v", cmd.ExitCode(err), err)
	}

	if len(auths) != 0 {
		t.Fatalf("a request was sent with %v", auths)
	}

	t.Setenv("AIGW_TEMP_API_KEY", "aigw_sk_temp")

	_, err = run(t, "", "projects", "list")
	if err != nil {
		t.Fatal(err)
	}

	_, err = run(t, "", "projects", "list", "--api-key", "aigw_sk_typed")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"Bearer aigw_sk_temp", "Bearer aigw_sk_typed"}
	if strings.Join(auths, ",") != strings.Join(want, ",") {
		t.Errorf("sent %v, want %v", auths, want)
	}
}

func TestHelpDoesNotTouchTheTarget(t *testing.T) {
	t.Setenv("AIGW_TEMP_API_BASE", "http://evil.example")

	out, err := run(t, "", "projects", "list", "--help")
	if err != nil {
		t.Fatalf("help failed over the base URL: %v", err)
	}

	if strings.Contains(out, "AIGW_TEMP_API_BASE") {
		t.Errorf("help mentions the override: %q", out)
	}
}

func TestMCPRefusesABadTemporaryBase(t *testing.T) {
	t.Setenv("AIGW_TEMP_API_BASE", "http://evil.example")

	// It must fail before it starts serving on stdin.
	_, err := run(t, "", "mcp")
	if err == nil || !strings.Contains(err.Error(), "must be https") {
		t.Fatalf("aigw mcp with a bad base: %v", err)
	}
}
