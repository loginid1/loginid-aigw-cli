//go:build gen_commands

// Command gen writes internal/apicmd/operations_gen.go from the devcon OpenAPI
// spec and api/devcon-overlay.yaml.
//
// It is kept out of the normal build by its build tag, as the Stripe CLI keeps
// its generator: `make generate` runs it, and the output is checked in, so
// building aigw never needs the spec or the network. The same inputs always
// give the same bytes; everything is sorted before it is written.
package main

import (
	"bytes"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"log"
	"os"
	"slices"
	"strings"
	"text/template"
	"unicode"

	"go.yaml.in/yaml/v3"
)

//go:embed operations_gen.go.tpl
var tplText string

// Flags every operation command defines itself; see internal/apicmd/engine.go.
var reserved = []string{"api-key", "data", "output", "confirm", "secret-file", "help"}

// secretFields are the response fields the API returns once and never again.
// The spec marks clientSecret "Shown once" on ProjectProxyClientSecret and
// "Returned once" on ResourceCreateResult. An operation whose success response
// has one at its top level gets --secret-file and the stderr warning.
var secretFields = []string{"clientSecret"}

var methods = []string{"get", "post", "put", "patch", "delete"}

// maxNesting bounds how deep body schemas may nest.
const maxNesting = 10

func main() {
	specPath := flag.String("spec", "", "path to the OpenAPI 3 spec (yaml)")
	overlayPath := flag.String("overlay", "api/devcon-overlay.yaml", "path to the overlay")
	ref := flag.String("ref", "", "spec source, recorded in the output header")
	out := flag.String("out", "internal/apicmd/operations_gen.go", "output file")

	flag.Parse()

	if *specPath == "" || *ref == "" {
		log.Fatal("gen: -spec and -ref are required")
	}

	src, err := generate(*specPath, *overlayPath, *ref)
	if err != nil {
		log.Fatalf("gen: %v", err)
	}

	err = os.WriteFile(*out, src, 0o600)
	if err != nil {
		log.Fatalf("gen: %v", err)
	}
}

// ---- input shapes: only the parts of OpenAPI 3 this generator reads ----

type spec struct {
	Servers    []struct{ URL string } `yaml:"servers"`
	Paths      map[string]map[string]yaml.Node
	Components struct {
		Schemas map[string]*schema
	}
}

type operation struct {
	OperationID   string       `yaml:"operationId"`
	Summary       string       `yaml:"summary"`
	Description   string       `yaml:"description"`
	RequiredScope string       `yaml:"x-required-scope"`
	Parameters    []parameter  `yaml:"parameters"`
	RequestBody   *requestBody `yaml:"requestBody"`
	Responses     map[string]struct {
		Content map[string]struct{ Schema *schema }
	} `yaml:"responses"`
}

type parameter struct {
	Name        string
	In          string
	Description string
	Required    bool
	Schema      *schema
}

type requestBody struct {
	Content map[string]struct{ Schema *schema }
}

type schema struct {
	Ref         string `yaml:"$ref"`
	Type        string
	Description string
	Enum        []string
	Required    []string
	Properties  map[string]*schema
	Items       *schema
}

type overlay struct {
	Operations map[string]struct {
		Command string
	}
}

// ---- output shapes: mirror apicmd.Operation and apicmd.Param ----

type outOp struct {
	ID, Short, Long, Method, Path, Scope string
	Command                              []string
	Args, Params                         []outParam
	Fields                               []field
	RequiredLists                        []string
	SecretFields                         []string
	HasBody                              bool
}

type outParam struct {
	Name, Flag, In, Type, Desc string
	Required                   bool
	Enum                       []string
}

// HasFieldEnums reports whether any -d field has a fixed set of values.
func (o *outOp) HasFieldEnums() bool {
	for _, f := range o.Fields {
		if len(f.Enum) > 0 {
			return true
		}
	}

	return false
}

type field struct {
	Path, Type string
	Enum       []string
}

