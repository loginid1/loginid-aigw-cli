package apicmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/config"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/credentials"
)

// Flag names every operation command shares. The generator refuses a spec whose
// body fields would collide with one of these.
const (
	flagAPIKey  = "api-key"
	flagData    = "data"
	flagOutput  = "output"
	flagConfirm = "confirm"

	envAPIKey = "AIGW_API_KEY" //nolint:gosec // the name of an env var, not a credential

	// TEMP: see AddAll.
	envTempAPIBase = "AIGW_TEMP_API_BASE"

	requestTimeout = 30 * time.Second
)

// AddAll registers every generated operation under root, building the noun
// commands the operations' paths name along the way.
//
// Requests go to DefaultAPIBase, the spec's server. The base is not a flag: a
// key sent to a URL the user typed would leak.
//
// TEMP: envTempAPIBase overrides it, for testing against local and staging
// servers until there is a proper answer for those. It is left out of all help
// text on purpose, and every run that uses it says so on stderr. Remove it
// once that answer exists.
func AddAll(root *cobra.Command) {
	base := DefaultAPIBase

	if override := os.Getenv(envTempAPIBase); override != "" {
		fmt.Fprintf(os.Stderr, "warning: %s is set; API requests go to %s\n", envTempAPIBase, override)

		base = override
	}

	addAll(root, base)
}

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
				pf.String(flagAPIKey, "", "API key to authenticate with (env "+envAPIKey+")")
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

	apiKey, err := resolveAPIKey(cmd)
	if err != nil {
		return err
	}

	reqURL := buildURL(cmd, op, base, args)

	var body io.Reader

	if op.HasBody {
		payload, bodyErr := buildBody(cmd, op)
		if bodyErr != nil {
			return bodyErr
		}

		raw, encErr := json.Marshal(payload)
		if encErr != nil {
			return fmt.Errorf("encode request body: %w", encErr)
		}

		body = bytes.NewReader(raw)
	}

	if op.Method == http.MethodDelete {
		confirmErr := confirmDelete(cmd, op, args)
		if confirmErr != nil {
			return confirmErr
		}
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, op.Method, reqURL, body) //nolint:gosec // TEMP: base can come from AIGW_TEMP_API_BASE; see AddAll
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // TEMP: see AddAll
	if err != nil {
		return fmt.Errorf("%s %s: %w", op.Method, op.Path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return responseError(op, resp.StatusCode, respBody)
	}

	return printJSON(cmd.OutOrStdout(), respBody)
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

// resolveAPIKey takes the key from --api-key, then AIGW_API_KEY, then the key
// `aigw login` saved.
func resolveAPIKey(cmd *cobra.Command) (string, error) {
	if v, _ := cmd.Flags().GetString(flagAPIKey); v != "" {
		return v, nil
	}

	if v := os.Getenv(envAPIKey); v != "" {
		return v, nil
	}

	dir, err := config.Dir()
	if err != nil {
		return "", err
	}

	key, err := credentials.Load(dir, config.DefaultProfile)
	if errors.Is(err, credentials.ErrNotLoggedIn) {
		return "", &ExitError{Code: ExitNotAuthenticated, Err: fmt.Errorf("not logged in: run `aigw login`, pass --%s, or set %s", flagAPIKey, envAPIKey)}
	}

	if err != nil {
		return "", &ExitError{Code: ExitNotAuthenticated, Err: err}
	}

	return key, nil
}

func buildURL(cmd *cobra.Command, op *Operation, base string, args []string) string {
	path := op.Path
	for i, a := range op.Args {
		path = strings.Replace(path, "{"+a.Name+"}", url.PathEscape(args[i]), 1)
	}

	query := url.Values{}

	for _, p := range op.Params {
		switch p.In {
		case InPath:
			v, _ := cmd.Flags().GetString(p.Flag)
			path = strings.Replace(path, "{"+p.Name+"}", url.PathEscape(v), 1)
		case InQuery:
			if cmd.Flags().Changed(p.Flag) {
				query.Set(p.Name, cmd.Flags().Lookup(p.Flag).Value.String())
			}
		}
	}

	u := strings.TrimSuffix(base, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	return u
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
