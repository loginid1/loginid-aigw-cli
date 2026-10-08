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
		{"http://localhost:8085", true, true},
		{"http://127.0.0.1:8085", true, true},
		{"http://[::1]:8085", true, true},
		{"http://evil.example", false, false},             // plain http off-host
		{"https://user:pw@staging.example", false, false}, // userinfo
		{"staging.example", false, false},                 // no scheme
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

func TestTemporaryBaseNeverGetsTheSavedKey(t *testing.T) {
	keyring.MockInit()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AIGW_API_KEY", "")

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

	// The warning prints once per process, so this first run carries it.
	out, err := run(t, "", "projects", "list")
	if !strings.Contains(out, "warning: AIGW_TEMP_API_BASE is set") {
		t.Errorf("no warning in %q", out)
	}

	if cmd.ExitCode(err) != apicmd.ExitNotAuthenticated || !strings.Contains(err.Error(), "saved login is not used") {
		t.Fatalf("want exit 3 refusing the saved key, got %d: %v", cmd.ExitCode(err), err)
	}

	if len(auths) != 0 {
		t.Fatalf("a request was sent: %v", auths)
	}

	// An explicitly given key is used.
	t.Setenv("AIGW_API_KEY", "aigw_sk_explicit")

	_, err = run(t, "", "projects", "list")
	if err != nil {
		t.Fatal(err)
	}

	if len(auths) != 1 || auths[0] != "Bearer aigw_sk_explicit" {
		t.Errorf("sent %v", auths)
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
