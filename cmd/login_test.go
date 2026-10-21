package cmd_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/cmd"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/apicmd"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/credentials"
)

func run(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	root := cmd.NewRootCommand()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)

	err := root.Execute()

	return out.String(), err
}

// Not parallel: it sets env vars and swaps in the keyring mock.
func TestLoginLogout(t *testing.T) {
	keyring.MockInit()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	const key = "aigw_sk_Xk7Qb2vR9sT4wY6zA1cD3eF5"

	out, err := run(t, key+"\n", "login", "--with-key-stdin")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out, key) {
		t.Errorf("login printed the key: %q", out)
	}

	if !strings.Contains(out, "aigw_sk_…3eF5") {
		t.Errorf("login output lacks the key hint: %q", out)
	}

	got, err := credentials.Load(dir+"/aigw", apicmd.Profile())
	if err != nil || got != key {
		t.Fatalf("saved key = %q, %v", got, err)
	}

	_, err = run(t, "", "logout")
	if err != nil {
		t.Fatal(err)
	}

	_, err = credentials.Load(dir+"/aigw", apicmd.Profile())
	if err == nil {
		t.Error("key still saved after logout")
	}
}

func TestLoginRejectsBadInput(t *testing.T) {
	keyring.MockInit()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cases := map[string][]string{
		"no tty, no flag": {"login"},
		"empty key":       {"login", "--with-key-stdin"},
	}
	for name, args := range cases {
		_, err := run(t, "", args...)
		if err == nil {
			t.Errorf("%s: login succeeded", name)
		}
	}

	_, err := run(t, "two words", "login", "--with-key-stdin")
	if err == nil {
		t.Error("a key with a space was accepted")
	}

	// Refused, because they break the Authorization header or config.toml.
	for _, bad := range []string{"aigw_sk_\u65e5\u672c\u8a9e\u65e5\u672c\u8a9e\u65e5\u672c\u8a9e", "aigw_sk_ab\x01cdefghijkl"} {
		_, err = run(t, bad, "login", "--with-key-stdin")
		if err == nil || !strings.Contains(err.Error(), "printable ASCII") {
			t.Errorf("%q: %v", bad, err)
		}
	}

	// Refused, not cut to 4096 bytes and saved broken.
	_, err = run(t, "aigw_sk_"+strings.Repeat("a", 5000), "login", "--with-key-stdin")
	if err == nil || !strings.Contains(err.Error(), "longer than 4096 bytes") {
		t.Errorf("an oversized key: %v", err)
	}

	if got, _ := credentials.Load(os.Getenv("XDG_CONFIG_HOME")+"/aigw", apicmd.Profile()); got != "" {
		t.Errorf("an oversized key was saved: %d bytes", len(got))
	}
}

func TestLoginAgainNeedsConfirm(t *testing.T) {
	keyring.MockInit()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	const (
		oldKey = "aigw_sk_Xk7Qb2vR9sT4wY6zA1cD3eF5"
		newKey = "aigw_sk_Pq8Lm3nB7vC2xZ9wK4jH6gAB12"
	)

	_, err := run(t, oldKey, "login", "--with-key-stdin")
	if err != nil {
		t.Fatal(err)
	}

	// No terminal to ask on: refuse, and keep the old key.
	_, err = run(t, newKey, "login", "--with-key-stdin")
	if err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("want a --confirm error, got %v", err)
	}

	got, _ := credentials.Load(dir+"/aigw", apicmd.Profile())
	if got != oldKey {
		t.Fatalf("saved key changed without --confirm: %q", got)
	}

	out, err := run(t, newKey, "login", "--with-key-stdin", "--confirm")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "Replaced saved key aigw_sk_…3eF5 with aigw_sk_…AB12") {
		t.Errorf("output = %q", out)
	}

	got, _ = credentials.Load(dir+"/aigw", apicmd.Profile())
	if got != newKey {
		t.Errorf("saved key = %q, want the new one", got)
	}
}
