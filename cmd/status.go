package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/apicmd"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/client"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/credentials"
)

// Values of statusReport.Check.
const (
	checkOK          = "ok"
	checkFailed      = "failed"
	checkNotLoggedIn = "not_logged_in"
)

// statusReport is what `aigw status` prints. Its JSON names are a contract for
// scripts and agents: add fields, do not rename them.
type statusReport struct {
	APIBase   string `json:"apiBase"`
	KeyHint   string `json:"keyHint,omitempty"`
	KeySource string `json:"keySource,omitempty"`
	Check     string `json:"check"`
	Error     string `json:"error,omitempty"`
	LatencyMS int64  `json:"latencyMs,omitempty"`
}

func newStatusCommand() *cobra.Command {
	var output, apiKey string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the API and key in use, and check the key",
		Long: "status shows the API aigw calls, and which key it sends and where that key\n" +
			"came from. Then it lists projects with the key.\n\n" +
			"It exits 0 when the key works, 3 when there is no key or the API rejects it,\n" +
			"4 when the key lacks project:read, and 1 for anything else.",
		Example: "  aigw status\n" +
			"  aigw status -o json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if output != outputText && output != outputJSON {
				return fmt.Errorf("unsupported output format %q: use text or json", output)
			}

			report, result := checkStatus(cmd, apiKey)

			err := printStatus(cmd.OutOrStdout(), output, &report)
			if err != nil {
				return err
			}

			return result
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", outputText, "Output format (text, json)")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "API key to check instead of the profile's (env "+client.EnvAPIKey+")")

	return cmd
}

// checkStatus fills the report and returns the error that sets the exit code.
func checkStatus(cmd *cobra.Command, flagKey string) (statusReport, error) {
	report := statusReport{APIBase: apicmd.APIBase}

	key, err := client.ResolveAPIKey(flagKey)
	if errors.Is(err, client.ErrNotLoggedIn) {
		report.Check = checkNotLoggedIn
		err = apicmd.ExitErrorFor(err)
		report.Error = err.Error()

		return report, err
	}

	if err != nil {
		// As for any command: a key that cannot be read is exit 3.
		var exitErr *apicmd.ExitError
		if !errors.As(apicmd.ExitErrorFor(err), &exitErr) {
			err = &apicmd.ExitError{Code: apicmd.ExitNotAuthenticated, Err: fmt.Errorf("read the saved API key: %w", err)}
		}

		report.Check = checkFailed
		report.Error = err.Error()

		return report, err
	}

	report.KeyHint = credentials.Hint(key.Value)
	report.KeySource = key.Source

	start := time.Now()
	err = apicmd.ProbeKey(cmd.Context(), apicmd.APIBase, key.Value)
	report.LatencyMS = time.Since(start).Milliseconds()

	if err != nil {
		report.Check = checkFailed
		report.Error = err.Error()

		return report, err
	}

	report.Check = checkOK

	return report, nil
}

func printStatus(w io.Writer, output string, r *statusReport) error {
	if output == outputJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		return enc.Encode(r)
	}

	key := "none"
	if r.KeyHint != "" {
		key = r.KeyHint + " (" + r.KeySource + ")"
	}

	check := r.Check
	if r.Check == checkOK {
		check = fmt.Sprintf("ok (%d ms)", r.LatencyMS)
	}

	_, err := fmt.Fprintf(w, "API:    %s\nKey:    %s\nCheck:  %s\n", r.APIBase, key, check)

	return err
}
