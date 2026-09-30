package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/version"
)

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the aigw version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// OutOrStdout rather than fmt.Println, so tests can capture it.
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "aigw %s (%s)\n", version.Version, version.GitCommit)

			return err
		},
	}
}
