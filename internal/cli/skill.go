package cli

import (
	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/agent/claude"
)

const skillLong = `The setup skill teaches your own Claude Code session how to set up and edit a
project configuration using Feat's own commands.

Feat carries it in the binary, so the skill on disk is written by the build it
matches.`

func newSkillCommand(env *environment) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage the setup skill Feat installs for your agent",
		Long:  skillLong,
	}
	cmd.AddCommand(
		newSkillInstallCommand(env),
		newSkillShowCommand(),
	)
	return cmd
}

func newSkillShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the setup skill this build would install",
		Long: `Print the setup skill document, byte for byte as ` + "`feat skill install`" + ` writes
it.

Read it before installing, or diff it against an installed copy an install
refused to replace. ` + "`feat skill install --dry-run`" + ` says what an install would
do on this machine.`,
		Args: checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write(claude.Skill())
			return err
		},
	}
}

func newSkillInstallCommand(env *environment) *cobra.Command {
	var force, dryRun bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write the setup skill where Claude Code discovers it",
		Long: `Write the setup skill into Claude Code's skills directory — ~/.claude/skills,
unless CLAUDE_CONFIG_DIR says otherwise. Sessions discover skills when they
start.

Feat records what it wrote. Installing again — after an upgrade, say — replaces
the file while it still matches that record; one that does not match was edited
or written by something else, so it is refused with the reason, and --force
replaces it anyway.`,
		Args: checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			current, err := env.current()
			if err != nil {
				return err
			}
			dir := claude.SkillDir(current.Getenv, current.Home)
			out := cmd.OutOrStdout()

			if dryRun {
				// The same decision the install runs, including the same
				// refusal with the same exit code — what a dry run must not
				// share is only the writing.
				planned, err := claude.PlanSkillInstall(dir, force)
				if err != nil {
					return err
				}
				verb := "would install"
				if planned.Replaced {
					verb = "would replace"
				}
				printf(out, "%s %s\n", verb, planned.Path)
				printf(out, "Nothing was written: this was a dry run.\n")
				return nil
			}

			installed, err := claude.InstallSkill(dir, env.build.Version, force)
			if err != nil {
				return err
			}

			verb := "installed"
			if installed.Replaced {
				verb = "replaced"
			}
			printf(out, "%s %s\n", verb, installed.Path)
			printf(out, "Claude Code reads skills when a session starts, so it appears in your next session\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"print what an install would do instead of writing anything")
	cmd.Flags().BoolVar(&force, "force", false,
		"replace the installed skill even when it is not what Feat recorded writing")
	return cmd
}
