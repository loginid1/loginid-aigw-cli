package cmd_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/cmd"
)

// skillHome points Claude Code's config at a temp dir and returns the skill's
// path inside it.
func skillHome(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	return filepath.Join(dir, "skills", "aigw", "SKILL.md")
}

// Not parallel: these set env vars.
func TestSkillInstallUpdateUninstall(t *testing.T) {
	path := skillHome(t)

	out, err := run(t, "", "skill", "install")
	if err != nil || !strings.Contains(out, "Installed") {
		t.Fatalf("%v; output %q", err, out)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	shown, _ := run(t, "", "skill", "show")
	if string(got) != shown {
		t.Error("installed file differs from `skill show`")
	}

	out, _ = run(t, "", "skill", "install")
	if !strings.Contains(out, "already up to date") {
		t.Errorf("second install: %q", out)
	}

	// A copy from an older aigw is ours: replaced without --confirm.
	err = os.WriteFile(path, []byte(strings.Replace(string(got), "Written by aigw", "Written by aigw 0.0.1-old;", 1)), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	out, err = run(t, "", "skill", "install")
	if err != nil || !strings.Contains(out, "Updated") {
		t.Errorf("update: %v; output %q", err, out)
	}

	out, err = run(t, "", "skill", "uninstall")
	if err != nil || !strings.Contains(out, "Removed") {
		t.Fatalf("%v; output %q", err, out)
	}

	_, err = os.Stat(filepath.Dir(path))
	if !os.IsNotExist(err) {
		t.Errorf("skill dir left behind: %v", err)
	}

	out, err = run(t, "", "skill", "uninstall")
	if err != nil || !strings.Contains(out, "not installed") {
		t.Errorf("uninstall twice: %v; output %q", err, out)
	}
}

// A SKILL.md the user wrote is never replaced or removed without --confirm.
func TestSkillLeavesSomeoneElsesFileAlone(t *testing.T) {
	path := skillHome(t)

	err := os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	const mine = "---\nname: aigw\ndescription: my own\n---\n"

	err = os.WriteFile(path, []byte(mine), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"skill", "install"}, {"skill", "uninstall"}} {
		_, err = run(t, "", args...)
		if err == nil || !strings.Contains(err.Error(), "--confirm") {
			t.Errorf("%v: %v", args, err)
		}

		if got, _ := os.ReadFile(path); string(got) != mine {
			t.Fatalf("%v changed the file", args)
		}
	}

	_, err = run(t, "", "skill", "install", "--confirm")
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "Written by aigw") {
		t.Error("--confirm did not replace it")
	}
}

func TestSkillDefaultsToHomeClaudeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	_, err := run(t, "", "skill", "install")
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(home, ".claude", "skills", "aigw", "SKILL.md"))
	if err != nil {
		t.Error(err)
	}
}

// Every command the skill names exists, so the skill cannot drift from the
// CLI unnoticed.
func TestSkillNamesOnlyRealCommands(t *testing.T) {
	t.Parallel()

	shown, err := run(t, "", "skill", "show")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(shown, "---\nname: aigw\ndescription: ") {
		t.Errorf("frontmatter: %q", shown[:min(len(shown), 60)])
	}

	known := map[string]bool{}

	var walk func(c *cobra.Command, path string)

	walk = func(c *cobra.Command, path string) {
		for _, sub := range c.Commands() {
			name := strings.TrimSpace(path + " " + sub.Name())
			known[name] = true
			walk(sub, name)
		}
	}

	walk(cmd.NewRootCommand(), "")

	nouns := `projects|resources|status|login|logout|skill|version`
	cmdRe := regexp.MustCompile("`aigw ((?:" + nouns + `)(?: [a-z][a-z-]*)*)`)

	for _, m := range cmdRe.FindAllStringSubmatch(shown, -1) {
		words := strings.Fields(m[1])
		// Trailing words may be placeholders such as <noun>; check the
		// longest real prefix is not empty.
		for len(words) > 0 && !known[strings.Join(words, " ")] {
			words = words[:len(words)-1]
		}

		if len(words) == 0 {
			t.Errorf("the skill names %q, which is not a command", m[0])
		}
	}

	if strings.Contains(shown, "{{version}}") {
		t.Error("version placeholder left in")
	}
}
