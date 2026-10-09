package credentials

import (
	"errors"
	"fmt"
	"time"

	"github.com/zalando/go-keyring"
)

// keyringTimeout bounds each call to the OS store. On Linux a missing or hung
// D-Bus daemon can block forever; the Stripe CLI guards against the same thing
// with 3s. This is longer because the macOS Keychain may show an unlock
// dialog, and the user needs time to type a password into it.
var keyringTimeout = 10 * time.Second

// setInKeyring is keyring.Set; a test swaps it for one that hangs.
var setInKeyring = keyring.Set

func keyringSet(profile, key string) error {
	_, err := withTimeout(func() (struct{}, error) {
		return struct{}{}, setInKeyring(keyringService, profile, key)
	})

	return err
}

func keyringGet(profile string) (string, error) {
	return withTimeout(func() (string, error) { return keyring.Get(keyringService, profile) })
}

func keyringDelete(profile string) error {
	_, err := withTimeout(func() (struct{}, error) {
		return struct{}{}, keyring.Delete(keyringService, profile)
	})

	return err
}

// errTimeout means the OS store did not answer in time. The call may still
// complete after we stop waiting.
var errTimeout = errors.New("no answer")

type result[T any] struct {
	val T
	err error
}

// withTimeout runs fn and gives up after keyringTimeout. The value travels
// over the channel, so a call that gave up shares nothing with the caller; it
// keeps running in its goroutine, and the CLI exits soon after without
// waiting for it.
func withTimeout[T any](fn func() (T, error)) (T, error) {
	done := make(chan result[T], 1)

	go func() {
		v, err := fn()
		done <- result[T]{v, err}
	}()

	select {
	case r := <-done:
		return r.val, r.err
	case <-time.After(keyringTimeout):
		var zero T

		// Callers add what they were doing and to which store.
		return zero, fmt.Errorf("%w after %s", errTimeout, keyringTimeout)
	}
}
