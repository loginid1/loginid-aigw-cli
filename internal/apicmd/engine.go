package apicmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/client"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/prompt"
)

// Flag names every operation command shares. The generator refuses a spec whose
// body fields would collide with one of these.
const (
	flagAPIKey  = "api-key"
	flagData    = "data"
	flagOutput  = "output"
	flagConfirm = "confirm"

	// TEMP: see AddAll.
	envTempAPIBase = "AIGW_TEMP_API_BASE"
)

// AddAll registers every generated operation under root, building the noun
// commands the operations' paths name along the way. Where requests go is
// decided when one runs, so `--help` neither warns nor fails over it.
func AddAll(root *cobra.Command) {
	addAll(root, ResolveTarget)
}

func addAll(root *cobra.Command, target func() (Target, error)) {
	for i := range Operations {
		op := &Operations[i]
		parent := ensureGroups(root, op.Command[:len(op.Command)-1])
		parent.AddCommand(newOperationCommand(op, target))
	}
}

// Target is where API requests go.
type Target struct {
	BaseURL string
	// Temporary is true when AIGW_TEMP_API_BASE chose BaseURL. The key then
	// comes only from --api-key or AIGW_TEMP_API_KEY: never the saved login
	// and never AIGW_API_KEY, so a variable set by a cloned repo's .mcp.json
	// or .envrc cannot collect a key it does not already know.
	Temporary bool
}

// ResolveTarget returns APIBase, the spec's server. It is not a flag:
// a key sent to a URL the user typed would leak.
//
// TEMP: envTempAPIBase overrides it, for testing against local and staging
// servers until there is a proper answer for those. It is left out of all help
// text on purpose; it must be https, or http on localhost or a loopback IP;
// and every run that uses it says so on stderr. Remove it once that answer
// exists.
func ResolveTarget() (Target, error) {
	override := os.Getenv(envTempAPIBase)
	if override == "" {
		return Target{BaseURL: APIBase}, nil
	}

	u, err := url.Parse(override)
	// No query or fragment: the API path is appended to the base as a string,
	// and would land inside them, so every call would hit the same URL.
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(override, "#") {
		return Target{}, fmt.Errorf("%s=%q is not a usable base URL", envTempAPIBase, override)
	}

	if u.Scheme != "https" && (u.Scheme != "http" || !isLoopback(u.Hostname())) {
		return Target{}, fmt.Errorf("%s must be https, or http on localhost; got %q", envTempAPIBase, override)
	}

	return Target{BaseURL: override, Temporary: true}, nil
}

// isLoopback accepts "localhost" and loopback IPs. Not "*.localhost": Go
// resolves those through DNS, which a hostile network answers. For plain http
// the client also refuses to dial anything that does not resolve to loopback.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

// Warn says on w that requests go to a temporary base URL. Each run resolves
// the target once, so this prints once.
func (t Target) Warn(w io.Writer) {
	if t.Temporary {
		fmt.Fprintf(w, "warning: %s is set; API requests go to %s with the key from --%s or %s\n",
			envTempAPIBase, t.BaseURL, flagAPIKey, client.EnvTempAPIKey)
	}
}

// ensureGroups finds or creates the chain of noun commands for path. The top
// noun carries --api-key, so `aigw version` does not show it.
func ensureGroups(root *cobra.Command, path []string) *cobra.Command {
	cur := root
	for depth, name := range path {
		next := findChild(cur, name)
		if next == nil {
			next = &cobra.Command{
				Use:   name,
				Short: "Manage " + strings.ReplaceAll(name, "-", " "),
				Args:  cobra.ArbitraryArgs,
				RunE:  groupRunE,
			}
			if depth == 0 {
				pf := next.PersistentFlags()
				pf.String(flagAPIKey, "", "API key to authenticate with (env "+client.EnvAPIKey+")")
			}

			cur.AddCommand(next)
		}

		cur = next
	}

	return cur
}

