package cmd

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/apicmd"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/mcpserver"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/version"
)

func newMCPCommand() *cobra.Command {
	var readOnly, exposeSecrets bool

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run an MCP server so AI agents can call the API",
		Long: "mcp serves the devcon API as Model Context Protocol tools over stdio, one tool\n" +
			"per API command. An MCP client such as Claude Code starts it; it is not meant\n" +
			"to be run by hand.\n\n" +
			"Tools use the same API key as the CLI: `aigw login`, or AIGW_API_KEY in the\n" +
			"server's environment.\n\n" +
			"Tools whose response carries a one-time secret (a new client secret) are left\n" +
			"out unless --expose-secrets is set: the secret would enter the model's\n" +
			"context, and the API never shows it again.",
		Example: "  claude mcp add aigw -- aigw mcp --read-only",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			target, err := apicmd.ResolveTarget()
			if err != nil {
				return err
			}

			target.Warn(cmd.ErrOrStderr())

			srv := mcpserver.New(apicmd.Operations, mcpserver.Options{
				BaseURL:       target.BaseURL,
				TemporaryBase: target.Temporary,
				ReadOnly:      readOnly,
				ExposeSecrets: exposeSecrets,
				Version:       version.Version,
			})

			// stdout carries the protocol; anything else must go to stderr.
			err = srv.Run(ctx, &mcp.StdioTransport{})

			// MCP clients stop a server with SIGINT or SIGTERM. That is a
			// normal shutdown, not "Error: context canceled" and exit 1.
			if errors.Is(err, context.Canceled) && ctx.Err() != nil {
				return nil
			}

			return err
		},
	}

	cmd.Flags().BoolVar(&readOnly, "read-only", false, "Offer only tools that read (GET requests)")
	cmd.Flags().BoolVar(&exposeSecrets, "expose-secrets", false, "Also offer tools that return a one-time secret")

	return cmd
}
