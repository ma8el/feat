package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/config"
)

// writeSettings puts a settings file in a temporary configuration directory and
// returns the directory.
func writeSettings(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return dir
}

// settingsFixture reads the worked settings file used by several tests here.
func settingsFixture(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "settings.yaml"))
	if err != nil {
		t.Fatalf("reading the settings fixture: %v", err)
	}
	return string(body)
}

// TestSettingsAreDefaultedWhenNoFileExists covers the case every machine is in
// until somebody writes a file.
//
// Absence is not a failure here, unlike a project Feat was asked about and
// cannot find: every value has a documented default, so the file exists to
// change one rather than to supply one.
func TestSettingsAreDefaultedWhenNoFileExists(t *testing.T) {
	opts, _ := testOptions(t, map[string]string{"EDITOR": "hx"})

	settings, err := config.LoadSettings(t.TempDir(), opts)
	if err != nil {
		t.Fatalf("a machine with no settings file must still have settings: %v", err)
	}

	if settings.Path() != "" {
		t.Errorf("Path = %q, and no file was read", settings.Path())
	}
	if got, want := settings.Resources.Sample(), 2*time.Second; got != want {
		t.Errorf("resources.sample_interval = %v, want %v", got, want)
	}
	if got, want := settings.Notifications.IdleGrace(), 5*time.Second; got != want {
		t.Errorf("notifications.idle_grace_period = %v, want %v", got, want)
	}
	if !settings.Notifications.DesktopEnabled() || !settings.Notifications.SuppressedWhileAttached() {
		t.Error("the notification defaults must be on: unset means true")
	}
	if got := strings.Join(settings.Review.Diff.Command, " "); got != "git diff {base_commit}" {
		t.Errorf("review.diff.command = %q", got)
	}
	if got := strings.Join(settings.Review.Editor.Command, " "); got != "hx {repository_path}" {
		t.Errorf("review.editor.command = %q, and $EDITOR is what it falls back to", got)
	}
}

// TestSettingsAreReadFromTheFile checks that a written value reaches the
// resolved settings.
func TestSettingsAreReadFromTheFile(t *testing.T) {
	dir := writeSettings(t, "settings.yaml", `version: 1

notifications:
  desktop: false

resources:
  sample_interval: 10s
`)
	opts, _ := testOptions(t, nil)

	settings, err := config.LoadSettings(dir, opts)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}

	if settings.Path() != filepath.Join(dir, "settings.yaml") {
		t.Errorf("Path = %q, want the file that was read", settings.Path())
	}
	if settings.Notifications.DesktopEnabled() {
		t.Error("notifications.desktop is false in the file and true in the resolved settings")
	}
	if got, want := settings.Resources.Sample(), 10*time.Second; got != want {
		t.Errorf("resources.sample_interval = %v, want %v", got, want)
	}
	// Everything the file did not mention still has its default, so a file that
	// changes one value does not silently unset the rest.
	if got, want := settings.Notifications.IdleGrace(), 5*time.Second; got != want {
		t.Errorf("notifications.idle_grace_period = %v, want the default %v", got, want)
	}
}

// TestSettingsAcceptTheOtherExtension covers a user who writes ".yml"
// everywhere, since the project files beside this one accept both.
func TestSettingsAcceptTheOtherExtension(t *testing.T) {
	dir := writeSettings(t, "settings.yml", "version: 1\n")
	opts, _ := testOptions(t, nil)

	settings, err := config.LoadSettings(dir, opts)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.Path() != filepath.Join(dir, "settings.yml") {
		t.Errorf("Path = %q, want the .yml file", settings.Path())
	}
}

// TestSettingsWrittenTwiceAreRefused pins the same rule a project configured by
// two files gets: which one Feat read would depend on a rule the user has no
// reason to know, and the one they edited might be the other one.
func TestSettingsWrittenTwiceAreRefused(t *testing.T) {
	dir := writeSettings(t, "settings.yaml", "version: 1\n")
	if err := os.WriteFile(filepath.Join(dir, "settings.yml"), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("writing the second file: %v", err)
	}
	opts, _ := testOptions(t, nil)

	_, err := config.LoadSettings(dir, opts)
	if err == nil {
		t.Fatal("two settings files were accepted")
	}
	for _, name := range []string{"settings.yaml", "settings.yml"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error does not name %s: %v", name, err)
		}
	}
}

