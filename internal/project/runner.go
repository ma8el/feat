package project

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// commandTimeout bounds one diagnostic command. Every command diagnostics runs
// reports something a tool already knows, so one that has not answered in this long
// is stuck rather than slow and `feat doctor` says so instead of hanging.
const commandTimeout = 30 * time.Second

// ErrNotInstalled reports an executable that is not on the path.
var ErrNotInstalled = errors.New("not installed")

// Runner runs host commands for diagnostics.
//
// It is an interface so diagnostics can be tested without the tools they look for: a
// test that needs Git absent should not have to uninstall Git. Opt-in integration
// tests use the real implementation.
type Runner interface {
	// Look resolves an executable on the path, returning an error matching
	// ErrNotInstalled when it is absent.
	Look(name string) (string, error)
	// Run executes a command and returns its standard output. The command is
	// an argument vector: nothing is handed to a shell to re-split (CLAUDE.md
	// architectural rules).
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

// HostRunner runs commands on the machine Feat is running on.
type HostRunner struct{}

// Look resolves an executable on the path.
func (HostRunner) Look(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s is %w", name, ErrNotInstalled)
	}
	return path, nil
}

// Run executes one command and returns its standard output.
//
// Standard output and standard error are kept apart on purpose. Diagnostics read the
// output and may report it, while error output is summarised to one line, because a
// tool's failure message is the actionable part.
//
// A failure is summarised from standard error and then from standard output, because
// the failing tool chooses which of the two carries the reason. `docker exec` writes
// "executable file not found in $PATH" to standard output and exits 127 with an empty
// standard error, which is how containerRunner tells "there is nothing to run" from
// "it ran and disagreed".
func (HostRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir

	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()
	if ctx.Err() != nil {
		return "", fmt.Errorf("%s did not answer within %s", name, commandTimeout)
	}
	if err != nil {
		if detail := firstLine(stderr.String(), stdout.String()); detail != "" {
			return "", fmt.Errorf("%s: %s", name, detail)
		}
		return "", fmt.Errorf("running %s: %w", name, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// containerRunner runs diagnostic commands inside a running devcontainer.
//
// It exists so the agent checks are the same checks wherever they run: the caller
// asks whether Claude is installed and whether a provider CLI is authenticated, and
// only this type knows the answer comes from inside a container. FR-PROJ-004 is
// worded around the environment the agent uses, and an authenticated CLI on the host
// is not an answer about a container.
type containerRunner struct {
	// host runs Docker on this machine. Only the host ever runs Docker.
	host Runner
	// container is the container to look inside.
	container string
	// user is the identity to ask as, which is the one the agent runs as.
	user string
}

var _ Runner = containerRunner{}

// Look reports whether an executable exists in the container.
//
// It runs the tool rather than asking a shell: `command -v` is a builtin and `docker
// exec` starts a program rather than a shell, so asking that way would report every
// tool as missing. A tool that runs and fails is still installed, and the two answers
// are fixed in different ways.
func (r containerRunner) Look(name string) (string, error) {
	_, err := r.Run(context.Background(), "", name, "--version")
	if errors.Is(err, ErrNotInstalled) {
		return "", err
	}
	return name + " in container " + r.container, nil
}

// Run executes one command inside the container as the agent's own user.
func (r containerRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	vector := []string{"exec", "--user", r.user}
	if dir != "" {
		vector = append(vector, "--workdir", dir)
	}
	vector = append(vector, r.container, name)
	vector = append(vector, args...)

	output, err := r.host.Run(ctx, "", dockerExecutable, vector...)
	if err != nil && missingInContainer(err) {
		return output, fmt.Errorf("%s is %w", name, ErrNotInstalled)
	}
	return output, err
}

// missingInContainer reports whether a failure means the container has no such
// executable.
func missingInContainer(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "executable file not found") ||
		strings.Contains(message, "no such file or directory")
}

// firstLine returns the first non-empty line of a command's output, taking the
// streams in the order given so a caller can say which one to prefer.
func firstLine(outputs ...string) string {
	for _, output := range outputs {
		for _, line := range strings.Split(output, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}
