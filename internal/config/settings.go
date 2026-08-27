package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/goccy/go-yaml"
)

// SettingsSchemaVersion is the settings schema this build understands.
//
// It is a second number rather than the project file's, because the two files
// are two compatibility surfaces. A project file and a settings file describe
// different things and change for different reasons, and one number would make
// every change to either a version bump for both.
const SettingsSchemaVersion = 1

// settingsFileName is the settings file's stem. Its extension is one of the
// two a project file may carry, so that a user who writes ".yml" everywhere is
// not told they must write ".yaml" here.
const settingsFileName = "settings"

// Where a resolved settings value came from.
//
// `feat settings show` prints one beside every value. The file is optional and
// on most machines absent, so without this a user cannot tell a value they set
// from one Feat chose for them — which is the question the command exists to
// answer.
const (
	originFile    = "configured"
	originDefault = "default"
	// originEditor is the tell that made review a settings section rather than a
	// project one: the editor default already reaches for a user-level source,
	// and until now there was no user-level file for it to reach for.
	originEditor = "from $EDITOR"
)

// Settings is what Feat is told once for this machine and this user, rather
// than once per project.
//
// Three sections are here rather than in project configuration because none of
// them is a fact about a project. Sampling the machine's resources produces one
// machine-wide number, so a per-project interval had to be reconciled by a rule
// ("the most eager project wins") that existed only because the setting was
// misplaced; notifications are about the person at the keyboard and the desktop
// they are using; and the review commands are that person's tools, which the
// editor default has always said out loud by falling back to $EDITOR (ADR-079).
//
// There is deliberately no per-project override. Precedence rules are
// load-bearing and hard to remove once written, and nothing has yet asked for
// one — an override can be added later, on evidence that somebody wants it.
//
// The file is optional. Every value has a documented default, so the file
// exists to change one, and a machine that has written none still has settings.
type Settings struct {
	// Version is the settings schema version. It must be SettingsSchemaVersion.
	Version int `yaml:"version"`
	// Review configures the external commands review opens.
	Review ReviewSection `yaml:"review"`
	// Notifications configures attention notifications.
	Notifications NotificationsSection `yaml:"notifications"`
	// Resources configures resource sampling.
	Resources ResourcesSection `yaml:"resources"`

	// path is the file the settings were read from. It is empty for settings
	// that are entirely defaults, which is what a machine with no file has.
	path string
	// source is the file's bytes, kept so that a validation problem can be shown
	// in place.
	source []byte
	// resolved records that Resolve has run, so that a caller cannot validate or
	// use unfilled defaults by mistake.
	resolved bool
	// origins records where each described value came from, keyed by the dotted
	// path Describe prints. It is filled by Resolve, which is the only moment
	// the difference between "the file said so" and "Feat chose it" is still
	// visible.
	origins map[string]string
}

// SettingsFile returns the settings file path.
//
// It is beside the projects directory rather than inside it, because it is not
// one project's answer to anything: a file in projects/ is named after the
// project it describes, and this one would have no such name.
func SettingsFile(dir string) string {
	return filepath.Join(dir, settingsFileName+extensions[0])
}

// FindSettings returns the settings file, or an empty path when there is none.
//
// Both accepted extensions are looked for, and finding two is an error rather
// than a preference, for the reason a project configured twice is one: which of
// them Feat used would otherwise depend on a rule the user has no reason to
// know, and the one they edited might be the other one.
func FindSettings(dir string) (string, error) {
	var found []string
	for _, extension := range extensions {
		candidate := filepath.Join(dir, settingsFileName+extension)
		if _, err := os.Stat(candidate); err == nil {
			found = append(found, candidate)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("looking for the settings file %s: %w", candidate, err)
		}
	}

	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf(
			"settings are written twice, by %s and %s: keep one of them", found[0], found[1])
	}
}

// LoadSettings reads, resolves, and validates the global settings.
//
// A missing file is not an error, which is the one place this differs from
// loading a project. A project Feat was asked about and cannot find is a
// question with no answer; settings Feat was never told are settings it has,
// because every value here has a default and the file exists to change one.
func LoadSettings(dir string, opts Options) (*Settings, error) {
	file, err := FindSettings(dir)
	if err != nil {
		return nil, err
	}
	if file == "" {
		return DefaultSettings(opts)
	}
	return LoadSettingsFile(file, opts)
}

// LoadSettingsFile reads, resolves, and validates one settings file.
func LoadSettingsFile(file string, opts Options) (*Settings, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s", ErrNotFound, file)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}

	settings, err := ParseSettings(file, data)
	if err != nil {
		return nil, err
	}
	if err := settings.Resolve(opts); err != nil {
		return nil, err
	}
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	return settings, nil
}

// DefaultSettings returns the settings of a machine that has written no file.
//
// Every value is a default and Describe says so of each one, so that the output
// of `feat settings show` is the same shape whether or not a file exists.
func DefaultSettings(opts Options) (*Settings, error) {
	settings := &Settings{Version: SettingsSchemaVersion}
	if err := settings.Resolve(opts); err != nil {
		return nil, err
	}
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	return settings, nil
}

// ParseSettings decodes one settings document.
//
// Decoding is strict in both directions that matter to a hand-edited file, for
// the reasons Parse gives: a field Feat does not know is an error rather than a
// value silently ignored, and a key given twice is an error rather than a value
// silently discarded.
func ParseSettings(file string, data []byte) (*Settings, error) {
	settings := &Settings{path: file, source: data}

	if err := yaml.UnmarshalWithOptions(data, settings, yaml.Strict()); err != nil {
		return nil, decodingError(file, data, err)
	}
	return settings, nil
}