// TestSettingsRejectAnUnknownField covers the strictness a hand-edited file
// depends on: a value silently ignored is a user believing they configured
// something they did not.
func TestSettingsRejectAnUnknownField(t *testing.T) {
	dir := writeSettings(t, "settings.yaml", `version: 1

resources:
  sample_intervals: 10s
`)
	opts, _ := testOptions(t, nil)

	_, err := config.LoadSettings(dir, opts)
	if err == nil {
		t.Fatal("an unknown field was accepted")
	}
	if !strings.Contains(err.Error(), "sample_intervals") {
		t.Errorf("the error does not name the field it rejected: %v", err)
	}
}

// TestSettingsRejectAnotherSchemaVersion keeps the version a version rather
// than decoration, and keeps it separate from the project file's.
func TestSettingsRejectAnotherSchemaVersion(t *testing.T) {
	dir := writeSettings(t, "settings.yaml", "version: 2\n")
	opts, _ := testOptions(t, nil)

	_, err := config.LoadSettings(dir, opts)
	invalid := configError(t, err)
	if got := problemAt(t, invalid, "version").Reason; !strings.Contains(got, "settings schema version") {
		t.Errorf("version problem = %q, and it should say which schema it is about", got)
	}
}

// TestSettingsReportABadDurationAgainstItsOwnField covers why the durations are
// held as strings: the YAML decoder cannot name the field a custom scalar type
// failed in, and the user has to be told which line to fix.
func TestSettingsReportABadDurationAgainstItsOwnField(t *testing.T) {
	dir := writeSettings(t, "settings.yaml", `version: 1

resources:
  sample_interval: two seconds
`)
	opts, _ := testOptions(t, nil)

	_, err := config.LoadSettings(dir, opts)
	invalid := configError(t, err)
	if got := problemAt(t, invalid, "resources.sample_interval").Reason; !strings.Contains(got, "duration") {
		t.Errorf("problem = %q, want one about a duration", got)
	}
}

// TestSettingsRejectANegativeInterval covers the other half of the same rule: a
// duration that parses is not yet a duration that means anything.
func TestSettingsRejectANegativeInterval(t *testing.T) {
	dir := writeSettings(t, "settings.yaml", `version: 1

notifications:
  idle_grace_period: -5s
`)
	opts, _ := testOptions(t, nil)

	_, err := config.LoadSettings(dir, opts)
	invalid := configError(t, err)
	if got := problemAt(t, invalid, "notifications.idle_grace_period").Reason; !strings.Contains(got, "negative") {
		t.Errorf("problem = %q, want one about a negative duration", got)
	}
}

// TestSettingsValidateTheReviewCommands checks that the rule about a templated
// program follows the section to its new file: the program to run is fixed by
// configuration, and only arguments may contain placeholders.
func TestSettingsValidateTheReviewCommands(t *testing.T) {
	dir := writeSettings(t, "settings.yaml", `version: 1

review:
  editor:
    command: ["{repository_path}"]
`)
	opts, _ := testOptions(t, nil)

	_, err := config.LoadSettings(dir, opts)
	invalid := configError(t, err)
	if got := problemAt(t, invalid, "review.editor.command").Reason; !strings.Contains(got, "fixed by configuration") {
		t.Errorf("problem = %q, want the rule about a templated program", got)
	}
}

