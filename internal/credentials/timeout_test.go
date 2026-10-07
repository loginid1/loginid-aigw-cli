package credentials

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// In-package and not parallel: it shortens the package-level timeout.
func TestWithTimeoutGivesUp(t *testing.T) {
	old := keyringTimeout
	keyringTimeout = 20 * time.Millisecond

	t.Cleanup(func() { keyringTimeout = old })

	release := make(chan struct{})
	defer close(release)

	_, err := withTimeout("read from", func() (string, error) {
		<-release // a store that never answers

		return "late", nil
	})
	if err == nil || !strings.Contains(err.Error(), "no answer after") {
		t.Fatalf("want a timeout error, got %v", err)
	}

	got, err := withTimeout("read from", func() (string, error) { return "key", errors.New("boom") })
	if got != "key" || err == nil || err.Error() != "boom" {
		t.Errorf("a prompt answer must pass through unchanged: %q, %v", got, err)
	}
}
