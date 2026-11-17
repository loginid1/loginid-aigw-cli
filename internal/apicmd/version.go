package apicmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/spf13/cobra"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/client"
)

// aigw refuses a write that was made from a stale read (lost-update protection,
// loginid-aigw II-420). On this API the version is optional: a write that sends
// none is not checked. One row's write (PUT, PATCH, DELETE) sends the updatedAt
// it read in the X-Expected-Updated-At header; a list's write sends `seen`, one
// {issuerName, updatedAt} per entry it held, in the body. A row never updated
// has no updatedAt, so its write sends none, and a seen entry for it says "".
const (
	// stampHeader is the header a row's write sends its version in.
	stampHeader = "X-Expected-Updated-At"
	seenField   = "seen"
	// seenIDField is the key of an entry's id in the list the GET returns.
	// shortcut: the kya-issuer lists are the only versioned lists the CLI has,
	// and their ids are issuerNames; read it from the spec if another one comes.
	seenIDField    = "issuerName"
	seenStampField = "updatedAt"
	// noVersion is how a caller says the row was never updated. A header cannot
	// carry null, so it is sent as no header, which the API does not check.
	noVersion = "null"
)

// expectedFlagHelp replaces the spec's text, which is written for API callers.
const expectedFlagHelp = "The updatedAt you read. Optional: the CLI reads the current one just before writing. " +
	"Pass it to have the write refused (409 conflict) if the object changed since you read it. " +
	"Pass null, or nothing, for an object that was never updated."

// isVersionInput reports whether p is the version a row's write sends, which
// the CLI fills in itself and so never asks for.
func isVersionInput(p *Param) bool { return p.In == InHeader && p.Name == stampHeader }

// fillVersions sets the versions a write needs when the caller gave none, from
// a read of the same path just before it. A caller who did set one keeps it. A
// build without lost-update protection ignores both, so the CLI sends them to
// every server.
func fillVersions(ctx context.Context, cmd *cobra.Command, op *Operation, c *client.Client, req *client.Request, body map[string]any) error {
	one := false

	for _, p := range op.Params {
		if !isVersionInput(&p) {
			continue
		}

		one = true

		if cmd.Flags().Changed(p.Flag) {
			given, _ := cmd.Flags().GetString(p.Flag)
			setStamp(req, given)

			one = false
		}
	}

	_, list := op.Fields[seenField+"[]."+seenIDField]
	if list {
		if _, set := body[seenField]; set {
			list = false
		}
	}

	if !one && !list {
		return nil
	}

	current, err := readCurrent(ctx, op, c, req)
	if err != nil {
		return fmt.Errorf("read the current version before writing (or pass it with --expected-updated-at): %w", ExitErrorFor(err))
	}

	if one {
		if s, ok := updatedAt(current).(string); ok {
			setStamp(req, s)
		}
	}

	if list {
		body[seenField] = seenOf(current)
	}

	return nil
}

// setStamp sends v as the write's version header. Empty, and "null", send none.
func setStamp(req *client.Request, v string) {
	if v == "" || v == noVersion {
		return
	}

	if req.Header == nil {
		req.Header = map[string]string{}
	}

	req.Header[stampHeader] = v
}

// readCurrent is the GET on op's path, decoded. A 404 is nil: the object was
// never saved, which a write states with no version.
func readCurrent(ctx context.Context, op *Operation, c *client.Client, req *client.Request) (any, error) {
	body, err := getSamePath(ctx, op, c, req)
	if err != nil {
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return nil, nil
		}

		return nil, err
	}

	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}

	var current any

	err = json.Unmarshal(body, &current)
	if err != nil {
		return nil, fmt.Errorf("decode the current version: %w", err)
	}

	return current, nil
}

// errNoGet means op's path has no GET to read the current version from.
var errNoGet = errors.New("this path has no get command")

// getSamePath runs the GET on op's path with req's path parameters.
func getSamePath(ctx context.Context, op *Operation, c *client.Client, req *client.Request) ([]byte, error) {
	i := slices.IndexFunc(Operations, func(o Operation) bool {
		return o.Method == http.MethodGet && o.Path == op.Path
	})
	if i < 0 {
		return nil, errNoGet
	}

	return c.Do(ctx, &client.Request{
		Method:     http.MethodGet,
		Path:       Operations[i].Path,
		PathParams: req.PathParams,
		Scope:      Operations[i].Scope,
	})
}

// updatedAt is v's updatedAt, or nil when it has none: never updated, or a build
// that does not report it.
func updatedAt(v any) any {
	m, _ := v.(map[string]any)
	if s, ok := m[seenStampField].(string); ok && s != "" {
		return s
	}

	return nil
}

// seenOf is the seen list for the entries v holds. An empty list says the scope
// held none. An entry never updated says "".
func seenOf(v any) []any {
	items, _ := v.([]any)
	out := make([]any, 0, len(items))

	for _, item := range items {
		m, _ := item.(map[string]any)
		if id, ok := m[seenIDField].(string); ok && id != "" {
			stamp, _ := updatedAt(m).(string)
			out = append(out, map[string]any{seenIDField: id, seenStampField: stamp})
		}
	}

	return out
}
