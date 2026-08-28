package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/api"
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
		newSettingsInitCommand(env),
		newSettingsEditCommand(env),
	)
	return cmd
}

// Permissions for the settings file, which are the ones the wizard uses for a
// project's: a file naming paths in the user's home and the programs they run
// is nobody else's business.
const (
	settingsDirPerm  os.FileMode = 0o700
	settingsFilePerm os.FileMode = 0o600
)

func newSettingsInitCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Write a commented settings file",
		Long: `Write a settings file with every value shown, commented out, and explained.

Only the version is live: everything else is a default, and a default written
down is a value that stops following Feat when Feat's own changes. Uncomment what
you want to change.

An existing file is never overwritten. There is no force flag: this is a file you
authored, and losing it to a mistyped command is not a trade this makes.`,
		Args: checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, err := env.resolve()
			if err != nil {
				return err
			}

			// Both extensions, so that `init` beside an existing settings.yml
			// refuses rather than writing a second file Feat would then refuse to
			// choose between.
			found, err := config.FindSettings(layout.Config)
			if err != nil {
				return err
			}
			if found != "" {
				return fmt.Errorf("%s already exists, and nothing was written to it: "+
					"open it with `feat settings edit`", found)
			}

			path, err := writeSettingsTemplate(layout.Config)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			printf(out, "wrote %s\n", path)
			printf(out, "every value in it is commented out, so nothing has changed yet\n")
			printf(out, "edit it with `feat settings edit`, then `feat settings show` to check what Feat will act on\n")
			return nil
		},
	}
}

// writeSettingsTemplate writes the commented default and returns its path.
//
// The create is exclusive, and that is the whole of the check: a file that
// appeared between the search above and this line is still a file somebody
// wrote. It is the rule `feat project init` follows for the same reason.
func writeSettingsTemplate(dir string) (string, error) {
	if err := os.MkdirAll(dir, settingsDirPerm); err != nil {
		return "", fmt.Errorf("creating the configuration directory %s: %w", dir, err)
	}
	path := config.SettingsFile(dir)

	// #nosec G304 -- the path is the resolved configuration directory joined
	// with a constant file name; nothing here comes from a caller.
	handle, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, settingsFilePerm)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("%s already exists, and nothing was written to it", path)
		}
		return "", fmt.Errorf("creating %s: %w", path, err)
	}
	if _, err := handle.WriteString(config.SettingsTemplate); err != nil {
		_ = handle.Close()
		// Half a settings file is worse than none: the next command would read
		// it and report a parse error about a file the user never wrote.
		if removeErr := os.Remove(path); removeErr != nil {
			return "", fmt.Errorf("writing %s: %w (and it could not be removed either: %w)",
				path, err, removeErr)
		}
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	if err := handle.Close(); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

func newSettingsEditCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open the settings file in your editor",
		Long: `Open the settings file in the editor review.editor.command names, or in $EDITOR.

A machine with no settings file gets the commented default written first, so that
what opens is the file with every value in it rather than an empty buffer.

The editor keeps its own flags — ` + "`code -w`" + ` has to stay ` + "`code -w`" + ` — and the
argument that would have named a repository is dropped, because what this opens
is the settings file.

Settings that do not parse are still opened. A file with a typo in it is the one
you most need to edit, so the editor falls back to $EDITOR rather than to the
command a file Feat could not read was going to name.`,
		Args: checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, options, err := env.project()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			path, err := config.FindSettings(layout.Config)
			if err != nil {
				return err
			}
			if path == "" {
				if path, err = writeSettingsTemplate(layout.Config); err != nil {
					return err
				}
				printf(out, "wrote %s, with every value commented out\n", path)
			}

			// Loaded for the editor alone, and a failure is not one: a file that
			// does not parse is exactly the file somebody runs this to fix.
			var configured []string
			if settings, err := config.LoadSettings(layout.Config, options); err == nil {
				configured = settings.Review.DocumentEditor()
			}

			editor, err := documentEditor(editorCommand(configured), env, path)
			if err != nil {
				return err
			}
			editor.Stdin, editor.Stdout, editor.Stderr = cmd.InOrStdin(), out, cmd.ErrOrStderr()
			if err := editor.Run(); err != nil {
				return fmt.Errorf("the editor did not finish: %w", err)
			}

			// Read back rather than trusted: an editor that exited cleanly says
			// nothing about what is now in the file, and finding out here beats
			// finding out from the next daemon that fails to start.
			settings, err := config.LoadSettings(layout.Config, options)
			if err != nil {
				return configFailure(err)
			}
			printf(out, "%s is valid\n", settings.Path())
			printf(out, "a running daemon reads it once, at startup: restart it to apply this\n")
			return nil
		},
	}
}

// editorCommand splits a vector into the shape documentEditor takes.
func editorCommand(vector []string) api.EditorCommand {
	if len(vector) == 0 {
		return api.EditorCommand{}
	}
	return api.EditorCommand{Program: vector[0], Arguments: vector[1:]}
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
running — and so what it prints is what a daemon would adopt if it started now,
which is not necessarily what a daemon already running adopted. Settings are read
once at startup; restart the daemon after changing one.`,
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
			// The second line is where the cost of resolving these once is paid.
			// A daemon holds them from the moment it started, so this command can
			// print a value the running daemon is not using — and editing a
			// setting and watching nothing happen is exactly the confusion a
			// sentence here costs nothing to prevent (ADR-079).
			printf(out, "\nthese settings are global: they apply to every project on this machine\n")
			printf(out, "a running daemon reads them once, at startup: restart it after changing one\n")
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