// groupRunE runs for a noun such as `aigw projects`. With no arguments it
// shows the noun's help. With one, the word was not a subcommand: that is an
// error, so a typo in a script fails instead of printing help and exiting 0.
func groupRunE(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}

	// Cobra applies its default distance only on the root's own lookup.
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}

	msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
	if suggestions := cmd.SuggestionsFor(args[0]); len(suggestions) > 0 {
		msg += "; did you mean " + strings.Join(suggestions, " or ") + "?"
	}

	return errors.New(msg)
}

func findChild(cmd *cobra.Command, name string) *cobra.Command {
	for _, c := range cmd.Commands() {
		if c.Name() == name {
			return c
		}
	}

	return nil
}

func newOperationCommand(op *Operation, target func() (Target, error)) *cobra.Command {
	var use strings.Builder

	use.WriteString(op.Command[len(op.Command)-1])

	for _, a := range op.Args {
		use.WriteString(" <" + kebab(a.Name) + ">")
	}

	long := op.Long
	if op.ReplacesNested() {
		long += "\n\n" + NestedReplaceNote
	}

	if op.Scope != "" {
		long += "\n\nRequires scope: " + op.Scope
	}

	cmd := &cobra.Command{
		Use:     use.String(),
		Short:   op.Short,
		Long:    long,
		Example: requiredExample(op),
		Args:    cobra.ExactArgs(len(op.Args)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, op, target, args)
		},
	}

	fs := cmd.Flags()

	for _, p := range op.Params {
		desc := p.Desc
		if len(p.Enum) > 0 {
			desc += " (one of: " + enumList(p.Enum) + ")"
		}

		if p.Required {
			desc += " (required)"
		}

		desc = strings.TrimSpace(desc)

		switch p.Type {
		case TypeBoolean:
			fs.Bool(p.Flag, false, desc)
		case TypeInteger:
			fs.Int64(p.Flag, 0, desc)
		case TypeNumber:
			fs.Float64(p.Flag, 0, desc)
		default:
			if strings.HasPrefix(p.Type, ListPrefix) {
				fs.StringArray(p.Flag, nil, desc+" (repeatable)")
			} else {
				fs.String(p.Flag, "", desc)
			}
		}

		// Only path and query params are enforced here. Required body fields
		// are left to the server, which can also accept them through -d.
		if p.Required && p.In != InBody {
			_ = cmd.MarkFlagRequired(p.Flag)
		}
	}

	if op.HasBody {
		fs.StringArrayP(flagData, "d", nil, "Body field as key=value, nested with brackets, e.g. -d "+dataExample(op))
	}

	fs.StringP(flagOutput, "o", "json", "Output format (json)")

	if op.Method == http.MethodDelete {
		fs.Bool(flagConfirm, false, "Skip the confirmation prompt")
	}

	return cmd
}

func run(cmd *cobra.Command, op *Operation, resolveTarget func() (Target, error), args []string) error {
	if out, _ := cmd.Flags().GetString(flagOutput); out != "json" {
		return fmt.Errorf("unsupported output format %q: only json is supported", out)
	}

	target, err := resolveTarget()
	if err != nil {
		return err
	}

	target.Warn(cmd.ErrOrStderr())

	flagKey, _ := cmd.Flags().GetString(flagAPIKey)

	apiKey, err := client.ResolveAPIKey(flagKey, target.Temporary)
	if errors.Is(err, client.ErrNotLoggedIn) {
		return NotLoggedIn(target)
	}

	if err != nil {
		mapped := ExitErrorFor(err)

		var exitErr *ExitError
		if errors.As(mapped, &exitErr) {
			return mapped
		}

		// Anything else came from reading the saved key: a corrupt file, a
		// locked or denied keyring, a timeout. Exit 3 all the same, so a
		// script treating 3 as "re-authenticate" still does.
		return &ExitError{Code: ExitNotAuthenticated, Err: fmt.Errorf(
			"read the saved API key: %w; run `aigw login` again, or set %s", err, client.EnvAPIKey)}
	}

	req := &client.Request{
		Method:     op.Method,
		Path:       op.Path,
		PathParams: map[string]string{},
		Query:      url.Values{},
		Scope:      op.Scope,
	}

	for i, a := range op.Args {
		req.PathParams[a.Name] = args[i]
	}

	for _, p := range op.Params {
		switch p.In {
		case InPath:
			req.PathParams[p.Name], _ = cmd.Flags().GetString(p.Flag)
		case InQuery:
			if cmd.Flags().Changed(p.Flag) {
				req.Query.Set(p.Name, cmd.Flags().Lookup(p.Flag).Value.String())
			}
		}
	}

	if op.HasBody {
		req.Body, err = buildBody(cmd, op)
		if err != nil {
			return err
		}
	}

	c := client.New(target.BaseURL, apiKey.Value)

	if op.Method == http.MethodDelete {
		err = confirmDelete(cmd, op, args, c, req)
		if err != nil {
			return err
		}
	}

	body, err := c.Do(cmd.Context(), req)
	if err != nil {
		return ExitErrorFor(err)
	}

	warnOneTimeSecrets(cmd.ErrOrStderr(), body)

	// A delete answers 204 with no body. Say what went, so a script or an
	// agent has something to read and a person sees it worked.
	if op.Method == http.MethodDelete && len(bytes.TrimSpace(body)) == 0 {
		body, err = doneBody(op, req.PathParams)
		if err != nil {
			return err
		}
	}

	return printJSON(cmd.OutOrStdout(), body)
}

