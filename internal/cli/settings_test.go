package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// settings writes a global settings file onto the machine.
func (m *machine) settings(t *testing.T, body string) string {
	t.Helper()
	file := filepath.Join(m.layout.Config, "settings.yaml")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the settings file: %v", err)
	}
	return file
}

// TestSettingsShowRunsWithoutAFileOrADaemon covers the state every machine
// starts in.
//
// There is no file and no daemon, and the command still prints the settings
// Feat will act on, because every one of them has a default. It also has to say
// that they are defaults and where a file would go, or a user wanting to change
// one is left with values and no way to reach them.
func TestSettingsShowRunsWithoutAFileOrADaemon(t *testing.T) {
	machine := prepare(t)

	code, stdout, stderr := machine.run(t, "settings", "show")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
	}

	for _, want := range []string{
		"no settings file",
		filepath.Join(machine.layout.Config, "settings.yaml"),
		"review", "notifications", "resources",
		"sample_interval", "2s",
		"idle_grace_period", "5s",
		"suppress_while_attached",
		"git diff {base_commit}",
		// $EDITOR is set on the test machine, and the row says so rather than
		// reading as a value somebody configured.
		"from $EDITOR",
		"global",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the output does not contain %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "configured") {
		t.Errorf("a value was marked as configured, and there is no file:\n%s", stdout)
	}
	// Nothing is holding an older copy, so there is nothing to act on and
	// nothing to say.
	if strings.Contains(stdout, "feat daemon restart") {
		t.Errorf("a restart was asked for with no daemon running:\n%s", stdout)
	}
}

// TestSettingsShowMarksWhatTheFileSets is the difference the command exists to
// draw: which of these values did I choose.
func TestSettingsShowMarksWhatTheFileSets(t *testing.T) {
	machine := prepare(t)
	file := machine.settings(t, `version: 1

resources:
  sample_interval: 10s
`)

	code, stdout, stderr := machine.run(t, "settings", "show")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
	}

	if !strings.Contains(stdout, file) {
		t.Errorf("the output does not name the file it read:\n%s", stdout)
	}
	if got := settingsRow(stdout, "sample_interval"); !strings.Contains(got, "10s") ||
		!strings.Contains(got, "(configured)") {
		t.Errorf("the configured value is not printed as configured: %q", got)
	}
	if got := settingsRow(stdout, "desktop"); !strings.Contains(got, "(default)") {
		t.Errorf("an untouched value is not marked as a default: %q", got)
	}
}

// TestSettingsInitWritesAFileThatChangesNothing is the property that makes
// running it safe.
//
// Every value in what it writes is commented out, so a machine that ran `init`
// is configured exactly as one that did not, and `feat settings show` says so of
// each value. A file full of live defaults would be a file that stops following
// Feat when Feat's own change.
func TestSettingsInitWritesAFileThatChangesNothing(t *testing.T) {
	machine := prepare(t)
	expected := filepath.Join(machine.layout.Config, "settings.yaml")

	code, stdout, stderr := machine.run(t, "settings", "init")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, expected) || !strings.Contains(stdout, "commented out") {
		t.Errorf("the output does not say what it wrote:\n%s", stdout)
	}

	written, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("reading what init wrote: %v", err)
	}
	if info, err := os.Stat(expected); err != nil {
		t.Fatalf("stat: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Errorf("the settings file is mode %o, want 0600", info.Mode().Perm())
	}

	// The one live line, and nothing else.
	for _, line := range strings.Split(string(written), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "version: 1" {
			continue
		}
		t.Errorf("the written file has a live value: %q", line)
	}

	_, shown, _ := machine.run(t, "settings", "show")
	if strings.Contains(shown, "(configured)") {
		t.Errorf("a value is marked as configured after `init`, which set nothing:\n%s", shown)
	}
}

// TestSettingsInitNeverOverwrites covers the one thing this command must not do.
//
// There is no force flag: the settings file is authored by hand, and losing it
// to a mistyped command is not a trade Feat makes — the same rule the project
// wizard follows.
func TestSettingsInitNeverOverwrites(t *testing.T) {
	machine := prepare(t)
	existing := machine.settings(t, "version: 1\n\nresources:\n  sample_interval: 42s\n")

	code, _, stderr := machine.run(t, "settings", "init")
	if code == 0 {
		t.Fatal("`settings init` overwrote an existing file")
	}
	if !strings.Contains(stderr, existing) {
		t.Errorf("the error does not name the file it refused to touch:\n%s", stderr)
	}

	body, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("reading the settings file: %v", err)
	}
	if !strings.Contains(string(body), "42s") {
		t.Errorf("the existing file was changed: %s", body)
	}
}

