package cmd_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/cmd"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/apicmd"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/config"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/credentials"
)

// isolate gives the test its own config dir and clears every env var that
// picks a key or a host.
func isolate(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	for _, v := range []string{"AIGW_API_KEY"} {
		t.Setenv(v, "")
	}

	fake.take()

	return filepath.Join(dir, "aigw")
}

// Not parallel: these set env vars.
func TestLoginRefusesARejectedKey(t *testing.T) {
	dir := isolate(t)

	out, err := run(t, "aigw_sk_rejected1234", "login", "--with-key-stdin")
	if cmd.ExitCode(err) != apicmd.ExitNotAuthenticated || !strings.Contains(err.Error(), "not saved") {
		t.Fatalf("exit %d, %v; output %q", cmd.ExitCode(err), err, out)
	}

	_, err = credentials.Load(dir, config.DefaultProfile)
	if err == nil {
		t.Error("a rejected key was saved")
	}
}

// The removed AIGW_TEMP_API_BASE must not come back in any form: a cloned
// repo's env could set it to collect the key.
func TestEnvCannotRedirectTheKey(t *testing.T) {
	isolate(t)
	t.Setenv("AIGW_TEMP_API_BASE", "https://127.0.0.1:1")
	t.Setenv("AIGW_TEMP_API_KEY", "aigw_sk_tempkey12345")

	out, err := run(t, "aigw_sk_secretkey123", "login", "--with-key-stdin")
	if err != nil || !strings.Contains(out, "The key works") {
		t.Fatalf("%v; output %q", err, out)
	}

	_, err = run(t, "", "projects", "list")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"Bearer aigw_sk_secretkey123", "Bearer aigw_sk_secretkey123"}
	if got := fake.take(); !slices.Equal(got, want) {
		t.Errorf("sent %v, want %v", got, want)
	}
}

func TestLoginSavesWhenTheAPIIsUnreachable(t *testing.T) {
	dir := isolate(t)

	prev := apicmd.APIBase
	apicmd.APIBase = "https://127.0.0.1:1"

	t.Cleanup(func() { apicmd.APIBase = prev })

	out, err := run(t, "aigw_sk_offline12345", "login", "--with-key-stdin")
	if err != nil || !strings.Contains(out, "could not check the key") {
		t.Fatalf("%v; output %q", err, out)
	}

	if got, _ := credentials.Load(dir, config.DefaultProfile); got != "aigw_sk_offline12345" {
		t.Errorf("saved key = %q", got)
	}
}

func TestStatus(t *testing.T) {
	isolate(t)

	out, err := run(t, "", "status", "-o", "json")
	if cmd.ExitCode(err) != apicmd.ExitNotAuthenticated {
		t.Fatalf("not logged in: exit %d, %v", cmd.ExitCode(err), err)
	}

	var r map[string]any

	err = json.Unmarshal([]byte(out), &r)
	if err != nil || r["check"] != "not_logged_in" || r["apiBase"] != apicmd.APIBase {
		t.Fatalf("report %v, %v from %q", r, err, out)
	}

	_, err = run(t, "aigw_sk_savedkey1234", "login", "--with-key-stdin")
	if err != nil {
		t.Fatal(err)
	}

	out, err = run(t, "", "status")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"API:    " + apicmd.APIBase, "Key:    aigw_sk_…1234 (saved login)", "Check:  ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "aigw_sk_savedkey1234") {
		t.Error("status printed the key")
	}

	t.Setenv("AIGW_API_KEY", "aigw_sk_fromshell123")

	out, _ = run(t, "", "status")
	if !strings.Contains(out, "(AIGW_API_KEY)") {
		t.Errorf("env key not named as the source:\n%s", out)
	}

	_, err = run(t, "", "status", "--api-key", "aigw_sk_rejected1234")
	if cmd.ExitCode(err) != apicmd.ExitNotAuthenticated {
		t.Errorf("rejected key: exit %d, %v", cmd.ExitCode(err), err)
	}
}

// Input that is not shaped like a key is saved unchecked: it may be another
// secret pasted by mistake, and must not go over the network.
func TestLoginNeverSendsSomethingThatIsNotAKey(t *testing.T) {
	dir := isolate(t)

	out, err := run(t, "ghp_somegithubtoken123", "login", "--with-key-stdin")
	if err != nil || !strings.Contains(out, "saving it without checking") {
		t.Fatalf("%v; output %q", err, out)
	}

	if got := fake.take(); len(got) != 0 {
		t.Errorf("sent %v", got)
	}

	if got, _ := credentials.Load(dir, config.DefaultProfile); got != "ghp_somegithubtoken123" {
		t.Errorf("saved key = %q", got)
	}
}

// A network that never answers holds login only for the check's own timeout.
func TestLoginCheckGivesUpQuickly(t *testing.T) {
	isolate(t)

	hang := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(hang.Close)

	prev := apicmd.APIBase
	apicmd.APIBase = hang.URL

	t.Cleanup(func() { apicmd.APIBase = prev })
	t.Cleanup(cmd.SetLoginCheckTimeout(200 * time.Millisecond))

	start := time.Now()

	out, err := run(t, "aigw_sk_slownetwork1", "login", "--with-key-stdin")
	if err != nil || !strings.Contains(out, "could not check the key") {
		t.Fatalf("%v; output %q", err, out)
	}

	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("login took %s", took)
	}
}
