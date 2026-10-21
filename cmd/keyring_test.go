package cmd_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/cmd"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/apicmd"
)

// Not parallel: it swaps the keyring mock and sets env vars. Abithan's review:
// a credential-store failure must exit 3, like "not logged in", not 1.
func TestKeyringFailureExitsNotAuthenticated(t *testing.T) {
	keyring.MockInit()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AIGW_API_KEY", "")

	_, err := run(t, "aigw_sk_saved", "login", "--with-key-stdin")
	if err != nil {
		t.Fatal(err)
	}

	keyring.MockInitWithError(errors.New("keyring is locked"))
	t.Cleanup(keyring.MockInit)

	_, err = run(t, "", "projects", "list")
	if cmd.ExitCode(err) != apicmd.ExitNotAuthenticated || !strings.Contains(err.Error(), "keyring is locked") {
		t.Errorf("a locked keyring: exit %d, %v", cmd.ExitCode(err), err)
	}
}