func generate(specPath, overlayPath, ref string) ([]byte, error) {
	var (
		s  spec
		ov overlay
	)

	err := errors.Join(readYAML(specPath, &s), readYAML(overlayPath, &ov))
	if err != nil {
		return nil, err
	}

	if len(s.Servers) == 0 {
		return nil, errors.New("spec has no servers entry")
	}

	var (
		ops  []outOp
		errs []error
		seen = map[string]bool{}
		cmds = map[string]string{}
	)

	for _, path := range sortedKeys(s.Paths) {
		for _, method := range methods {
			node, ok := s.Paths[path][method]
			if !ok {
				continue
			}

			var o operation

			decodeErr := node.Decode(&o)
			if decodeErr != nil {
				return nil, fmt.Errorf("%s %s: %w", method, path, decodeErr)
			}

			entry, ok := ov.Operations[o.OperationID]
			if !ok {
				errs = append(errs, fmt.Errorf("operation %q (%s %s) has no overlay entry", o.OperationID, strings.ToUpper(method), path))

				continue
			}

			seen[o.OperationID] = true

			if prev, dup := cmds[entry.Command]; dup {
				errs = append(errs, fmt.Errorf("command %q is used by both %q and %q", entry.Command, prev, o.OperationID))
			}

			cmds[entry.Command] = o.OperationID

			op, opErr := buildOp(&s, path, method, &o, entry.Command)
			if opErr != nil {
				errs = append(errs, fmt.Errorf("%s: %w", o.OperationID, opErr))

				continue
			}

			ops = append(ops, op)
		}
	}

	for _, id := range sortedKeys(ov.Operations) {
		if !seen[id] {
			errs = append(errs, fmt.Errorf("overlay entry %q matches no operation in the spec", id))
		}
	}

	err = errors.Join(errs...)
	if err != nil {
		return nil, err
	}

	slices.SortFunc(ops, func(a, b outOp) int {
		return strings.Compare(strings.Join(a.Command, " "), strings.Join(b.Command, " "))
	})

	tpl := template.Must(template.New("gen").Funcs(template.FuncMap{"q": quote, "qs": quoteList}).Parse(tplText))

	var buf bytes.Buffer

	err1 := tpl.Execute(&buf, map[string]any{"Ref": ref, "Base": s.Servers[0].URL, "Ops": ops})
	if err1 != nil {
		return nil, err1
	}

	src, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format output: %w\n%s", err, buf.Bytes())
	}

	return src, nil
}

func buildOp(s *spec, path, method string, o *operation, command string) (outOp, error) {
	if o.RequiredScope == "" {
		return outOp{}, errors.New("no x-required-scope")
	}

	short, long := describe(o)
	op := outOp{
		ID:      o.OperationID,
		Command: strings.Fields(command),
		Short:   short,
		Long:    long,
		Method:  strings.ToUpper(method),
		Path:    path,
		Scope:   o.RequiredScope,
	}

	if len(op.Command) < 2 {
		return outOp{}, fmt.Errorf("command %q needs a noun and a verb", command)
	}

	flags := map[string]bool{}
	for _, r := range reserved {
		flags[r] = true
	}

	addFlag := func(p outParam) error {
		if flags[p.Flag] {
			return fmt.Errorf("flag --%s is used twice or collides with a shared flag", p.Flag)
		}

		flags[p.Flag] = true
		op.Params = append(op.Params, p)

		return nil
	}

	for _, p := range o.Parameters {
		switch p.In {
		case inPath:
			// The id a path ends with is the thing the command acts on, so it
			// is positional: `aigw resources get <resource-id>`. Parent ids
			// before it are flags: `--project-id`.
			if strings.HasSuffix(path, "/{"+p.Name+"}") {
				op.Args = append(op.Args, outParam{Name: p.Name, In: inPath, Type: tString, Required: true, Desc: p.Description})

				continue
			}

			err2 := addFlag(outParam{Name: p.Name, Flag: kebab(p.Name), In: inPath, Type: tString, Required: true, Desc: p.Description})
			if err2 != nil {
				return outOp{}, err2
			}
		case inQuery:
			typ, err := scalarType(s, p.Schema)
			if err != nil {
				return outOp{}, fmt.Errorf("query param %s: %w", p.Name, err)
			}

			err3 := addFlag(outParam{Name: p.Name, Flag: kebab(p.Name), In: inQuery, Type: typ, Required: p.Required, Desc: p.Description})
			if err3 != nil {
				return outOp{}, err3
			}
		case "header":
			// The only header is Authorization, which the engine sets.
		default:
			return outOp{}, fmt.Errorf("parameter %s: location %q not supported", p.Name, p.In)
		}
	}

	if o.RequestBody != nil {
		media, ok := o.RequestBody.Content["application/json"]
		if !ok {
			return outOp{}, errors.New("request body is not application/json")
		}

		op.HasBody = true

		body, err := resolve(s, media.Schema)
		if err != nil {
			return outOp{}, err
		}

		var fields []field

		err4 := walkBody(s, body, "", "", body.Required, &fields, addFlag)
		if err4 != nil {
			return outOp{}, err4
		}

		slices.SortFunc(fields, func(a, b field) int { return strings.Compare(a.Path, b.Path) })
		op.Fields = fields

		// Required top-level lists of objects have no flag, so help must
		// point at -d for them.
		for _, name := range body.Required {
			prop, err := resolve(s, body.Properties[name])
			if err != nil {
				return outOp{}, err
			}

			if prop.Type != tArray {
				continue
			}

			item, err := resolve(s, prop.Items)
			if err != nil {
				return outOp{}, err
			}

			if item.Type == tObject {
				op.RequiredLists = append(op.RequiredLists, name)
			}
		}

		slices.Sort(op.RequiredLists)
	}

	secrets, err := responseSecrets(s, o)
	if err != nil {
		return outOp{}, err
	}

	op.SecretFields = secrets

	return op, nil
}

