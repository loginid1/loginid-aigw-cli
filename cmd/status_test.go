package cmd_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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

	for _, v := range []string{"AIGW_API_KEY", "AIGW_TEMP_API_KEY", "AIGW_TEMP_API_BASE"} {
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

// login checks the key on the real API, never on AIGW_TEMP_API_BASE, which a
// cloned repo's env could set to collect it.
func TestLoginNeverSendsTheKeyToTheTemporaryBase(t *testing.T) {
	isolate(t)
	t.Setenv("AIGW_TEMP_API_BASE", "https://127.0.0.1:1")

	out, err := run(t, "aigw_sk_secretkey123", "login", "--with-key-stdin")
	if err != nil || !strings.Contains(out, "The key works") {
		t.Fatalf("%v; output %q", err, out)
	}

	if got := fake.take(); !slices.Equal(got, []string{"Bearer aigw_sk_secretkey123"}) {
		t.Errorf("sent %v", got)
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
