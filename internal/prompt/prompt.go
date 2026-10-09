// Package prompt decides whether aigw may ask the user something, and words
// the refusal when it may not.
package prompt

import (
	"io"
	"os"
	"runtime"

	"golang.org/x/term"
)

// IsTerminal reports whether in is an interactive terminal. It asks the OS: a
// character-device check is not enough, since /dev/null is one too.
func IsTerminal(in io.Reader) bool {
	f, ok := in.(*os.File)

	return ok && term.IsTerminal(int(f.Fd()))
}

// NotTerminalHint adds advice to a "not a terminal" error. Git Bash (mintty)
// on Windows runs programs behind pipes, so a real terminal looks like none;
// the fix there is winpty, not --with-key-stdin, which would echo the key.
func NotTerminalHint() string {
	if runtime.GOOS == "windows" && os.Getenv("MSYSTEM") != "" {
		return " (in Git Bash, run aigw through winpty, e.g. `winpty aigw login`, or use Windows Terminal)"
	}

	return ""
}
