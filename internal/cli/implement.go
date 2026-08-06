package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/daemon"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/ui"
)

// maxBriefBytes bounds an imported brief on the client side.
//
// The daemon applies its own limit; reading a whole disk image into memory
// before being told so is worth avoiding here as well.
const maxBriefBytes = 256 << 10

const implementLong = `Open task preparation.

You choose the project, write the task brief, and select which repositories the
task may read and write. Feat then fetches, resolves each repository's base
commit, and proposes the branches and worktree paths it would create.

Nothing is created until you confirm that proposal, and confirming creates
exactly what was displayed: a draft that changed in between is refused rather
than launched.`

func newImplementCommand(env *environment) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "implement",
		Short: "Prepare and launch a task",
		Long:  implementLong,
		Args:  checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			file, err := cmd.Flags().GetString("file")
			if err != nil {
				return err
			}
			project, err := cmd.Flags().GetString("project")
			if err != nil {
				return err
			}
			if project != "" {
				// Validated here so that a malformed identifier is a usage
				// error rather than something the daemon has to explain.
				if err := domain.ProjectID(project).Validate(); err != nil {
					return err
				}
			}

			brief, source, err := readBrief(file)
			if err != nil {
				return err
			}

			// Preparation is interactive: the confirmation is the whole point,
			// and there is nothing to confirm with in a pipe. Reporting that is
			// better than opening a screen nobody can answer.
			if !env.interactive {
				return errors.New(
					"`feat implement` needs a terminal, because a task is not created until you confirm it")
			}

			layout, err := env.resolve()
			if err != nil {
				return err
			}
			// Preparation writes state, so it is an explicit mutation and does
			// not start a daemon, for the reason ADR-028 gives for
			// `feat project add`.
			if status := daemon.Inspect(layout); !status.Running() {
				return &NotRunningError{Socket: layout.Socket}
			}

			caller := client.New(layout.Socket)
			defer caller.Close()
			current, err := env.current()
			if err != nil {
				return err
			}

			return ui.Run(cmd.Context(), ui.Options{
				Backend: &backend{client: caller, env: current},
				Daemon:  describeBackend(cmd, caller, layout),
				Project: project,
				Prepare: true,
				Brief:   brief,
				Source:  source,
			})
		},
	}
	cmd.Flags().String("file", "", "read the task brief from a Markdown file")
	cmd.Flags().String("project", "", "prepare the task in this project")
	return cmd
}

// readBrief reads an imported Markdown brief.
//
// The client reads it, not the daemon: the file is one the user named, and no
// caller-supplied filesystem path crosses the socket (ADR-028). What is sent is
// the content, with the path recorded only so that the task can say where its
// brief came from.
func readBrief(file string) (string, api.Source, error) {
	if file == "" {
		return "", api.Source{Kind: string(domain.SourcePrompt)}, nil
	}

	path, err := filepath.Abs(file)
	if err != nil {
		return "", api.Source{}, fmt.Errorf("resolving %s: %w", file, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", api.Source{}, fmt.Errorf("reading the task brief: %w", err)
	}
	if info.IsDir() {
		return "", api.Source{}, fmt.Errorf("%s is a directory, not a task brief", path)
	}
	if info.Size() > maxBriefBytes {
		return "", api.Source{}, fmt.Errorf("the task brief %s is %d bytes, and the limit is %d",
			path, info.Size(), maxBriefBytes)
	}

	content, err := os.ReadFile(path) // #nosec G304 -- the user named this file on their own command line
	if err != nil {
		return "", api.Source{}, fmt.Errorf("reading the task brief: %w", err)
	}
	return string(content), api.Source{Kind: string(domain.SourceMarkdown), Reference: path}, nil
}

// describeBackend reports which daemon the dashboard is talking to.
func describeBackend(cmd *cobra.Command, caller *client.Client, layout paths.Layout) ui.Daemon {
	described := ui.Daemon{Socket: layout.Socket}
	if health, err := caller.Health(cmd.Context()); err == nil {
		described.Version = health.Daemon.Version
	}
	return described
}
