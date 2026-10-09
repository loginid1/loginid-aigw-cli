package credentials

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// In-package and not parallel: it shortens the package-level timeout.
func TestWithTimeoutGivesUp(t *testing.T) {
	old := keyringTimeout
	keyringTimeout = 20 * time.Millisecond

	t.Cleanup(func() { keyringTimeout = old })

	release := make(chan struct{})
	defer close(release)

	start := time.Now()

	_, err := withTimeout(func() (string, error) {
		<-release // a store that never answers

		return "late", nil
	})
	if err == nil || !strings.Contains(err.Error(), "no answer after") {
		t.Fatalf("want a timeout error, got %v", err)
	}

	if waited := time.Since(start); waited > time.Second {
		t.Errorf("gave up after %s, want about %s", waited, keyringTimeout)
	}

	got, err := withTimeout(func() (string, error) { return "key", errors.New("boom") })
	if got != "key" || err == nil || err.Error() != "boom" {
		t.Errorf("a prompt answer must pass through unchanged: %q, %v", got, err)
	}
}

// Not parallel: it swaps package-level hooks. A save that times out names the
// store once, and records the profile so logout can remove a key that lands
// after we stopped waiting.
func TestSaveTimeoutLeavesNoOrphan(t *testing.T) {
	keyring.MockInit()

	oldTimeout, oldSet := keyringTimeout, setInKeyring
	keyringTimeout = 20 * time.Millisecond

	release, landed := make(chan struct{}), make(chan struct{})
	setInKeyring = func(service, user, password string) error {
		<-release // answers only after we have given up

		defer close(landed)

		return keyring.Set(service, user, password)
	}

	t.Cleanup(func() { keyringTimeout, setInKeyring = oldTimeout, oldSet })

	dir := t.TempDir()

	_, err := Save(dir, "default", "aigw_sk_late123456", false)
	if err == nil || !strings.Contains(err.Error(), "may still have saved") ||
		strings.Count(err.Error(), storeName()) != 1 {
		t.Fatalf("timeout error %q", err)
	}

	close(release) // the key lands late
	<-landed       // the in-memory mock is not safe to read while it writes

	deleted, err := Delete(dir, "default")
	if err != nil || !deleted.Removed {
		t.Fatalf("logout after the timeout: %+v, %v", deleted, err)
	}

	_, err = keyring.Get(keyringService, "default")
	if !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("the late key is still in the keyring: %v", err)
	}
}
