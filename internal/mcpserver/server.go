// Package mcpserver exposes the generated API operations as MCP tools.
//
// Each operation becomes one tool, with the input schema the generator copied
// from the spec, and each call goes through internal/client: the same request
// path, auth and error handling as the CLI commands. Only the input (JSON
// arguments instead of flags) and the output (a tool result instead of
// printed JSON) differ.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/apicmd"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/client"
)

// Options configures the server.
type Options struct {
	// BaseURL is the API every tool calls.
	BaseURL string
	// APIKey, when set, is used instead of resolving one per call. Tests set
	// it; `aigw mcp` leaves it empty so a later `aigw login` takes effect
	// without a restart.
	APIKey string
	// TemporaryBase is set when BaseURL is a temporary override (see
	// apicmd.Target). The key then comes only from AIGW_TEMP_API_KEY.
	TemporaryBase bool
	// ReadOnly registers only the tools that make GET requests.
	ReadOnly bool
	// ExposeSecrets registers the tools whose response carries a one-time
	// secret. Off by default: the secret would enter the model's context,
	// and the API never shows it again, so hiding it after the fact would
	// lose it for good.
	ExposeSecrets bool
	// Version is reported to the client.
	Version string
}

// New builds a server with one tool for each operation opts allows.
func New(ops []apicmd.Operation, opts Options) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "aigw", Version: opts.Version}, nil)

	for i := range ops {
		op := &ops[i]

		if opts.ReadOnly && op.Method != http.MethodGet {
			continue
		}

		if op.ReturnsSecret && !opts.ExposeSecrets {
			continue
		}

		srv.AddTool(tool(op), handler(op, mustResolve(op), opts))
	}

	return srv
}

// ToolName is the MCP name of op: its command path joined by underscores,
// e.g. "resources_signing_keys_create".
func ToolName(op *apicmd.Operation) string {
	return strings.ReplaceAll(strings.Join(op.Command, "_"), "-", "_")
}

func tool(op *apicmd.Operation) *mcp.Tool {
	desc := op.Long
	if desc == "" {
		desc = op.Short
	}

	if op.ReplacesNested() {
		desc += "\n\n" + apicmd.NestedReplaceNote
	}

	desc += "\n\nRequires API key scope: " + op.Scope + "."

	notDestructive, closedWorld := false, false
	annotations := &mcp.ToolAnnotations{
		Title:         op.Short,
		ReadOnlyHint:  op.Method == http.MethodGet,
		OpenWorldHint: &closedWorld,
		// PUT replaces a resource and DELETE removes one; both can undo
		// earlier work. POST adds, and PATCH changes only what it names.
		IdempotentHint: op.Method == http.MethodGet || op.Method == http.MethodPut || op.Method == http.MethodDelete,
	}

	// A PATCH that replaces nested objects whole can wipe fields, so it keeps
	// the default destructive hint, like PUT and DELETE.
	if op.Method == http.MethodPost || (op.Method == http.MethodPatch && !op.ReplacesNested()) {
		annotations.DestructiveHint = &notDestructive
	}

	return &mcp.Tool{
		Name:        ToolName(op),
		Description: desc,
		InputSchema: json.RawMessage(op.InputSchema),
		Annotations: annotations,
	}
}

// mustResolve compiles op's input schema for validation. The schema is
// generated, so one that does not compile is a generator bug; a test resolves
// every operation's schema, so this cannot fail at run time unnoticed.
func mustResolve(op *apicmd.Operation) *jsonschema.Resolved {
	var schema jsonschema.Schema

	err := json.Unmarshal([]byte(op.InputSchema), &schema)
	if err == nil {
		var resolved *jsonschema.Resolved

		resolved, err = schema.Resolve(nil)
		if err == nil {
			return resolved
		}
	}

	panic(fmt.Sprintf("mcpserver: input schema of %s: %v", op.ID, err))
}

// validate checks the arguments against the tool's input schema. The SDK
// leaves this to low-level tools like these, and without it an unknown field
// (a typo such as "nme"), a bad enum value or a missing required field would
// be sent to the API and could even succeed.
func validate(schema *jsonschema.Resolved, raw json.RawMessage) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}

	var args any

	err := json.Unmarshal(raw, &args)
	if err != nil {
		return fmt.Errorf("arguments must be a JSON object: %w", err)
	}

	err = schema.Validate(args)
	if err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}

	return nil
}

func handler(op *apicmd.Operation, schema *jsonschema.Resolved, opts Options) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		err := validate(schema, req.Params.Arguments)
		if err != nil {
			return toolError(err.Error()), nil
		}

		apiReq, err := request(op, req.Params.Arguments)
		if err != nil {
			return toolError(err.Error()), nil
		}

		key := opts.APIKey
		if key == "" {
			key, err = client.ResolveAPIKey("", opts.TemporaryBase)
		}

		if errors.Is(err, client.ErrNotLoggedIn) {
			if opts.TemporaryBase {
				return toolError("No API key: set " + client.EnvTempAPIKey + " in this MCP server's environment. " +
					"While the base URL is overridden, " + client.EnvAPIKey + " and the saved login are not used."), nil
			}

			return toolError("aigw is not logged in. Run `aigw login` in a terminal, or set " +
				client.EnvAPIKey + " in this MCP server's environment."), nil
		}

		if err != nil {
			return toolError(err.Error()), nil
		}

		body, err := client.New(opts.BaseURL, key).Do(ctx, apiReq)

		// The client canceled the call (an MCP cancel notification): it expects
		// no result, so return the context's error rather than a tool result.
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, ctx.Err()
		}

		if err != nil {
			// A tool error, not a protocol error, so the model sees what went
			// wrong and can correct its arguments.
			return toolError(err.Error()), nil
		}

		text := string(body)
		if strings.TrimSpace(text) == "" {
			text = "Done. The API returned no content."
		}

		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
	}
}

// request splits the tool arguments into path parameters, query parameters
// and, for an operation with a body, the body: whatever is left.
func request(op *apicmd.Operation, raw json.RawMessage) (*client.Request, error) {
	args := map[string]any{}

	if len(raw) > 0 {
		err := json.Unmarshal(raw, &args)
		if err != nil {
			return nil, fmt.Errorf("arguments must be a JSON object: %w", err)
		}
	}

	req := &client.Request{
		Method:     op.Method,
		Path:       op.Path,
		PathParams: map[string]string{},
		Query:      url.Values{},
		Scope:      op.Scope,
	}

	pathParams := make([]string, 0, len(op.Args))
	for _, a := range op.Args {
		pathParams = append(pathParams, a.Name)
	}

	for _, p := range op.Params {
		switch p.In {
		case apicmd.InPath:
			pathParams = append(pathParams, p.Name)
		case apicmd.InQuery:
			if v, ok := args[p.Name]; ok {
				req.Query.Set(p.Name, fmt.Sprint(v))
				delete(args, p.Name)
			}
		}
	}

	for _, name := range pathParams {
		v, ok := args[name].(string)
		if !ok || v == "" {
			return nil, fmt.Errorf("missing required argument %q", name)
		}

		req.PathParams[name] = v
		delete(args, name)
	}

	if op.HasBody {
		req.Body = args
	} else if len(args) > 0 {
		names := make([]string, 0, len(args))
		for name := range args {
			names = append(names, name)
		}

		slices.Sort(names)

		return nil, fmt.Errorf("unknown arguments: %s", strings.Join(names, ", "))
	}

	return req, nil
}

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}
