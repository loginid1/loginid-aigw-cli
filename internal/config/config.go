// Package config reads and writes ~/.config/aigw/config.toml.
//
// The file never holds a secret. A profile records where its key is stored and
// a hint to tell keys apart; the key itself lives in the OS credential store,
// or, only when the user asked for it, in a separate 0600 file (see
// internal/credentials).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/BurntSushi/toml"
)

// DefaultProfile is the only profile until -p lands.
const DefaultProfile = "default"

// Where a profile's key is kept.
const (
	StorageKeyring = "keyring"
	StorageFile    = "file"
)

// Profile is one named login.
type Profile struct {
	// Storage is StorageKeyring or StorageFile.
	Storage string `toml:"storage"`
	// KeyHint is the key with its secret part masked, e.g. "aigw_sk_…Xk7Q".
	KeyHint string `toml:"key_hint"`
}

// Config is the whole file, keyed by profile name, so `[default]` reads as a
// table of its own.
type Config map[string]Profile

// Dir is $XDG_CONFIG_HOME/aigw, or ~/.config/aigw when that is unset. The same
// path is used on every OS, as the gh and Stripe CLIs do, so docs can name it.
//
// A relative XDG_CONFIG_HOME is ignored, as the XDG spec requires: it would
// put the credentials file inside whatever directory aigw runs in, where it
// can end up committed.
func Dir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" && filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "aigw"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}

	return filepath.Join(home, ".config", "aigw"), nil
}

func path(dir string) string { return filepath.Join(dir, FileName) }

// Load reads the config in dir. A missing file is an empty config.
func Load(dir string) (Config, error) {
	cfg := Config{}

	_, err := toml.DecodeFile(path(dir), &cfg)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}

	// Permission is the user's to fix. Anything else (not TOML, the wrong
	// shape, a directory where the file should be) logout can reset.
	if errors.Is(err, fs.ErrPermission) {
		return nil, fmt.Errorf("read %s: %w", path(dir), err)
	}

	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w; run `aigw logout` to reset it", ErrUnreadable, path(dir), err)
	}

	return cfg, nil
}

// ErrUnreadable means config.toml exists but cannot be used.
var ErrUnreadable = errors.New("config file is unreadable")

// MoveAside renames file in dir to file.broken-<time>, so the next write
// starts clean while the old content stays for a look. The time keeps an
// earlier broken copy from being overwritten. It returns the new path, or ""
// when there was no file.
func MoveAside(dir, file string) (string, error) {
	src := filepath.Join(dir, file)
	dest := src + ".broken-" + time.Now().Format("20060102-150405.000")

	err := os.Rename(src, dest)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("move %s aside: %w", src, err)
	}

	return dest, nil
}

// FileName is config.toml's name inside Dir.
const FileName = "config.toml"

// Save writes cfg to dir, replacing the file in one step so a crash cannot
// leave half a config behind.
func Save(dir string, cfg Config) error {
	f, err := CreateTemp(dir)
	if err != nil {
		return err
	}

	err = toml.NewEncoder(f).Encode(cfg)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())

		return fmt.Errorf("write config: %w", err)
	}

	return Commit(f, path(dir))
}

// CreateTemp makes dir with owner-only permissions and opens a 0600 temp file
// in it, for Commit to move into place.
func CreateTemp(dir string) (*os.File, error) {
	err := os.MkdirAll(dir, 0o700)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}

	// MkdirAll leaves an existing directory as it is. If others can write to
	// it, they can rename their own config and key files over these, so
	// tighten it before writing anything into it. Windows has no mode bits:
	// there, the user folder's ACLs keep others out.
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("check %s: %w", dir, err)
	}

	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		err = os.Chmod(dir, 0o700)
		if err != nil {
			return nil, fmt.Errorf("%s is accessible to other users and could not be restricted: %w", dir, err)
		}
	}

	// CreateTemp opens with 0600.
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("create temp file in %s: %w", dir, err)
	}

	return f, nil
}

// Commit closes f and renames it to dest.
func Commit(f *os.File, dest string) error {
	err := f.Close()
	if err == nil {
		err = os.Rename(f.Name(), dest)
	}

	if err != nil {
		_ = os.Remove(f.Name())

		return fmt.Errorf("write %s: %w", dest, err)
	}

	return nil
}
