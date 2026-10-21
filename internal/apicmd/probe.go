package apicmd

import (
	"context"
	"errors"
	"slices"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/client"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/config"
)

// APIBase is where requests go. It is DefaultAPIBase unless the build sets it
// with -ldflags -X, e.g. `make build API_BASE=...` for another environment. It
// is never read from a flag or the environment: a key sent to a URL a cloned
// repo's env chose would leak. The env can still reach the key in ways no CLI
// setting closes: Go honors HTTPS_PROXY, and on Linux SSL_CERT_FILE and
// SSL_CERT_DIR, so a hostile proxy can be trusted; and PATH can run another
// aigw outright. Tests replace it to point at a fake server.
var APIBase = DefaultAPIBase

// Profile names the saved key for APIBase. A build for another environment
// keeps its key under its base URL, so its login never replaces the
// production key and its commands never send it.
func Profile() string {
	if APIBase == DefaultAPIBase {
		return config.DefaultProfile
	}

	return APIBase
}

// probeOperation is the call ProbeKey makes. It needs only project:read.
const probeOperation = "projects#list"

// ProbeKey checks key against base with the cheapest call there is, listing
// projects. The error carries the exit code the CLI gives that failure.
func ProbeKey(ctx context.Context, base, key string) error {
	i := slices.IndexFunc(Operations, func(op Operation) bool { return op.ID == probeOperation })
	if i < 0 {
		return errors.New("no " + probeOperation + " operation to check the key with")
	}

	op := Operations[i]

	_, err := client.New(base, key).Do(ctx, &client.Request{Method: op.Method, Path: op.Path, Scope: op.Scope})

	return ExitErrorFor(err)
}