// TestSettingsInitRefusesBesideTheOtherExtension keeps `init` from writing the
// second file that loading would then refuse to choose between.
func TestSettingsInitRefusesBesideTheOtherExtension(t *testing.T) {
	machine := prepare(t)
	other := filepath.Join(machine.layout.Config, "settings.yml")
	if err := os.WriteFile(other, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("writing the settings file: %v", err)
	}

	code, _, stderr := machine.run(t, "settings", "init")
	if code == 0 {
		t.Fatal("`settings init` wrote a second settings file")
	}
	if !strings.Contains(stderr, other) {
		t.Errorf("the error does not name the file already there:\n%s", stderr)
	}
	if _, err := os.Stat(filepath.Join(machine.layout.Config, "settings.yaml")); !os.IsNotExist(err) {
		t.Errorf("a second settings file was written; err = %v", err)
	}
}

// editor points the machine's $EDITOR at a command that runs to completion
// without a terminal, so that `settings edit` can be driven by a test.
//
// `cp <source>` with the settings path appended is an editor that replaces the
// file, which is what makes "the editor ran" something a test can observe rather
// than assume.
func (m *machine) editor(t *testing.T, command string) {
	t.Helper()
	m.env.Getenv = func(key string) string {
		if key == "EDITOR" {
			return command
		}
		return ""
	}
}

// sourceFile writes a file for the fake editor above to copy into place.
func (m *machine) sourceFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(m.home, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// TestSettingsEditCreatesTheFileItOpens covers a machine that has never written
// one: what opens is the commented default rather than an empty buffer.
func TestSettingsEditCreatesTheFileItOpens(t *testing.T) {
	machine := prepare(t)
	machine.editor(t, "true")
	expected := filepath.Join(machine.layout.Config, "settings.yaml")

	code, stdout, stderr := machine.run(t, "settings", "edit")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
	}

	body, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("`settings edit` did not create the file it opened: %v", err)
	}
	if !strings.Contains(string(body), "version: 1") {
		t.Errorf("the created file is not the template:\n%s", body)
	}
	for _, want := range []string{expected, "commented out", "is valid"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the output does not contain %q:\n%s", want, stdout)
		}
	}
}

// TestSettingsEditOpensAFileThatDoesNotParse is the case the command most has to
// get right.
//
// A file with a typo in it is the one somebody runs this to fix, so the editor
// must open it rather than the command refusing on the way in. It cannot take
// the editor from a file it could not read either, so it falls back to $EDITOR.
func TestSettingsEditOpensAFileThatDoesNotParse(t *testing.T) {
	machine := prepare(t)
	broken := machine.settings(t, "version: 1\n\nresources:\n  sample_intervals: 10s\n")
	fixed := machine.sourceFile(t, "fixed.yaml", "version: 1\n\nresources:\n  sample_interval: 30s\n")
	machine.editor(t, "cp "+fixed)

	code, stdout, stderr := machine.run(t, "settings", "edit")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
	}

	body, err := os.ReadFile(broken)
	if err != nil {
		t.Fatalf("reading the settings file: %v", err)
	}
	if !strings.Contains(string(body), "sample_interval: 30s") {
		t.Errorf("the editor did not run on the broken file:\n%s", body)
	}
	if !strings.Contains(stdout, "is valid") {
		t.Errorf("the output does not report what the editor left:\n%s", stdout)
	}
}

// TestSettingsEditReportsWhatTheEditorLeft checks the read-back.
//
// An editor that exited cleanly says nothing about what is now in the file, and
// finding out here beats finding out from the next daemon that fails to start.
func TestSettingsEditReportsWhatTheEditorLeft(t *testing.T) {
	machine := prepare(t)
	machine.settings(t, "version: 1\n")
	broken := machine.sourceFile(t, "broken.yaml", "version: 1\n\nnotifications:\n  desktops: true\n")
	machine.editor(t, "cp "+broken)

	code, _, stderr := machine.run(t, "settings", "edit")
	if code == 0 {
		t.Fatal("`settings edit` accepted a file the editor left broken")
	}
	if !strings.Contains(stderr, "desktops") {
		t.Errorf("the error does not name what is wrong with what was saved:\n%s", stderr)
	}
}

// TestSettingsEditKeepsTheConfiguredEditorsFlags covers the same rule the
// publication draft depends on: `code -w` has to stay `code -w`, and the
// argument that would have named a repository is dropped.
func TestSettingsEditKeepsTheConfiguredEditorsFlags(t *testing.T) {
	machine := prepare(t)
	fixed := machine.sourceFile(t, "fixed.yaml", "version: 1\n\nresources:\n  sample_interval: 7s\n")
	settings := machine.settings(t, `version: 1

review:
  editor:
    command: ["cp", "`+fixed+`", "{repository_path}"]
`)
	// $EDITOR would open a terminal editor; the configured command must be what
	// runs, so this is set to something that would fail loudly if it were used.
	machine.editor(t, "false")

	code, stdout, stderr := machine.run(t, "settings", "edit")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
	}

	body, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("reading the settings file: %v", err)
	}
	if !strings.Contains(string(body), "sample_interval: 7s") {
		t.Errorf("the configured editor did not run:\n%s", body)
	}
	if !strings.Contains(stdout, "is valid") {
		t.Errorf("the output does not report what the editor left:\n%s", stdout)
	}
}

