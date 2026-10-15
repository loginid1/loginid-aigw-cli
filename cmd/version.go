package cmd

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/apicmd"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/version"
)

// Values of the -o flag on the hand-written commands.
const (
	outputText = "text"
	outputJSON = "json"
)

// versionLine is what both `aigw version` and `aigw --version` print.
func versionLine() string {
	return fmt.Sprintf("aigw %s (%s)\n", version.Version, version.GitCommit)
}

// versionReport is `aigw version -o json`. Its names are a contract for
// scripts and agents: add fields, do not rename them.
type versionReport struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	// SpecRef is the devcon spec commit the API commands were generated from.
	SpecRef string `json:"specRef"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func newVersionCommand() *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the aigw version",
		Example: "  aigw version\n" +
			"  aigw version -o json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch output {
			case outputText:
				// OutOrStdout rather than fmt.Println, so tests can capture it.
				_, err := fmt.Fprint(cmd.OutOrStdout(), versionLine())

				return err
			case outputJSON:
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")

				return enc.Encode(versionReport{
					Version: version.Version,
					Commit:  version.GitCommit,
					SpecRef: apicmd.SpecRef,
					Go:      runtime.Version(),
					OS:      runtime.GOOS,
					Arch:    runtime.GOARCH,
				})
			default:
				return fmt.Errorf("unsupported output format %q: use text or json", output)
			}
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", outputText, "Output format (text, json)")

	return cmd
}
