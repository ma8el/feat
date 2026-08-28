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
		// The restart requirement is stated where somebody meets it, rather than
		// left to be found by editing a value and watching nothing happen.
		"restart it after changing one",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the output does not contain %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "configured") {
		t.Errorf("a value was marked as configured, and there is no file:\n%s", stdout)
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
