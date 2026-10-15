package cmd

import "time"

// SetLoginCheckTimeout lets tests shorten login's key check, and returns a
// func that restores it.
func SetLoginCheckTimeout(d time.Duration) func() {
	prev := loginCheckTimeout
	loginCheckTimeout = d

	return func() { loginCheckTimeout = prev }
}
