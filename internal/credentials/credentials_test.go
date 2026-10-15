package credentials_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/config"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/credentials"
)

const key = "aigw_sk_Xk7Qb2vR9sT4wY6zA1cD3eF5"

// Not parallel: keyring.MockInit swaps a package-level store.
func TestKeyringRoundTrip(t *testing.T) {
	keyring.MockInit()

	dir := t.TempDir()

	_, err := credentials.Load(dir, config.DefaultProfile)
	if !errors.Is(err, credentials.ErrNotLoggedIn) {
		t.Fatalf("before login: want ErrNotLoggedIn, got %v", err)
	}

	_, err = credentials.Save(dir, config.DefaultProfile, key, false)
	if err != nil {
		t.Fatal(err)
	}

	got, err := credentials.Load(dir, config.DefaultProfile)
	if err != nil || got != key {
		t.Fatalf("Load = %q, %v", got, err)
	}

	// The config names the store and a hint, never the key.
	raw, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if strings.Contains(string(raw), key) || !strings.Contains(string(raw), `storage = "keyring"`) {
		t.Errorf("config.toml:\n%s", raw)
	}

	deleted, err := credentials.Delete(dir, config.DefaultProfile)
	if !deleted.Removed || err != nil {
		t.Fatalf("Delete = %+v, %v", deleted, err)
	}

	_, err = keyring.Get("aigw", config.DefaultProfile)
	if !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("key still in keyring after Delete: %v", err)
	}

	deleted, _ = credentials.Delete(dir, config.DefaultProfile)
	if deleted.Removed {
		t.Error("second Delete reported a removal")
	}
}

func TestNoKeyringFailsWithoutFallback(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))

	dir := t.TempDir()

	_, err := credentials.Save(dir, config.DefaultProfile, key, false)
	if err == nil || !strings.Contains(err.Error(), "--insecure-storage") {
		t.Fatalf("want an error pointing at --insecure-storage, got %v", err)
	}

	// Nothing may be written as a quiet fallback.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("files written after a failed save: %v", entries)
	}
}

func TestInsecureFileStorage(t *testing.T) {
	keyring.MockInitWithError(errors.New("must not be used"))

	dir := t.TempDir()

	_, err := credentials.Save(dir, config.DefaultProfile, key, true)
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, "credentials.toml"))
	if err != nil {
		t.Fatal(err)
	}

	// Windows has no mode bits to check.
	perm := info.Mode().Perm()
	if runtime.GOOS != "windows" && perm != 0o600 {
		t.Errorf("credentials.toml mode = %o, want 600", perm)
	}

	got, _ := credentials.Load(dir, config.DefaultProfile)
	if got != key {
		t.Errorf("Load = %q", got)
	}

	_, err = credentials.Delete(dir, config.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(dir, "credentials.toml"))
	if !os.IsNotExist(err) {
		t.Errorf("credentials.toml left behind after logout: %v", err)
	}
}

func TestHint(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		key:                 "aigw_sk_…3eF5",
		"aigw_sk_short":     "…",
		"other-key-abcdef1": "…def1",
		"\u65e5\u672c\u8a9e\u65e5\u672c\u8a9e\u65e5\u672c\u8a9e\u65e5\u672c\u8a9e": "\u2026\u8a9e\u65e5\u672c\u8a9e", // whole characters, never half of one
	}
	for in, want := range cases {
		got := credentials.Hint(in)
		if got != want {
			t.Errorf("Hint(%q) = %q, want %q", in, got, want)
		}
	}
}

// Not parallel: keyring.MockInit swaps a package-level store.
func TestSwitchingStorageRemovesTheOldKey(t *testing.T) {
	keyring.MockInit()

	dir := t.TempDir()

	_, err := credentials.Save(dir, config.DefaultProfile, key, false)
	if err != nil {
		t.Fatal(err)
	}

	_, err = credentials.Save(dir, config.DefaultProfile, key, true)
	if err != nil {
		t.Fatal(err)
	}

	_, err = keyring.Get("aigw", config.DefaultProfile)
	if !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("keyring still holds the key after switching to a file: %v", err)
	}

	_, err = credentials.Save(dir, config.DefaultProfile, key, false)
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(dir, "credentials.toml"))
	if !os.IsNotExist(err) {
		t.Errorf("credentials.toml left behind after switching back to the keyring: %v", err)
	}
}

