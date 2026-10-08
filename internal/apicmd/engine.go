package apicmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/client"
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
// commands the operations' paths name along the way.
func AddAll(root *cobra.Command) {
	addAll(root, BaseURL())
}

// BaseURL is where API requests go: DefaultAPIBase, the spec's server. It is
// not a flag: a key sent to a URL the user typed would leak.
//
// TEMP: envTempAPIBase overrides it, for testing against local and staging
// servers until there is a proper answer for those. It is left out of all help
// text on purpose, and every run that uses it says so on stderr. Remove it
// once that answer exists.
func BaseURL() string {
	override := os.Getenv(envTempAPIBase)
	if override == "" {
		return DefaultAPIBase
	}

	warnTempBase.Do(func() {
		fmt.Fprintf(os.Stderr, "warning: %s is set; API requests go to %s\n", envTempAPIBase, override)
	})

	return override
}

// warnTempBase prints the BaseURL warning once per run, however often it is asked.
var warnTempBase sync.Once

func addAll(root *cobra.Command, base string) {
	for i := range Operations {
		op := &Operations[i]
		parent := ensureGroups(root, op.Command[:len(op.Command)-1])
		parent.AddCommand(newOperationCommand(op, base))
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
				Args:  cobra.NoArgs,
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

func findChild(cmd *cobra.Command, name string) *cobra.Command {
	for _, c := range cmd.Commands() {
		if c.Name() == name {
			return c
		}
	}

	return nil
}

func newOperationCommand(op *Operation, base string) *cobra.Command {
	var use strings.Builder

	use.WriteString(op.Command[len(op.Command)-1])

	for _, a := range op.Args {
		use.WriteString(" <" + kebab(a.Name) + ">")
	}

	long := op.Long
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
			return run(cmd, op, base, args)
		},
	}

	fs := cmd.Flags()

	for _, p := range op.Params {
		desc := p.Desc
		if len(p.Enum) > 0 {
			desc += " (one of: " + strings.Join(p.Enum, ", ") + ")"
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
		fs.StringArrayP(flagData, "d", nil, `Body field as key=value, nested with brackets, e.g. -d "scopes[0][value]=payments:read"`)
	}

	fs.StringP(flagOutput, "o", "json", "Output format (json)")

	if op.Method == http.MethodDelete {
		fs.Bool(flagConfirm, false, "Skip the confirmation prompt")
	}

	return cmd
}

func run(cmd *cobra.Command, op *Operation, base string, args []string) error {
	if out, _ := cmd.Flags().GetString(flagOutput); out != "json" {
		return fmt.Errorf("unsupported output format %q: only json is supported", out)
	}

	flagKey, _ := cmd.Flags().GetString(flagAPIKey)

	apiKey, err := client.ResolveAPIKey(flagKey)
	if err != nil {
		return exitError(err)
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

	if op.Method == http.MethodDelete {
		err = confirmDelete(cmd, op, args)
		if err != nil {
			return err
		}
	}

	body, err := client.New(base, apiKey).Do(cmd.Context(), req)
	if err != nil {
		return exitError(err)
	}

	return printJSON(cmd.OutOrStdout(), body)
}

// requiredExample shows the command with every unconditionally required
// input, as the Stripe CLI's "# required fields" line does. Fields that are
// required only inside an optional object are not listed; the server reports
// those.
func requiredExample(op *Operation) string {
	if len(op.Args) == 0 && !slices.ContainsFunc(op.Params, func(p Param) bool { return p.Required }) {
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

	return b.String()
}

// buildBody assembles the request body from body flags first, then -d values,
// so a -d entry can override or extend what a flag set.
func buildBody(cmd *cobra.Command, op *Operation) (map[string]any, error) {
	var root any = map[string]any{}

	var err error

	for _, p := range op.Params {
		if p.In != InBody || !cmd.Flags().Changed(p.Flag) {
			continue
		}

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
				return nil, fmt.Errorf("--%s: %q is not one of: %s", p.Flag, s, strings.Join(p.Enum, ", "))
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
		root, err = applyData(root, op.Fields, d)
		if err != nil {
			return nil, fmt.Errorf("-d %q: %w", d, err)
		}
	}

	err = checkNoHoles(root)
	if err != nil {
		return nil, err
	}

	return root.(map[string]any), nil
}

func confirmDelete(cmd *cobra.Command, op *Operation, args []string) error {
	if ok, _ := cmd.Flags().GetBool(flagConfirm); ok {
		return nil
	}

	target := strings.Join(op.Command, " ")
	if len(args) > 0 {
		target += " " + strings.Join(args, " ")
	}

	in, ok := cmd.InOrStdin().(*os.File)
	if !ok || !isTerminal(in) {
		return fmt.Errorf("refusing to run %q without --%s when input is not a terminal", target, flagConfirm)
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "This runs %q and cannot be undone. Continue? [y/N] ", target)

	line, _ := bufio.NewReader(in).ReadString('\n')
	if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
		return errors.New("aborted")
	}

	return nil
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()

	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// printJSON writes an indented copy of body. A body that is empty, as on a 204,
// prints nothing.
func printJSON(w io.Writer, body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}

	var buf bytes.Buffer

	err := json.Indent(&buf, body, "", "  ")
	if err != nil {
		// Not JSON: pass it through rather than lose it.
		buf.Reset()
		buf.Write(body)
	}

	buf.WriteByte('\n')
	_, err = w.Write(buf.Bytes())

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