// oneTimeSecrets are the response fields the API returns once and never
// again: the spec marks clientSecret "Shown once" on ProjectProxyClientSecret
// and "Returned once" on ResourceCreateResult.
var oneTimeSecrets = []string{"clientSecret"}

// warnOneTimeSecrets tells the user, on stderr, to store any one-time secret
// in body. stdout stays pure JSON for whoever reads it.
func warnOneTimeSecrets(w io.Writer, body []byte) {
	var top map[string]any
	if json.Unmarshal(body, &top) != nil {
		return
	}

	for _, field := range oneTimeSecrets {
		if v, _ := top[field].(string); v != "" {
			fmt.Fprintf(w, "warning: %s is shown only this once and cannot be read back; store it now\n", field)
		}
	}
}

// doneKeys maps the verb of each DELETE command to the key its result
// reports, so `agent-policy clear` says "cleared", not "deleted".
//
//nolint:goconst // the verbs as the overlay spells them, which operations_gen.go repeats
var doneKeys = map[string]string{
	"delete": "deleted",
	"remove": "removed",
	"revoke": "revoked",
	"clear":  "cleared",
}

// DoneKey is the key op's empty-body result reports: its verb in the past
// tense, or "ok" for a verb doneKeys does not know.
func DoneKey(op *Operation) string {
	if key, ok := doneKeys[op.Command[len(op.Command)-1]]; ok {
		return key
	}

	return "ok"
}

// doneBody is {"<DoneKey>": true} plus the path parameters that named what
// went, e.g. {"deleted": true, "projectId": "…"}.
func doneBody(op *Operation, pathParams map[string]string) ([]byte, error) {
	out := map[string]any{DoneKey(op): true}
	for k, v := range pathParams {
		out[k] = v
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("format result: %w", err)
	}

	return body, nil
}

// requiredExample shows the command with every unconditionally required
// input, as the Stripe CLI's "# required fields" line does. Fields that are
// required only inside an optional object are not listed; the server reports
// those.
func requiredExample(op *Operation) string {
	if len(op.Args) == 0 && len(op.RequiredLists) == 0 && !slices.ContainsFunc(op.Params, func(p Param) bool { return p.Required }) {
		return ""
	}

	var b strings.Builder

	b.WriteString("  # required fields\n  aigw ")
	b.WriteString(strings.Join(op.Command, " "))

	for _, a := range op.Args {
		b.WriteString(" <" + kebab(a.Name) + ">")
	}

	for _, p := range op.Params {
		if !p.Required {
			continue
		}

		switch {
		case p.Type == TypeBoolean:
			b.WriteString(" --" + p.Flag)
		case len(p.Enum) > 0:
			b.WriteString(" --" + p.Flag + " " + p.Enum[0])
		default:
			// "--exchange.client-id <client-id>", "--project-id <project-id>"
			b.WriteString(" --" + p.Flag + " <" + p.Flag[strings.LastIndex(p.Flag, ".")+1:] + ">")
		}
	}

	for _, list := range op.RequiredLists {
		if field := firstField(op, list+"[]."); field != "" {
			b.WriteString(` -d "` + bracketKey(field) + "=" + exampleValue(op, field) + `"`)
		}
	}

	return b.String()
}

