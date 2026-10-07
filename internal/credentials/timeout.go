package credentials

import (
	"fmt"
	"time"

	"github.com/zalando/go-keyring"
)

// keyringTimeout bounds each call to the OS store. On Linux a missing or hung
// D-Bus daemon can block forever; the Stripe CLI guards against the same thing
// with 3s. This is longer because the macOS Keychain may show an unlock
// dialog, and the user needs time to type a password into it.
var keyringTimeout = 10 * time.Second

func keyringSet(profile, key string) error {
	_, err := withTimeout("save to", func() (struct{}, error) {
		return struct{}{}, keyring.Set(keyringService, profile, key)
	})

	return err
}

func keyringGet(profile string) (string, error) {
	return withTimeout("read from", func() (string, error) { return keyring.Get(keyringService, profile) })
}

func keyringDelete(profile string) error {
	_, err := withTimeout("delete from", func() (struct{}, error) {
		return struct{}{}, keyring.Delete(keyringService, profile)
	})

	return err
}

type result[T any] struct {
	val T
	err error
}

// withTimeout runs fn and gives up after keyringTimeout. The value travels
// over the channel, so a call that gave up shares nothing with the caller; it
// keeps running in its goroutine, and the CLI exits soon after without
// waiting for it.
func withTimeout[T any](verb string, fn func() (T, error)) (T, error) {
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

		return zero, fmt.Errorf("%s %s: no answer after %s", verb, storeName(), keyringTimeout)
	}
}
