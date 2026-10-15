package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/skill"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/version"
)

func newSkillCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Install the Claude Code skill that teaches Claude to use aigw",
		Long: "The aigw skill tells Claude Code how to drive aigw: check the login first,\n" +
			"find commands with --help, ask before every delete, and keep secrets out of\n" +
			"chat. It ships inside aigw, so it always matches this version.\n\n" +
			"It installs to ~/.claude/skills/aigw, or $CLAUDE_CONFIG_DIR/skills/aigw when\n" +
			"that is set. Run `aigw skill install` again after upgrading aigw.",
		Args: cobra.NoArgs,
	}

	cmd.AddCommand(newSkillInstallCommand(), newSkillUninstallCommand(), newSkillShowCommand())

	return cmd
}

func newSkillInstallCommand() *cobra.Command {
	var confirm bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install or update the skill for Claude Code",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := skill.Dir()
			if err != nil {
				return err
			}

			res, err := skill.Install(dir, skill.Render(version.Version), confirm)
			if errors.Is(err, skill.ErrNotOurs) {
				return fmt.Errorf("%w; pass --confirm to replace it", err)
			}

			if err != nil {
				return err
			}

			switch res {
			case skill.UpToDate:
				fmt.Fprintf(cmd.ErrOrStderr(), "The aigw skill in %s is already up to date.\n", dir)
			case skill.Updated:
				fmt.Fprintf(cmd.ErrOrStderr(), "Updated the aigw skill in %s.\n", dir)
			default:
				fmt.Fprintf(cmd.ErrOrStderr(), "Installed the aigw skill in %s.\n"+
					"Claude Code picks it up in new sessions.\n", dir)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&confirm, "confirm", false, "Replace a SKILL.md that aigw did not write")

	return cmd
}

func newSkillUninstallCommand() *cobra.Command {
	var confirm bool

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the skill from Claude Code",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := skill.Dir()
			if err != nil {
				return err
			}

			res, err := skill.Uninstall(dir, confirm)
			if errors.Is(err, skill.ErrNotOurs) {
				return fmt.Errorf("%w; pass --confirm to remove it", err)
			}

			if err != nil {
				return err
			}

			if res == skill.NotInstalled {
				fmt.Fprintf(cmd.ErrOrStderr(), "The aigw skill is not installed in %s.\n", dir)
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "Removed the aigw skill from %s.\n", dir)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&confirm, "confirm", false, "Remove a SKILL.md that aigw did not write")

	return cmd
}

func newSkillShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the skill, e.g. to add it to another AI tool",
		Example: "  aigw skill show\n" +
			"  aigw skill show > AGENTS.md",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), skill.Render(version.Version))

			return err
		},
	}
}
