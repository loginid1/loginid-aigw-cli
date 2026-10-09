// Package apicmd turns generated operation tables into cobra commands.
//
// The split follows the Stripe CLI: internal/gen writes operations_gen.go, which
// holds data only, and everything that acts on that data lives here, written by
// hand. A bug in flag handling or request building is fixed once, in this
// package, and never by regenerating.
package apicmd

import (
	"net/http"
	"strings"
)

// Operation is one API call, as the generator read it from the spec and the
// overlay.
type Operation struct {
	// ID is the spec's operationId, e.g. "resources#create".
	ID string
	// Command is the command path under the root, e.g. ["resources", "create"].
	Command []string
	Short   string
	Long    string
	Method  string
	// Path is the URL template, e.g. "/projects/{projectId}/resources".
	Path string
	// Scope is the x-required-scope value the API key must carry.
	Scope string
	// Args are path parameters taken positionally, in order.
	Args []Param
	// Params are path, query and flattened body parameters taken as flags.
	Params []Param
	// Fields maps every settable body path to its type, for -d. Array elements
	// are written "[]", e.g. "scopes[].value".
	Fields map[string]string
	// FieldEnums holds the allowed values of the Fields that have a fixed set,
	// so -d refuses what the matching flag would.
	FieldEnums map[string][]string
	// RequiredLists names required top-level lists of objects. They have no
	// flag, so help points at -d for them.
	RequiredLists []string
	// HasBody is true when the operation takes a JSON request body.
	HasBody bool
	// ReturnsSecret is true when a success response carries a one-time
	// secret, such as a new client secret the API never shows again.
	ReturnsSecret bool
	// InputSchema is the JSON Schema of the operation's MCP tool input: path
	// and query parameters next to the body's top-level fields.
	InputSchema string
}

// Param is one input an operation takes.
type Param struct {
	// Name is the API name. Body params are dotted, e.g. "exchange.audience".
	Name string
	// Flag is the CLI name, e.g. "exchange.actor-client-id". Empty for Args.
	Flag string
	// In is "path", "query" or "body".
	In string
	// Type is "string", "integer", "number", "boolean", or "[]" plus one of those.
	Type     string
	Required bool
	Enum     []string
	Desc     string
}

// Parameter locations.
const (
	InPath  = "path"
	InQuery = "query"
	InBody  = "body"
)

// Scalar types. A list type is ListPrefix plus one of these.
const (
	TypeString  = "string"
	TypeInteger = "integer"
	TypeNumber  = "number"
	TypeBoolean = "boolean"
	ListPrefix  = "[]"
)

// NestedReplaceNote warns that a PATCH replaces nested objects whole. The API
// merges top-level fields, but an object you send (exchange, options, ...)
// replaces the stored one, so its fields you leave out are reset, not kept.
const NestedReplaceNote = "Only the top-level fields you send change. An object you send " +
	"(for example exchange or options) replaces the stored one whole: its fields you " +
	"leave out are reset. Read the current value first and send the full object."

// ReplacesNested reports whether op is a PATCH whose body has nested objects,
// which the API replaces whole (see NestedReplaceNote).
func (op *Operation) ReplacesNested() bool {
	if op.Method != http.MethodPatch {
		return false
	}

	for path := range op.Fields {
		if strings.Contains(path, ".") {
			return true
		}
	}

	return false
}
