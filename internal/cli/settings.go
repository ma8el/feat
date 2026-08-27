package cli

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/config"
)

const settingsLong = `Inspect the settings Feat applies to this machine and this user.

Some of what Feat is told is not a fact about a project: how often the machine's
resources are sampled produces one number for the whole machine, notifications
are about the person at the keyboard, and the review commands are that person's
own tools. Those live in one file in the configuration directory, beside the
per-project files rather than inside them.

The file is optional, and every value in it has a default, so a machine that has
never written one is fully configured. It is global: there is no per-project
override.

This is not ` + "`feat project show`" + `, which prints one project's configuration.`

func newSettingsCommand(env *environment) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Inspect Feat's global settings",
		Long:  settingsLong,
	}
	cmd.AddCommand(
		newSettingsShowCommand(env),
		newSettingsPathCommand(env),
	)
	return cmd
}

func newSettingsShowCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the resolved global settings",
		Long: `Print the settings Feat will act on, with every default filled in and each
value marked with where it came from.

A value marked ` + "`default`" + ` is one Feat chose; a value marked ` + "`configured`" + ` is one
the settings file sets. The editor may also be marked ` + "`from $EDITOR`" + `, which is
where it comes from when nothing configures it.

It reads the file directly and asks no daemon anything, so it works before one is
running.`,
		Args: checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, options, err := env.project()
			if err != nil {
				return err
			}

			settings, err := config.LoadSettings(layout.Config, options)
			if err != nil {
				return configFailure(err)
			}

			out := cmd.OutOrStdout()
			printSettingsSource(out, settings, config.SettingsFile(layout.Config))
			for _, section := range settings.Describe() {
				printf(out, "\n%s\n", section.Title)
				for _, field := range section.Fields {
					if field.Note != "" {
						printf(out, "  %-26s %s  (%s)\n", field.Name, field.Value, field.Note)
						continue
					}
					printf(out, "  %-26s %s\n", field.Name, field.Value)
				}
			}
			printf(out, "\nthese settings are global: they apply to every project on this machine\n")
			return nil
		},
	}
}

// printSettingsSource says which file the values came from, or that there is
// none.
//
// A machine with no file is the normal case rather than a problem, so it is
// reported as what it is — everything below is a default — and the path it
// would be written at is named, since that is the next thing somebody wanting
// to change one needs.
func printSettingsSource(out io.Writer, settings *config.Settings, expected string) {
	if settings.Path() == "" {
		printf(out, "no settings file: every value below is a default\n")
		printf(out, "write one at %s to change any of them\n", expected)
		return
	}
	printf(out, "%s\n", settings.Path())
}

func newSettingsPathCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print where the global settings file belongs",
		Long: `Print the path of the settings file.

The path is printed whether or not the file exists, because the two questions a
caller has are where to write one and whether one is there, and a command that
printed nothing for a missing file could answer only the second. Whether it
exists is said on standard error, so the path alone is what a pipe receives.`,
		Args: checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, err := env.resolve()
			if err != nil {
				return err
			}

			found, err := config.FindSettings(layout.Config)
			if err != nil {
				return err
			}

			path := found
			if path == "" {
				path = config.SettingsFile(layout.Config)
			}
			printf(cmd.OutOrStdout(), "%s\n", path)
			if found == "" {
				printf(cmd.ErrOrStderr(), "no settings file is there yet; Feat is running on the defaults\n")
			}
			return nil
		},
	}
}