// TestSettingsMarkEveryValueAsDefaultOrConfigured is what `feat settings show`
// rests on.
//
// The file is optional and mostly absent, so a printed value a user cannot tell
// from a default is one they cannot tell they set. The editor carries a third
// origin, which is the tell that made this a settings section: nothing
// configured it, and it came from the user's own environment.
func TestSettingsMarkEveryValueAsDefaultOrConfigured(t *testing.T) {
	dir := writeSettings(t, "settings.yaml", `version: 1

resources:
  sample_interval: 10s
`)
	opts, _ := testOptions(t, map[string]string{"EDITOR": "hx"})

	settings, err := config.LoadSettings(dir, opts)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}

	notes := make(map[string]string)
	for _, section := range settings.Describe() {
		for _, field := range section.Fields {
			notes[section.Title+"."+field.Name] = field.Note
		}
	}

	for name, want := range map[string]string{
		"resources.sample_interval":             "configured",
		"notifications.desktop":                 "default",
		"notifications.idle_grace_period":       "default",
		"notifications.suppress_while_attached": "default",
		"review.diff.command":                   "default",
		"review.status.command":                 "default",
		"review.editor.command":                 "from $EDITOR",
	} {
		if got := notes[name]; got != want {
			t.Errorf("%s is marked %q, want %q", name, got, want)
		}
	}
}

// TestSettingsSayWhyTheEditorIsMissing covers the one value that can end up
// unset: "(none)" alone reads like a value Feat lost rather than one nobody has
// supplied yet.
func TestSettingsSayWhyTheEditorIsMissing(t *testing.T) {
	opts, _ := testOptions(t, nil)

	settings, err := config.DefaultSettings(opts)
	if err != nil {
		t.Fatalf("DefaultSettings: %v", err)
	}

	for _, section := range settings.Describe() {
		for _, field := range section.Fields {
			if section.Title != "review" || field.Name != "editor.command" {
				continue
			}
			if field.Value != "(none)" {
				t.Errorf("editor.command = %q, and $EDITOR is unset in this environment", field.Value)
			}
			if !strings.Contains(field.Note, "$EDITOR is not set") {
				t.Errorf("editor.command note = %q, and it should say what to do about it", field.Note)
			}
			return
		}
	}
	t.Error("the description has no review editor row")
}

// TestSettingsFixtureLoads keeps the worked example honest, the way the project
// examples are kept honest against the loader that reads them.
func TestSettingsFixtureLoads(t *testing.T) {
	dir := writeSettings(t, "settings.yaml", settingsFixture(t))
	opts, _ := testOptions(t, nil)

	settings, err := config.LoadSettings(dir, opts)
	if err != nil {
		t.Fatalf("the settings fixture does not load: %v", err)
	}
	if got, want := settings.Resources.Sample(), 2*time.Second; got != want {
		t.Errorf("resources.sample_interval = %v, want %v", got, want)
	}
}

// TestAProjectFileNoLongerCarriesTheMovedSections is the break ADR-079 chose to
// take rather than write a migration for.
//
// Strict decoding is what makes that choice safe: a project file still carrying
// a section that has moved fails to load, naming the field, rather than being
// silently ignored — which is the failure a migration would have existed to
// prevent. The message is generic rather than naming the new home, which is
// enough at one user.
func TestAProjectFileNoLongerCarriesTheMovedSections(t *testing.T) {
	dir := write(t, "app.yaml", fixture(t, "app.yaml")+`
resources:
  sample_interval: 2s
`)
	opts, _ := testOptions(t, nil)

	_, err := config.Load(dir, "app", opts)
	if err == nil {
		t.Fatal("a project file carrying resources: was accepted")
	}
	if !strings.Contains(err.Error(), "resources") {
		t.Errorf("the error does not name the section it rejected: %v", err)
	}
}

// TestSettingsFilePathIsBesideTheProjectsDirectory pins where the file belongs,
// which is what `feat settings path` prints and what a user is told to write.
func TestSettingsFilePathIsBesideTheProjectsDirectory(t *testing.T) {
	if got, want := config.SettingsFile("/c/feat"), "/c/feat/settings.yaml"; got != want {
		t.Errorf("SettingsFile = %q, want %q", got, want)
	}

	// And it is found nowhere when it is not there, which is a state rather than
	// an error: the command prints where one would go.
	found, err := config.FindSettings(t.TempDir())
	if err != nil {
		t.Fatalf("FindSettings: %v", err)
	}
	if found != "" {
		t.Errorf("FindSettings = %q, want no file", found)
	}
}
