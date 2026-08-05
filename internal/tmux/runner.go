package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Executable is the terminal multiplexer Feat drives. It is never a shell.
const Executable = "tmux"

const commandTimeout = 15 * time.Second

// ErrServerNotRunning reports that the dedicated socket has no tmux server.
// Discovery treats it as an empty result; operations that need an existing
// target retain it as an actionable error.
var ErrServerNotRunning = errors.New("the dedicated tmux server is not running")

// Runner executes non-interactive tmux control commands.
//
// The socket is explicit on every call. A fake runner pins every argument
// vector in unit tests; opt-in integration tests ask the real tmux executable.
type Runner interface {
	Run(ctx context.Context, socket string, args ...string) (string, error)
}

// HostRunner runs the installed tmux executable.
type HostRunner struct {
	// Timeout bounds a control command. Zero uses the package default. Native
	// attachment is deliberately not handled here because it lasts until the
	// user detaches and belongs to the CLI process.
	Timeout time.Duration
}

// Run executes one tmux command as an argument vector.
func (r HostRunner) Run(ctx context.Context, socket string, args ...string) (string, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = commandTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	vector := append([]string{"-S", socket}, args...)
	// #nosec G204 -- Executable is a package constant and vector is passed
	// directly to tmux without shell interpolation.
	command := exec.CommandContext(ctx, Executable, vector...)

	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()
	if ctx.Err() != nil {
		return "", &TimeoutError{Socket: socket, Args: args, Timeout: timeout}
	}
	if err == nil {
		return strings.TrimSpace(stdout.String()), nil
	}

	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return "", fmt.Errorf("running tmux on %s: %w", socket, err)
	}

	failure := &ExitError{
		Socket: socket,
		Args:   append([]string(nil), args...),
		Code:   exit.ExitCode(),
		Stdout: strings.TrimSpace(stdout.String()),
		Stderr: strings.TrimSpace(stderr.String()),
	}
	if failure.Code == 1 && missingServer(failure.Stderr) {
		return "", fmt.Errorf("%w on %s: %w", ErrServerNotRunning, socket, failure)
	}
	return "", failure
}

// ExitError reports a tmux command that ran and failed.
type ExitError struct {
	Socket string
	Args   []string
	Code   int
	Stdout string
	Stderr string
}

func (e *ExitError) Error() string {
	message := fmt.Sprintf("`tmux -S %s %s` failed with exit code %d",
		e.Socket, strings.Join(e.Args, " "), e.Code)
	if line := firstLine(e.Stderr); line != "" {
		message += ": " + line
	}
	return message
}

// TimeoutError reports a tmux control command that did not answer.
type TimeoutError struct {
	Socket  string
	Args    []string
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("`tmux -S %s %s` did not answer within %s",
		e.Socket, strings.Join(e.Args, " "), e.Timeout)
}

func missingServer(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "no server running") ||
		strings.Contains(lower, "no such file or directory") ||
		strings.Contains(lower, "no such file")
}

func firstLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