// responseSecrets returns the secretFields at the top level of o's 2xx JSON
// responses, sorted.
func responseSecrets(s *spec, o *operation) ([]string, error) {
	var found []string

	for code, resp := range o.Responses {
		if !strings.HasPrefix(code, "2") {
			continue
		}

		media, ok := resp.Content["application/json"]
		if !ok || media.Schema == nil {
			continue
		}

		body, err := resolve(s, media.Schema)
		if err != nil {
			return nil, fmt.Errorf("response %s: %w", code, err)
		}

		for _, name := range secretFields {
			if body.Properties[name] != nil && !slices.Contains(found, name) {
				found = append(found, name)
			}
		}
	}

	slices.Sort(found)

	return found, nil
}

// walkBody flattens an object schema. Scalars and scalar lists become flags,
// named by their dotted path. Every settable path, including the ones inside
// lists of objects that no flag can express, is recorded in fields for -d.
func walkBody(s *spec, obj *schema, apiPrefix, flagPrefix string, required []string, fields *[]field, addFlag func(outParam) error) error {
	// resolve only bounds $ref-to-$ref chains; a schema whose property points
	// back at itself would otherwise recurse for ever.
	if strings.Count(apiPrefix, ".") > maxNesting {
		return fmt.Errorf("%s: nested more than %d levels; is the schema recursive?", apiPrefix, maxNesting)
	}

	for _, name := range sortedKeys(obj.Properties) {
		prop, err := resolve(s, obj.Properties[name])
		if err != nil {
			return fmt.Errorf("%s%s: %w", apiPrefix, name, err)
		}

		apiName, flagName := apiPrefix+name, flagPrefix+kebab(name)
		desc := firstNonEmpty(obj.Properties[name].Description, prop.Description)

		switch prop.Type {
		case tObject:
			// As in the Stripe CLI, a nested field counts as required only
			// when every object above it is required too. "Required when the
			// parent is sent" is the server's to check.
			var nested []string
			if slices.Contains(required, name) {
				nested = prop.Required
			}

			err5 := walkBody(s, prop, apiName+".", flagName+".", nested, fields, addFlag)
			if err5 != nil {
				return err5
			}
		case tArray:
			item, err := resolve(s, prop.Items)
			if err != nil {
				return fmt.Errorf("%s: %w", apiName, err)
			}

			if item.Type == tObject {
				// Only -d can set a list of objects.
				err6 := walkFields(s, item, apiName+"[]", fields)
				if err6 != nil {
					return err6
				}

				continue
			}

			*fields = append(*fields, field{apiName + "[]", item.Type, item.Enum})

			err7 := addFlag(outParam{Name: apiName, Flag: flagName, In: "body", Type: "[]" + item.Type, Required: slices.Contains(required, name), Enum: item.Enum, Desc: desc})
			if err7 != nil {
				return err7
			}
		default:
			*fields = append(*fields, field{apiName, prop.Type, prop.Enum})

			err8 := addFlag(outParam{Name: apiName, Flag: flagName, In: "body", Type: prop.Type, Required: slices.Contains(required, name), Enum: prop.Enum, Desc: desc})
			if err8 != nil {
				return err8
			}
		}
	}

	return nil
}

