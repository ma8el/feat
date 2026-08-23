package claude

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ma8el/feat/internal/agent"
)

// Validate reports whether the environment can run a Claude session.
//
// It runs before anything is created. A task whose agent could never start
// should not first be given worktrees it will not use, a terminal nobody will
// attach to, and a session record describing a process that does not exist.
//
// Every probe runs through the environment's own runner, so the questions are
// asked where the agent will run rather than wherever the daemon happens to be.
// That distinction is the whole reason FR-PROJ-004 words the requirement the way
// it does, and it is what keeps this check meaningful once the agent is in a
// container.
func (a Adapter) Validate(ctx context.Context, env agent.Environment) error {
	if env.Runner == nil {
		return fmt.Errorf("validating a Claude environment needs a runner")
	}

	// The version is probed for its side effect of proving the executable runs.
	// An unverified version is deliberately not a failure here: it is a reason
	// to be careful, which `feat doctor` reports, and refusing to launch on one
	// would make every Claude release an outage until Feat caught up.
	if _, err := a.Version(ctx, env); err != nil {
		return err
	}

	var problems []error
	for _, capability := range []struct {
		level    agent.CapabilityLevel
		tool     string
		field    string
		remedy   string
		authArgs []string
	}{
		{env.GitHubCLI, "gh", "agent.capabilities.github_cli", "gh auth login", []string{"auth", "status"}},
		{env.GitLabCLI, "glab", "agent.capabilities.gitlab_cli", "glab auth login", []string{"auth", "status"}},
	} {
		if capability.level != agent.CapabilityRequired {
			// Optional and disabled capabilities are `feat doctor`'s business.
			// Launch fails only for what the project said it cannot work
			// without (docs/07-configuration-model.md).
			continue
		}
		if err := probeCLI(ctx, env, capability.tool, capability.authArgs, capability.field, capability.remedy); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// probeCLI reports whether a required provider CLI is installed and
// authenticated in the agent's environment.
func probeCLI(ctx context.Context, env agent.Environment, tool string, args []string, field, remedy string) error {
	output, err := env.Runner.Run(ctx, agent.Command{Program: tool, Arguments: args})
	if errors.Is(err, agent.ErrNotInstalled) {
		return fmt.Errorf(
			"%s is required by %s, and %s is not installed %s: install it or set %s to optional",
			tool, field, tool, where(env), field)
	}
	if err != nil {
		return fmt.Errorf("checking whether %s is authenticated %s: %w", tool, where(env), err)
	}
	if !output.Succeeded() {
		detail := firstLine(output.Stderr)
		if detail == "" {
			detail = firstLine(output.Stdout)
		}
		message := fmt.Sprintf(
			"%s is required by %s, and it is not authenticated %s: run %s",
			tool, field, where(env), remedy)
		if detail != "" {
			message += " (" + tool + " reported: " + detail + ")"
		}
		return errors.New(message)
	}
	return nil
}

// where names the environment a probe ran in, so a message about a missing tool
// says which machine to install it on.
func where(env agent.Environment) string {
	if env.OutsideConfiguredBoundary {
		return "on this host, where the agent is being launched"
	}
	if env.Mode == "devcontainer" {
		return "in the agent's devcontainer"
	}
	return "on this host"
}

// Version probes the installed Claude Code in the agent's environment.
//
// It is exported because `feat doctor` asks the same question for a different
// reason: launch needs to know the executable runs, and diagnostics need to
// report which version it is and whether Feat has been checked against it.
func (a Adapter) Version(ctx context.Context, env agent.Environment) (Version, error) {
	output, err := env.Runner.Run(ctx, agent.Command{Program: Executable, Arguments: []string{"--version"}})
	if errors.Is(err, agent.ErrNotInstalled) {
		return Version{}, fmt.Errorf(
			"the %s executable is not installed %s: install Claude Code, or change agent.provider",
			Executable, where(env))
	}
	if err != nil {
		return Version{}, fmt.Errorf("checking the %s executable %s: %w", Executable, where(env), err)
	}
	if !output.Succeeded() {
		return Version{}, fmt.Errorf("%s --version failed %s: %s",
			Executable, where(env), firstLine(output.Stderr+output.Stdout))
	}
	return ParseVersion(output.Stdout), nil
}

// Version is an installed Claude Code version.
type Version struct {
	// Text is what the CLI printed, kept for a diagnostic that could not parse
	// it.
	Text string
	// Major, Minor, and Patch are the parsed components. They are zero when the
	// version could not be parsed.
	Major, Minor, Patch int
	// Parsed reports whether the components mean anything.
	Parsed bool
}

// ParseVersion reads the version from `claude --version` output, which prints
// something like "2.1.220 (Claude Code)".
func ParseVersion(output string) Version {
	text := firstLine(output)
	version := Version{Text: text}

	fields := strings.Fields(text)
	if len(fields) == 0 {
		return version
	}
	parts := strings.Split(fields[0], ".")
	if len(parts) < 2 {
		return version
	}
	numbers := make([]int, 0, 3)
	for _, part := range parts[:min(3, len(parts))] {
		value := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				return version
			}
			value = value*10 + int(r-'0')
		}
		numbers = append(numbers, value)
	}
	version.Major = numbers[0]
	version.Minor = numbers[1]
	if len(numbers) > 2 {
		version.Patch = numbers[2]
	}
	version.Parsed = true
	return version
}

// String renders the version for a message.
func (v Version) String() string {
	if v.Text != "" {
		return v.Text
	}
	return "unknown"
}