// dataExample is a -d example built from one of op's own fields: a field in
// a list of objects if there is one, since only -d can set those.
func dataExample(op *Operation) string {
	field := firstField(op, "")
	if listField := firstField(op, "[]."); listField != "" {
		field = listField
	}

	if field == "" {
		return `"key=value"`
	}

	return `"` + bracketKey(field) + "=" + exampleValue(op, field) + `"`
}

// exampleValue is a placeholder named after the field, or, for a field with a
// fixed set of values, the first real one, so the example works as written.
func exampleValue(op *Operation, field string) string {
	for _, v := range op.FieldEnums[field] {
		if v != "" {
			return v
		}
	}

	return "<" + field[strings.LastIndex(field, ".")+1:] + ">"
}

// firstField is the alphabetically first Fields path containing sub, so help
// text is the same on every run.
func firstField(op *Operation, sub string) string {
	var paths []string

	for path := range op.Fields {
		if strings.Contains(path, sub) && !strings.HasSuffix(path, "[]") {
			paths = append(paths, path)
		}
	}

	slices.Sort(paths)

	if len(paths) == 0 {
		return ""
	}

	return paths[0]
}

// bracketKey writes a Fields path the way -d takes it: "scopes[].value"
// becomes "scopes[0][value]".
func bracketKey(path string) string {
	path = strings.ReplaceAll(path, "[]", "[0]")

	parts := strings.Split(path, ".")
	for i := 1; i < len(parts); i++ {
		parts[i] = "[" + parts[i] + "]"
	}

	return strings.Join(parts, "")
}

// buildBody assembles the request body from body flags first, then -d values,
// so a -d entry can override or extend what a flag set.
func buildBody(cmd *cobra.Command, op *Operation) (map[string]any, error) {
	var root any = map[string]any{}

	var err error

	// The body fields set by flags, by API name, so -d cannot quietly
	// overwrite one.
	byFlag := map[string]string{}

	for _, p := range op.Params {
		if p.In != InBody || !cmd.Flags().Changed(p.Flag) {
			continue
		}

		byFlag[p.Name] = p.Flag

		var v any

		switch {
		case p.Type == TypeBoolean:
			v, _ = cmd.Flags().GetBool(p.Flag)
		case p.Type == TypeInteger:
			v, _ = cmd.Flags().GetInt64(p.Flag)
		case p.Type == TypeNumber:
			v, _ = cmd.Flags().GetFloat64(p.Flag)
		case strings.HasPrefix(p.Type, ListPrefix):
			raw, _ := cmd.Flags().GetStringArray(p.Flag)

			items := make([]any, 0, len(raw))
			for _, r := range raw {
				if len(p.Enum) > 0 && !slices.Contains(p.Enum, r) {
					return nil, fmt.Errorf("--%s: %q is not one of: %s", p.Flag, r, enumList(p.Enum))
				}

				item, coerceErr := coerce(strings.TrimPrefix(p.Type, ListPrefix), r)
				if coerceErr != nil {
					return nil, fmt.Errorf("--%s: %w", p.Flag, coerceErr)
				}

				items = append(items, item)
			}

			v = items
		default:
			s, _ := cmd.Flags().GetString(p.Flag)
			if len(p.Enum) > 0 && !slices.Contains(p.Enum, s) {
				return nil, fmt.Errorf("--%s: %q is not one of: %s", p.Flag, s, enumList(p.Enum))
			}

			v = s
		}

		root, err = insert(root, strings.Split(p.Name, "."), v)
		if err != nil {
			return nil, fmt.Errorf("--%s: %w", p.Flag, err)
		}
	}

	data, _ := cmd.Flags().GetStringArray(flagData)
	for _, d := range data {
		if flag := overlappingFlag(d, byFlag); flag != "" {
			return nil, fmt.Errorf("-d %q and --%s set the same field: use one of them", d, flag)
		}

		root, err = applyData(root, op, d)
		if err != nil {
			return nil, fmt.Errorf("-d %q: %w", d, err)
		}
	}

	return root.(map[string]any), nil
}

