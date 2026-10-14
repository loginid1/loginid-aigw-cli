// Package cmd holds the aigw command tree. One file per noun.
package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/apicmd"
)

// NewRootCommand builds the command tree. It is exported so tests can drive a
// fresh tree without the global state a package-level command would carry.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "aigw",
		Short: "Command-line interface for the AIGW Developer Console",
		Long: "aigw drives the AIGW Developer Console from a terminal, so setup can be\n" +
			"scripted and checked into version control instead of clicked through.",

		// Errors are printed once, by Execute. Without these, cobra prints the
		// error itself and then dumps the whole usage block after it, which
		// buries the one line that matters.
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newVersionCommand(), newLoginCommand(), newLogoutCommand(), newStatusCommand())
	apicmd.AddAll(root)

	return root
}

// ExitCode maps an error from Execute to the process exit code: 0 for none, the
// code an apicmd.ExitError carries, and 1 for anything else.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}

	var exitErr *apicmd.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}

	return 1
}

// Execute runs the command tree and reports whether it failed. main decides the
// exit code; this only owns the message.
func Execute() error {
	err := NewRootCommand().Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}

	return err
}
