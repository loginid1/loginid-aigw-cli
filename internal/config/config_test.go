package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/config"
)

// Not parallel: these set env vars.
func TestDirIgnoresRelativeXDG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows

	t.Setenv("XDG_CONFIG_HOME", "rel")

	got, err := config.Dir()
	if err != nil || got != filepath.Join(home, ".config", "aigw") {
		t.Errorf("relative XDG_CONFIG_HOME: %q, %v", got, err)
	}

	abs := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", abs)

	got, err = config.Dir()
	if err != nil || got != filepath.Join(abs, "aigw") {
		t.Errorf("absolute XDG_CONFIG_HOME: %q, %v", got, err)
	}
}

func TestSaveRestrictsTheDirectory(t *testing.T) {
	t.Parallel()

	for name, existing := range map[string]os.FileMode{"new": 0, "existing 0777": 0o777} {
		dir := filepath.Join(t.TempDir(), "aigw")

		if existing != 0 {
			err := os.Mkdir(dir, existing)
			if err == nil {
				err = os.Chmod(dir, existing) // past the umask
			}

			if err != nil {
				t.Fatal(err)
			}
		}

		err := config.Save(dir, config.Config{"default": {Storage: config.StorageKeyring}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}

		// Windows has no mode bits to check.
		if perm := info.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o700 {
			t.Errorf("%s: directory mode %o, want 700", name, perm)
		}

		cfg, err := config.Load(dir)
		if err != nil || cfg["default"].Storage != config.StorageKeyring {
			t.Errorf("%s: round trip: %v, %v", name, cfg, err)
		}
	}
}