// TestSettingsShowAsksForARestartOnlyWhenOneIsNeeded is what the resolve-once
// rule costs, paid where somebody meets it.
//
// A daemon holds settings from the moment it started, so this command can print
// a file the daemon is not working from. Both states are asserted, and the
// silent one is the point of the pair: a line saying a restart is *not* needed
// is a line nobody can act on (ADR-028, ADR-079).
func TestSettingsShowAsksForARestartOnlyWhenOneIsNeeded(t *testing.T) {
	machine := prepare(t)
	machine.settings(t, "version: 1\n\nresources:\n  sample_interval: 10s\n")
	machine.serve(t)

	_, stdout, _ := machine.run(t, "settings", "show")
	if strings.Contains(stdout, "feat daemon restart") {
		t.Errorf("a restart was asked for while the daemon is working from this file:\n%s", stdout)
	}

	// And now the file is newer than the daemon, which is the state anybody who
	// edits a setting is in.
	machine.settings(t, "version: 1\n\nresources:\n  sample_interval: 20s\n")

	_, stdout, _ = machine.run(t, "settings", "show")
	if !strings.Contains(stdout, "the settings have changed since the daemon started") ||
		!strings.Contains(stdout, "feat daemon restart") {
		t.Errorf("a file written after the daemon started is not reported as pending:\n%s", stdout)
	}
}

// TestSettingsEditAsksForARestartAfterItChangedSomething is the same question a
// moment later, and the moment somebody is most likely to need the answer.
func TestSettingsEditAsksForARestartAfterItChangedSomething(t *testing.T) {
	machine := prepare(t)
	machine.settings(t, "version: 1\n")
	machine.serve(t)

	// An editor that leaves the file as it found it asks for nothing, because
	// the daemon is still working from what is there.
	machine.editor(t, "true")
	_, stdout, _ := machine.run(t, "settings", "edit")
	if strings.Contains(stdout, "feat daemon restart") {
		t.Errorf("a restart was asked for after an editor that changed nothing:\n%s", stdout)
	}

	// One that saves something does.
	changed := machine.sourceFile(t, "changed.yaml", "version: 1\n\nresources:\n  sample_interval: 30s\n")
	machine.editor(t, "cp "+changed)

	code, stdout, stderr := machine.run(t, "settings", "edit")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "the settings have changed since the daemon started") {
		t.Errorf("a saved change is not reported as pending:\n%s", stdout)
	}
}

// settingsRow returns the printed row for one field name, so that a test asserts
// about a value and its marker together rather than about column widths.
func settingsRow(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), name+" ") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// TestSettingsShowReportsABrokenFileInPlace covers the failure a hand-edited
// file actually produces, where the line it is on is most of the fix.
func TestSettingsShowReportsABrokenFileInPlace(t *testing.T) {
	machine := prepare(t)
	machine.settings(t, `version: 1

resources:
  sample_intervals: 10s
`)

	code, _, stderr := machine.run(t, "settings", "show")
	if code == 0 {
		t.Fatal("a broken settings file was accepted")
	}
	if !strings.Contains(stderr, "sample_intervals") {
		t.Errorf("the error does not name the field it rejected:\n%s", stderr)
	}
}

// TestSettingsPathPrintsThePathWhetherOrNotItExists pins what a caller can pipe.
//
// The path goes to standard output in both cases, and the fact that no file is
// there yet goes to standard error, so `$(feat settings path)` is usable before
// anybody has written one.
func TestSettingsPathPrintsThePathWhetherOrNotItExists(t *testing.T) {
	machine := prepare(t)
	expected := filepath.Join(machine.layout.Config, "settings.yaml")

	code, stdout, stderr := machine.run(t, "settings", "path")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != expected {
		t.Errorf("stdout = %q, want %q alone", stdout, expected)
	}
	if !strings.Contains(stderr, "no settings file") {
		t.Errorf("stderr does not say the file is missing:\n%s", stderr)
	}

	machine.settings(t, "version: 1\n")

	code, stdout, stderr = machine.run(t, "settings", "path")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != expected {
		t.Errorf("stdout = %q, want %q", stdout, expected)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, and the file is there", stderr)
	}
}

// TestSettingsPathFindsTheOtherExtension covers a user who writes ".yml": the
// command has to print the file that exists rather than the one Feat would have
// created.
func TestSettingsPathFindsTheOtherExtension(t *testing.T) {
	machine := prepare(t)
	file := filepath.Join(machine.layout.Config, "settings.yml")
	if err := os.WriteFile(file, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("writing the settings file: %v", err)
	}

	code, stdout, stderr := machine.run(t, "settings", "path")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != file {
		t.Errorf("stdout = %q, want %q", stdout, file)
	}
}
