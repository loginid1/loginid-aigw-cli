package apicmd

import (
	"context"
	"errors"
	"slices"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/client"
)

// APIBase is where requests go. It is DefaultAPIBase unless the build sets it
// with -ldflags -X, e.g. `make build API_BASE=...` for another environment. It
// is never read from a flag or the environment: a key sent to a URL a cloned
// repo's env chose would leak. Tests replace it to point at a fake server.
var APIBase = DefaultAPIBase

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
