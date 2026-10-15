// Package skill holds the Claude Code skill that teaches an agent to drive
// aigw, and installs it where Claude Code looks for personal skills.
//
// The skill ships inside the binary, so the installed copy always matches the
// commands this aigw has. `aigw skill install` after an upgrade refreshes it.
package skill

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed SKILL.md
var template string

// marker is in every file Render writes. A SKILL.md without it is someone
// else's, and is never replaced or removed without --confirm.
const marker = "<!-- Written by aigw "

// Name is the skill's directory name and its frontmatter name.
const Name = "aigw"

// Render returns SKILL.md stamped with the aigw version that wrote it.
func Render(version string) string {
	return strings.ReplaceAll(template, "{{version}}", version)
}

// Body returns the skill without its frontmatter and the install note, for
// another tool's instructions file (e.g. AGENTS.md), where neither applies.
func Body(version string) string {
	s := Render(version)

	// The frontmatter is the block between the first two "---" lines.
	if rest, ok := strings.CutPrefix(s, "---\n"); ok {
		if _, after, found := strings.Cut(rest, "\n---\n"); found {
			s = after
		}
	}

	var kept []string

	for line := range strings.Lines(s) {
		if !strings.HasPrefix(line, marker) {
			kept = append(kept, line)
		}
	}

	return strings.TrimLeft(strings.Join(kept, ""), "\n")
}

// Dir is where Claude Code reads the skill: $CLAUDE_CONFIG_DIR/skills/aigw
// when that is set, as Claude Code itself does, else ~/.claude/skills/aigw.
func Dir() (string, error) {
	base := os.Getenv("CLAUDE_CONFIG_DIR")
	if base == "" || !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}

		base = filepath.Join(home, ".claude")
	}

	return filepath.Join(base, "skills", Name), nil
}

// Result says what Install or Uninstall did.
type Result int

// Outcomes of Install and Uninstall.
const (
	Installed Result = iota
	Updated
	UpToDate
	Removed
	NotInstalled
)

// ErrNotOurs means a SKILL.md that aigw did not write is in the way.
var ErrNotOurs = errors.New("a SKILL.md that aigw did not write is there")

// Install writes content to dir/SKILL.md. It replaces a copy aigw wrote, and
// one it did not only when replaceForeign is set.
func Install(dir, content string, replaceForeign bool) (Result, error) {
	path := filepath.Join(dir, "SKILL.md")

	old, err := os.ReadFile(path)

	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return 0, fmt.Errorf("read %s: %w", path, err)
	case string(old) == content:
		return UpToDate, nil
	case !strings.Contains(string(old), marker) && !replaceForeign:
		return 0, fmt.Errorf("%w: %s", ErrNotOurs, path)
	}

	err = os.MkdirAll(dir, 0o755)
	if err != nil {
		return 0, fmt.Errorf("create %s: %w", dir, err)
	}

	// Through a temp file, so Claude Code never reads half a skill.
	f, err := os.CreateTemp(dir, ".SKILL-*.md")
	if err != nil {
		return 0, fmt.Errorf("write %s: %w", path, err)
	}

	_, err = f.WriteString(content)
	if err == nil {
		err = f.Chmod(0o644)
	}

	if closeErr := f.Close(); err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(f.Name(), path)
	}

	if err != nil {
		_ = os.Remove(f.Name())

		return 0, fmt.Errorf("write %s: %w", path, err)
	}

	if old == nil {
		return Installed, nil
	}

	return Updated, nil
}

// Uninstall removes dir/SKILL.md, and dir when that leaves it empty. It
// removes a SKILL.md aigw did not write only when removeForeign is set.
func Uninstall(dir string, removeForeign bool) (Result, error) {
	path := filepath.Join(dir, "SKILL.md")

	old, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return NotInstalled, nil
	}

	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}

	if !strings.Contains(string(old), marker) && !removeForeign {
		return 0, fmt.Errorf("%w: %s", ErrNotOurs, path)
	}

	err = os.Remove(path)
	if err != nil {
		return 0, fmt.Errorf("remove %s: %w", path, err)
	}

	// Only if empty: anything else in it is not ours to delete.
	_ = os.Remove(dir)

	return Removed, nil
}
