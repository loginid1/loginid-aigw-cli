// Package version holds build-time identity for the aigw binary.
//
// Both values are overwritten at link time by the Makefile; the defaults below
// are what a plain `go build` or `go run` produces.
package version

var (
	// Version is the CLI version, injected at build time.
	Version = "latest"
	// GitCommit is the commit hash the binary was built from, injected at build time.
	GitCommit = "HEAD"
)
