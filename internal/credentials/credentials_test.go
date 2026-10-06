package credentials_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	removed, err := credentials.Delete(dir, config.DefaultProfile)
	if !removed || err != nil {
		t.Fatalf("Delete = %v, %v", removed, err)
	}

	_, err = keyring.Get("aigw", config.DefaultProfile)
	if !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("key still in keyring after Delete: %v", err)
	}

	removed, _ = credentials.Delete(dir, config.DefaultProfile)
	if removed {
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

	perm := info.Mode().Perm()
	if perm != 0o600 {
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
	}
	for in, want := range cases {
		got := credentials.Hint(in)
		if got != want {
			t.Errorf("Hint(%q) = %q, want %q", in, got, want)
		}
	}
}
