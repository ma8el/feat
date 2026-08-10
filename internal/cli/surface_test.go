package cli

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestCommandSurface pins the slice 0 acceptance criterion that `feat --help`
// shows the intended top-level command model.
//
// It compares a normalized rendering of the command tree rather than cobra's
// help text, so that a cobra formatting change does not fail the build while a
// change to the command model still does.
func TestCommandSurface(t *testing.T) {
	golden := filepath.Join("testdata", "command-surface.golden")
	got := renderCommandSurface(NewRootCommand(Options{}))

	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("writing %s: %v", golden, err)
		}
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading %s: %v\nRun: go test ./internal/cli -run TestCommandSurface -update", golden, err)
	}

	if got != string(want) {
		t.Errorf("command surface changed.\n\ngot:\n%s\nwant:\n%s\n"+
			"The command model is documented in docs/README.md. If this change is intended, update both, then run:\n"+
			"\tgo test ./internal/cli -run TestCommandSurface -update", got, want)
	}
}

// renderCommandSurface renders one line per command, deepest-last within each
// level and alphabetically ordered, with local flags appended.
func renderCommandSurface(root *cobra.Command) string {
	var out strings.Builder

	var walk func(cmd *cobra.Command, prefix string)
	walk = func(cmd *cobra.Command, prefix string) {
		line := strings.TrimSpace(prefix + " " + cmd.Use)
		for _, name := range localFlagNames(cmd) {
			line += " [--" + name + "]"
		}
		out.WriteString(line + "\n")

		children := cmd.Commands()
		sort.Slice(children, func(i, j int) bool {
			return children[i].Name() < children[j].Name()
		})
		for _, child := range children {
			walk(child, strings.TrimSpace(prefix+" "+cmd.Name()))
		}
	}
	walk(root, "")

	return out.String()
}

func localFlagNames(cmd *cobra.Command) []string {
	var names []string
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		names = append(names, f.Name)
	})
	sort.Strings(names)
	return names
}

// TestPlaceholdersDeclareOwningSlice checks that every command a later slice
// delivers reports which slice that is, instead of failing vaguely or, worse,
// reporting success.
func TestPlaceholdersDeclareOwningSlice(t *testing.T) {
	// Commands that are implemented. Their RunE is not invoked here: the root
	// command opens the dashboard, the daemon commands start, stop, and inspect
	// a background process, and the project and doctor commands read the
	// running user's configuration directory. Leaving one of them out of this
	// list would make this test run it.
	implemented := map[string]bool{
		"feat":               true,
		"feat version":       true,
		"feat daemon start":  true,
		"feat daemon stop":   true,
		"feat daemon status": true,
		"feat daemon run":    true,
		"feat doctor":        true,
		"feat project add":   true,
		"feat project list":  true,
		"feat project show":  true,
		"feat implement":     true,
		"feat task list":     true,
		// Attaching yields this process's terminal to native tmux, under both
		// the canonical name and the alias ADR-040 kept at the top level.
		"feat task attach": true,
		"feat attach":      true,
		// Review reaches the daemon and reads the task's worktrees. Invoking it
		// here would ask the running user's daemon to observe one of their
		// tasks.
		"feat task review": true,
		"feat review":      true,
		// The runtime actions reach the daemon and act on one task's Compose
		// project. Invoking one here would ask the running user's daemon to start
		// or remove something.
		"feat runtime create":  true,
		"feat runtime start":   true,
		"feat runtime stop":    true,
		"feat runtime status":  true,
		"feat runtime logs":    true,
		"feat runtime destroy": true,
		// Cleanup reaches the daemon and resolves one task's resources. Invoking
		// it here would ask the running user's daemon about one of their tasks,
		// and answering "yes" to a prompt would remove it.
		"feat task cleanup": true,
	}

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.RunE != nil && !implemented[cmd.CommandPath()] {
			err := cmd.RunE(cmd, nil)

			var notImplemented *NotImplementedError
			switch {
			case !errors.As(err, &notImplemented):
				t.Errorf("%s: want a NotImplementedError, got %v", cmd.CommandPath(), err)
			case notImplemented.Slice < 1 || notImplemented.Slice > 14:
				t.Errorf("%s: slice %d is outside the plan in docs/11-implementation-plan.md",
					cmd.CommandPath(), notImplemented.Slice)
			case notImplemented.Outcome == "":
				t.Errorf("%s: placeholder does not describe what slice %d delivers",
					cmd.CommandPath(), notImplemented.Slice)
			case notImplemented.Command != cmd.CommandPath():
				t.Errorf("%s: error names the command %q", cmd.CommandPath(), notImplemented.Command)
			}
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(NewRootCommand(Options{}))
}

// TestAnAliasIsOneImplementationUnderTwoNames checks ADR-040's rule for the
// short names it kept at the top level.
//
// What it compares is the code pointer behind each command's RunE. An alias is
// given the canonical command's own function value, so the two agree by
// construction and this fails the moment somebody gives the alias a body of its
// own and answers a change to one by editing the other.
//
// It deliberately does not call the same constructor twice and compare the
// results. Two closures over one function literal do not share a code pointer:
// the compiler inlines the constructor at each call site, which is what
// `NewRootCommand.newAttachCommand.func4` and `newTaskCommand.newAttachCommand.func2`
// were before the alias was made a copy instead.
func TestAnAliasIsOneImplementationUnderTwoNames(t *testing.T) {
	aliases := map[string]string{
		"feat attach": "feat task attach",
		"feat review": "feat task review",
	}

	commands := map[string]*cobra.Command{}
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		commands[cmd.CommandPath()] = cmd
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(NewRootCommand(Options{}))

	for name, canonicalName := range aliases {
		alias, ok := commands[name]
		if !ok {
			t.Errorf("%s: no such command", name)
			continue
		}
		canonical, ok := commands[canonicalName]
		if !ok {
			t.Errorf("%s: no such command", canonicalName)
			continue
		}

		if reflect.ValueOf(alias.RunE).Pointer() != reflect.ValueOf(canonical.RunE).Pointer() {
			t.Errorf("%s runs a different implementation from %s", name, canonicalName)
		}
		if !alias.Hidden {
			// Hidden keeps `feat --help` equal to the documented surface, which
			// is the whole reason an alias is allowed to exist (ADR-027).
			t.Errorf("%s is an alias and is not hidden", name)
		}
		if canonical.Hidden {
			t.Errorf("%s is the canonical command and is hidden", canonicalName)
		}
		if !strings.Contains(alias.Long, canonicalName) {
			t.Errorf("%s does not say in its help that it is %s", name, canonicalName)
		}
	}
}