// walkFields records the -d paths inside a list element, without flags.
func walkFields(s *spec, obj *schema, prefix string, fields *[]field) error {
	if strings.Count(prefix, ".")+strings.Count(prefix, "[]") > maxNesting {
		return fmt.Errorf("%s: nested more than %d levels; is the schema recursive?", prefix, maxNesting)
	}

	for _, name := range sortedKeys(obj.Properties) {
		prop, err := resolve(s, obj.Properties[name])
		if err != nil {
			return err
		}

		path := prefix + "." + name

		switch prop.Type {
		case tObject:
			err9 := walkFields(s, prop, path, fields)
			if err9 != nil {
				return err9
			}
		case tArray:
			item, err := resolve(s, prop.Items)
			if err != nil {
				return err
			}

			if item.Type == tObject {
				err10 := walkFields(s, item, path+"[]", fields)
				if err10 != nil {
					return err10
				}

				continue
			}

			*fields = append(*fields, field{path + "[]", item.Type, item.Enum})
		default:
			*fields = append(*fields, field{path, prop.Type, prop.Enum})
		}
	}

	return nil
}

// resolve follows a local $ref. Only "#/components/schemas/<name>" refs occur
// in the specs Goa writes.
func resolve(s *spec, sc *schema) (*schema, error) {
	for depth := 0; sc != nil && sc.Ref != ""; depth++ {
		if depth > 10 {
			return nil, fmt.Errorf("$ref chain too deep at %s", sc.Ref)
		}

		name, ok := strings.CutPrefix(sc.Ref, "#/components/schemas/")
		if !ok {
			return nil, fmt.Errorf("unsupported $ref %q", sc.Ref)
		}

		next, ok := s.Components.Schemas[name]
		if !ok {
			return nil, fmt.Errorf("unknown $ref %q", sc.Ref)
		}

		sc = next
	}

	if sc == nil {
		return nil, errors.New("missing schema")
	}

	switch sc.Type {
	case tObject, tArray, tString, "integer", "number", "boolean":
		return sc, nil
	default:
		return nil, fmt.Errorf("schema type %q not supported", sc.Type)
	}
}

func scalarType(s *spec, sc *schema) (string, error) {
	r, err := resolve(s, sc)
	if err != nil {
		return "", err
	}

	if r.Type == tObject || r.Type == tArray {
		return "", fmt.Errorf("type %s not supported here", r.Type)
	}

	return r.Type, nil
}

// describe picks the help text. Goa fills summary with "<method> <service>",
// which says nothing, so the first sentence of the description is preferred.
func describe(o *operation) (string, string) { //nolint:gocritic // short, then long; named results are banned by nonamedreturns
	long := strings.TrimSpace(o.Description)
	short := long

	if i := strings.Index(short, ". "); i >= 0 {
		short = short[:i]
	}

	short = strings.TrimSuffix(short, ".")
	if short == "" {
		short = o.Summary
	}

	// One line in a command table. The full text stays in Long.
	const maxShort = 80
	if r := []rune(short); len(r) > maxShort {
		cut := string(r[:maxShort-1])
		if i := strings.LastIndex(cut, " "); i > maxShort/2 {
			cut = cut[:i]
		}

		short = strings.TrimRight(cut, ",;:") + "…"
	}

	return short, long
}

func readYAML(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	err11 := yaml.Unmarshal(raw, v)
	if err11 != nil {
		return fmt.Errorf("%s: %w", path, err11)
	}

	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}

func kebab(s string) string {
	var b strings.Builder

	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('-')
			}

			r = unicode.ToLower(r)
		}

		b.WriteRune(r)
	}

	return b.String()
}

func quote(s string) string { return fmt.Sprintf("%q", s) }

func quoteList(ss []string) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = quote(s)
	}

	return "[]string{" + strings.Join(parts, ", ") + "}"
}
