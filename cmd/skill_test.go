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
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
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

	for _, bad := range unknownCommands(shown, commandPaths()) {
		t.Errorf("the skill names %q, which is not a command", bad)
	}

	if strings.Contains(shown, "{{version}}") {
		t.Error("version placeholder left in")
	}
}

// The check above must catch a made-up operation, a made-up nested operation,
// and a made-up noun, and must not trip on placeholders, flags or arguments.
func TestUnknownCommandsCatchesMadeUpPaths(t *testing.T) {
	t.Parallel()

	text := "Run `aigw projects frobnicate`, `aigw resources scopes rotate-everything` " +
		"or `aigw widgets list`. Fine: `aigw projects list`, `aigw status -o json`, " +
		"`aigw <noun> --help`, `aigw projects get <id>`, `aigw login`."

	got := unknownCommands(text, commandPaths())
	want := []string{"aigw projects frobnicate", "aigw resources scopes rotate-everything", "aigw widgets list"}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %q, want %q", got, want)
	}
}

// commandPaths is every command path in the real tree, e.g. "projects list".
func commandPaths() map[string]bool {
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

	return known
}

// commandRe matches a code span that starts `aigw word word...`: the words
// stop at the first flag, placeholder or argument that is not a lowercase word.
var commandRe = regexp.MustCompile("`aigw ([a-z][a-z-]*(?: [a-z][a-z-]*)*)")

// unknownCommands returns each `aigw ...` in text whose whole word path is not
// a command in known.
func unknownCommands(text string, known map[string]bool) []string {
	var bad []string

	for _, m := range commandRe.FindAllStringSubmatch(text, -1) {
		if !known[m[1]] {
			bad = append(bad, "aigw "+m[1])
		}
	}

	return bad
}

// --body is for another tool's instructions file: no frontmatter, and no note
// that `aigw skill install` owns the file.
func TestSkillShowBody(t *testing.T) {
	t.Parallel()

	full, _ := run(t, "", "skill", "show")

	body, err := run(t, "", "skill", "show", "--body")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(body, "# aigw\n") {
		t.Errorf("body starts %q", body[:min(len(body), 40)])
	}

	for _, gone := range []string{"name: aigw", "description:", "Written by aigw", "edits are lost"} {
		if strings.Contains(body, gone) {
			t.Errorf("body still has %q", gone)
		}
	}

	if !strings.HasSuffix(full, body) {
		t.Error("body is not the tail of the full skill")
	}
}