func confirmDelete(cmd *cobra.Command, op *Operation, args []string, c *client.Client, req *client.Request) error {
	if ok, _ := cmd.Flags().GetBool(flagConfirm); ok {
		return nil
	}

	target := strings.Join(op.Command, " ")
	if len(args) > 0 {
		target += " " + strings.Join(args, " ")
	}

	in, ok := cmd.InOrStdin().(*os.File)
	if !ok || !prompt.IsTerminal(in) {
		return fmt.Errorf("refusing to run %q without --%s when input is not a terminal%s", target, flagConfirm, prompt.NotTerminalHint())
	}

	fmt.Fprint(cmd.ErrOrStderr(), DeletePrompt(cmd.Context(), op, target, c, req))

	line, _ := bufio.NewReader(in).ReadString('\n')
	if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
		return errors.New("aborted")
	}

	return nil
}

// DeletePrompt is the question confirmDelete asks: what the command deletes,
// by name when the API can say, and what goes with it, in the spec's words.
func DeletePrompt(ctx context.Context, op *Operation, target string, c *client.Client, req *client.Request) string {
	var b strings.Builder

	b.WriteString("This runs " + strconv.Quote(target))

	if name := lookupName(ctx, op, c, req); name != "" {
		noun := strings.TrimSuffix(op.Command[len(op.Command)-2], "s")
		fmt.Fprintf(&b, ", deleting %s %q", noun, name)
	}

	b.WriteString(".\n")

	if op.Long != "" {
		b.WriteString(op.Long + "\n")
	}

	b.WriteString("This cannot be undone. Continue? [y/N] ")

	return b.String()
}

// lookupName fetches what a delete is about to remove, through the GET on the
// same path, and returns its name. Any failure returns "": the prompt is still
// right without a name, and the delete reports its own errors.
func lookupName(ctx context.Context, del *Operation, c *client.Client, req *client.Request) string {
	i := slices.IndexFunc(Operations, func(op Operation) bool {
		return op.Method == http.MethodGet && op.Path == del.Path
	})
	if i < 0 {
		return ""
	}

	body, err := c.Do(ctx, &client.Request{
		Method:     http.MethodGet,
		Path:       Operations[i].Path,
		PathParams: req.PathParams,
		Scope:      Operations[i].Scope,
	})
	if err != nil {
		return ""
	}

	var got struct {
		Name string `json:"name"`
	}

	if json.Unmarshal(body, &got) != nil {
		return ""
	}

	return got.Name
}

// printJSON writes an indented copy of body. A body that is empty, as on a 204,
// prints nothing.
func printJSON(w io.Writer, body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}

	var buf bytes.Buffer

	// The client only returns JSON (see client.ErrNotAPI), so this cannot
	// fail on a server's say-so.
	err := json.Indent(&buf, body, "", "  ")
	if err != nil {
		return fmt.Errorf("format response: %w", err)
	}

	// One newline at the end. The API's own trailing newline would otherwise
	// leave a blank line after the closing brace.
	out := append(bytes.TrimRight(buf.Bytes(), "\r\n"), '\n')
	_, err = w.Write(out)

	return err
}

func kebab(s string) string {
	var b strings.Builder

	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('-')
			}

			r += 'a' - 'A'
		}

		b.WriteRune(r)
	}

	return b.String()
}

// enumList renders allowed values for help and errors. An empty value is
// shown as "", not as nothing between two commas.
func enumList(values []string) string {
	shown := make([]string, len(values))

	for i, v := range values {
		if v == "" {
			v = `""`
		}

		shown[i] = v
	}

	return strings.Join(shown, ", ")
}
