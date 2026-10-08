package client_test

import (
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain swaps in go-keyring's in-memory store, so no test in this package
// can reach the real OS keyring, even through a future bug.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}
