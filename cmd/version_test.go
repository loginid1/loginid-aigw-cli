package cmd_test

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/cmd"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/apicmd"
)

func TestVersionCommand(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	root := cmd.NewRootCommand()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	got := out.String()
	if !strings.HasPrefix(got, "aigw ") {
		t.Errorf("want output starting with %q, got %q", "aigw ", got)
	}
}

// --version, -v and `version` print the same line.
func TestVersionFlag(t *testing.T) {
	t.Parallel()

	want := versionOutput(t, "version")

	for _, args := range [][]string{{"--version"}, {"-v"}} {
		if got := versionOutput(t, args...); got != want {
			t.Errorf("%v printed %q, want %q", args, got, want)
		}
	}
}

func TestVersionJSON(t *testing.T) {
	t.Parallel()

	var got map[string]string

	out := versionOutput(t, "version", "-o", "json")

	err := json.Unmarshal([]byte(out), &got)
	if err != nil {
		t.Fatalf("not JSON: %v: %q", err, out)
	}

	if got["specRef"] != apicmd.SpecRef || got["os"] != runtime.GOOS || got["version"] == "" || got["commit"] == "" {
		t.Errorf("report = %v", got)
	}

	_, err = run(t, "", "version", "-o", "yaml")
	if err == nil {
		t.Error("-o yaml accepted")
	}
}

func versionOutput(t *testing.T, args ...string) string {
	t.Helper()

	var out bytes.Buffer

	root := cmd.NewRootCommand()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)

	err := root.Execute()
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}

	return out.String()
}