// Not parallel: keyring.MockInit swaps a package-level store.
func TestDeleteRecoversFromAnUnreadableConfig(t *testing.T) {
	keyring.MockInit()

	dir := t.TempDir()

	_, err := credentials.Save(dir, config.DefaultProfile, key, false)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[default\nbroken"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = credentials.Load(dir, config.DefaultProfile)
	if !errors.Is(err, config.ErrUnreadable) || !strings.Contains(err.Error(), "aigw logout") {
		t.Fatalf("want ErrUnreadable pointing at logout, got %v", err)
	}

	deleted, err := credentials.Delete(dir, config.DefaultProfile)
	if err != nil || !deleted.Removed || len(deleted.MovedAside) != 1 ||
		!strings.HasPrefix(deleted.MovedAside[0], filepath.Join(dir, "config.toml.broken-")) {
		t.Fatalf("Delete = %+v, %v", deleted, err)
	}

	_, err = keyring.Get("aigw", config.DefaultProfile)
	if !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("key still in the keyring: %v", err)
	}

	// The next login starts clean.
	_, err = credentials.Save(dir, config.DefaultProfile, key, false)
	if err != nil {
		t.Errorf("login after the reset: %v", err)
	}
}

// Not parallel: keyring.MockInit swaps a package-level store.
func TestResetCoversEveryBrokenState(t *testing.T) {
	cases := []struct {
		name       string
		config     string
		configDir  bool   // a directory where config.toml should be
		creds      string // credentials.toml content, if any
		noKeyring  bool   // a machine with no usable keyring
		wantMoved  int
		wantWarned bool
	}{
		{name: "valid TOML, wrong shape", config: "default = 5\n", wantMoved: 1},
		{name: "a directory, not a file", configDir: true, wantMoved: 1},
		{name: "no keyring on the machine", config: "[default\n", noKeyring: true, wantMoved: 1, wantWarned: true},
		{name: "both files broken", config: "[default\n", creds: "[[[", wantMoved: 2},
	}

	for _, tc := range cases {
		if tc.noKeyring {
			keyring.MockInitWithError(errors.New("no secret service"))
		} else {
			keyring.MockInit()
		}

		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.toml")

		var err error
		if tc.configDir {
			err = os.Mkdir(cfgPath, 0o700)
		} else {
			err = os.WriteFile(cfgPath, []byte(tc.config), 0o600)
		}

		if err == nil && tc.creds != "" {
			err = os.WriteFile(filepath.Join(dir, "credentials.toml"), []byte(tc.creds), 0o600)
		}

		if err != nil {
			t.Fatal(err)
		}

		deleted, err := credentials.Delete(dir, config.DefaultProfile)
		if err != nil || !deleted.Removed || len(deleted.MovedAside) != tc.wantMoved || (len(deleted.Warnings) > 0) != tc.wantWarned {
			t.Errorf("%s: Delete = %+v, %v", tc.name, deleted, err)

			continue
		}

		// Afterwards a login works again.
		_, err = credentials.Save(dir, config.DefaultProfile, key, true)
		if err != nil {
			t.Errorf("%s: login after the reset: %v", tc.name, err)
		}
	}
}

func TestMoveAsideKeepsEarlierCopies(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	seen := map[string]bool{}

	for range 2 {
		err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("x"), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		moved, err := config.MoveAside(dir, "config.toml")
		if err != nil || moved == "" || seen[moved] {
			t.Fatalf("MoveAside = %q, %v (earlier: %v)", moved, err, seen)
		}

		seen[moved] = true

		time.Sleep(2 * time.Millisecond) // names carry milliseconds
	}
}

// Not parallel: keyring.MockInit swaps a package-level store.
func TestStorageSwitchFailures(t *testing.T) {
	// The old key cannot be removed: say so, but keep the new one.
	keyring.MockInit()

	dir := t.TempDir()

	_, err := credentials.Save(dir, config.DefaultProfile, key, false)
	if err != nil {
		t.Fatal(err)
	}

	keyring.MockInitWithError(errors.New("keyring locked"))

	_, err = credentials.Save(dir, config.DefaultProfile, key, true)
	if err == nil || !strings.Contains(err.Error(), "old one is still") {
		t.Errorf("an undeletable old key was not reported: %v", err)
	}

	got, err := credentials.Load(dir, config.DefaultProfile)
	if err != nil || got != key {
		t.Errorf("the new key was lost: %q, %v", got, err)
	}

	// The new store refuses the key: the old one must still work.
	dir = t.TempDir()

	_, err = credentials.Save(dir, config.DefaultProfile, key, true)
	if err != nil {
		t.Fatal(err)
	}

	_, err = credentials.Save(dir, config.DefaultProfile, "aigw_sk_replacement123", false)
	if err == nil {
		t.Fatal("saving to a failing keyring succeeded")
	}

	got, err = credentials.Load(dir, config.DefaultProfile)
	if err != nil || got != key {
		t.Errorf("a failed switch lost the old key: %q, %v", got, err)
	}
}
