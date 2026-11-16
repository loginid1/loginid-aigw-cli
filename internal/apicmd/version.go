package apicmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/client"
)

// aigw refuses a write that does not say which version it was made from
// (lost-update protection, loginid-aigw II-420): one object's write sends
// expectedUpdatedAt, the updatedAt it read, or null for one never saved; a
// list's write sends versions, one {id, expectedUpdatedAt} per item it held.
const (
	expectedField = "expectedUpdatedAt"
	versionsField = "versions"
	// versionsIDField is the key of a versions id in the list the GET returns.
	// shortcut: the kya-issuer lists are the only versioned lists the CLI has,
	// and their ids are issuerNames; read it from the spec if another one comes.
	versionsIDField = "issuerName"
	nullValue       = "null"
)

// expectedFlagHelp replaces the spec's text, which is written for API callers
// who must always send the field.
const expectedFlagHelp = "The updatedAt you read, or null for an object never saved. " +
	"Optional: the CLI reads the current one just before writing. " +
	"Pass it to have the write refused (409 conflict) if the object changed since you read it."

// isVersionInput reports whether p is the version a write sends, which the CLI
// fills in itself and so never asks for.
func isVersionInput(p *Param) bool { return p.Name == expectedField }

// fillVersions sets the versions a write needs when the caller gave none, from
// a read of the same path just before it. A caller who did set one keeps it,
// with "null" sent as JSON null. Builds without lost-update protection ignore
// both fields, so the CLI sends them to every server.
func fillVersions(ctx context.Context, op *Operation, c *client.Client, req *client.Request, body map[string]any) error {
	_, one := op.Fields[expectedField]
	_, list := op.Fields[versionsField+"[].id"]

	if one {
		if v, set := body[expectedField]; set {
			body[expectedField] = nullable(v)
			one = false
		}
	}

	if list {
		if items, set := body[versionsField].([]any); set {
			for _, item := range items {
				if m, ok := item.(map[string]any); ok {
					if v, set := m[expectedField]; set {
						m[expectedField] = nullable(v)
					}
				}
			}

			list = false
		}
	}

	if !one && !list {
		return nil
	}

	current, err := readCurrent(ctx, op, c, req)
	if err != nil {
		return fmt.Errorf("read the current version before writing (or pass it with --%s or -d): %w", kebab(expectedField), ExitErrorFor(err))
	}

	if one {
		body[expectedField] = updatedAt(current)
	}

	if list {
		body[versionsField] = versionsOf(current)
	}

	return nil
}

// readCurrent is the GET on op's path, decoded. A 404 is nil: the object was
// never saved, which a write states with a null version.
func readCurrent(ctx context.Context, op *Operation, c *client.Client, req *client.Request) (any, error) {
	body, err := getSamePath(ctx, op, c, req)
	if err != nil {
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return nil, nil
		}

		return nil, err
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

// updatedAt is v's updatedAt, or nil when it has none: never saved, or a build
// that does not report it.
func updatedAt(v any) any {
	m, _ := v.(map[string]any)
	if s, ok := m["updatedAt"].(string); ok && s != "" {
		return s
	}

	return nil
}

// versionsOf is the versions list for the items v holds. An empty list says the
// scope held none.
func versionsOf(v any) []any {
	items, _ := v.([]any)
	out := make([]any, 0, len(items))

	for _, item := range items {
		m, _ := item.(map[string]any)
		if id, ok := m[versionsIDField].(string); ok && id != "" {
			out = append(out, map[string]any{"id": id, expectedField: updatedAt(m)})
		}
	}

	return out
}

// nullable sends the value "null" as JSON null.
func nullable(v any) any {
	if v == nullValue {
		return nil
	}

	return v
}
