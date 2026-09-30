package cmd_test

import (
	"bytes"
	"strings"
	"testing"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/cmd"
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