// Path returns the file the settings were read from. It is empty when there is
// no file, which is not a failure: it is a machine running on the defaults.
func (s *Settings) Path() string { return s.path }

// Resolve fills the defaults and records which values it had to fill.
//
// It touches no file. Everything it needs beyond the document is in Options:
// the environment $EDITOR is read from, and nothing else.
func (s *Settings) Resolve(opts Options) error {
	if s.resolved {
		return nil
	}
	s.origins = make(map[string]string, 7)

	s.resolveReview(opts)
	if err := s.resolveIntervals(); err != nil {
		return err
	}

	s.resolved = true
	return nil
}

// resolveReview fills the review commands and records where each came from.
//
// The editor has three origins rather than two, and the third is the reason
// this section is here at all: an unset command falls back to $EDITOR, which is
// the user's own tool named by the user's own environment.
func (s *Settings) resolveReview(opts Options) {
	s.mark("review.diff.command", !s.Review.Diff.Empty())
	s.mark("review.status.command", !s.Review.Status.Empty())

	configured := !s.Review.Editor.Empty()
	resolveReviewSection(opts, &s.Review)

	switch {
	case configured:
		s.origins["review.editor.command"] = originFile
	case !s.Review.Editor.Empty():
		s.origins["review.editor.command"] = originEditor
	default:
		// Neither configured nor inherited. The note says which of the two to do
		// something about, because "(none)" alone reads like a value Feat lost.
		s.origins["review.editor.command"] = editorNote(s.Review.Editor)
	}
}

// resolveIntervals fills and parses the durations, which are held as strings so
// that a malformed one is reported against its own field rather than by the
// YAML decoder, which cannot name the field a custom scalar type failed in.
func (s *Settings) resolveIntervals() error {
	s.mark("notifications.desktop", s.Notifications.Desktop != nil)
	s.mark("notifications.suppress_while_attached", s.Notifications.SuppressWhileAttached != nil)

	s.mark("notifications.idle_grace_period", s.Notifications.IdleGracePeriod != "")
	if s.Notifications.IdleGracePeriod == "" {
		s.Notifications.IdleGracePeriod = defaultIdleGracePeriod
	}
	s.mark("resources.sample_interval", s.Resources.SampleInterval != "")
	if s.Resources.SampleInterval == "" {
		s.Resources.SampleInterval = defaultSampleInterval
	}

	for _, field := range []struct {
		path   string
		value  string
		target *time.Duration
	}{
		{"notifications.idle_grace_period", s.Notifications.IdleGracePeriod, &s.Notifications.idleGracePeriod},
		{"resources.sample_interval", s.Resources.SampleInterval, &s.Resources.sampleInterval},
	} {
		parsed, err := parseInterval(field.path, field.value)
		if err != nil {
			return asProblem(s.path, s.source, err)
		}
		*field.target = parsed
	}
	return nil
}

// mark records whether a value came from the file or from a default.
func (s *Settings) mark(path string, configured bool) {
	if configured {
		s.origins[path] = originFile
		return
	}
	s.origins[path] = originDefault
}

// Validate reports every rule the resolved settings break.
//
// It checks shape only, exactly as Validate does for a project: whether the
// configured editor is installed on this machine is a host question, and it
// belongs to `feat doctor`.
func (s *Settings) Validate() error {
	if !s.resolved {
		return fmt.Errorf("the settings must be resolved before they are validated")
	}

	found := &problems{}
	if s.Version != SettingsSchemaVersion {
		found.add("version", fmt.Sprintf(
			"must be %d, but is %d: this build understands settings schema version %d only",
			SettingsSchemaVersion, s.Version, SettingsSchemaVersion))
	}
	validateReviewSection(found, s.Review)

	return found.err(s.path, s.source)
}

// Describe renders the resolved settings.
//
// It is what `feat settings show` prints: the values Feat will act on with the
// defaults filled in, each marked with where it came from. A default a user
// cannot see is a default they cannot check, and a value they cannot tell from
// a default is one they cannot tell they set.
func (s *Settings) Describe() []Section {
	return []Section{s.describeReview(), s.describeNotifications(), s.describeResources()}
}

func (s *Settings) describeReview() Section {
	return Section{Title: "review", Fields: []Field{
		{Name: "diff.command", Value: renderCommand(s.Review.Diff),
			Note: s.origins["review.diff.command"]},
		{Name: "editor.command", Value: renderCommand(s.Review.Editor),
			Note: s.origins["review.editor.command"]},
		{Name: "status.command", Value: renderCommand(s.Review.Status),
			Note: s.origins["review.status.command"]},
	}}
}

func (s *Settings) describeNotifications() Section {
	return Section{Title: "notifications", Fields: []Field{
		{Name: "desktop", Value: yesNo(s.Notifications.DesktopEnabled()),
			Note: s.origins["notifications.desktop"]},
		{Name: "idle_grace_period", Value: s.Notifications.IdleGracePeriod,
			Note: s.origins["notifications.idle_grace_period"]},
		{Name: "suppress_while_attached", Value: yesNo(s.Notifications.SuppressedWhileAttached()),
			Note: s.origins["notifications.suppress_while_attached"]},
	}}
}

func (s *Settings) describeResources() Section {
	return Section{Title: "resources", Fields: []Field{
		{Name: "sample_interval", Value: s.Resources.SampleInterval,
			Note: s.origins["resources.sample_interval"]},
	}}
}
