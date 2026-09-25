package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

// SettingsSchemaVersion is the settings schema this build understands. It is a second
// number rather than the project file's, because the two files describe different
// things and change for different reasons, and one number would make every change to
// either a version bump for both.
const SettingsSchemaVersion = 1

// settingsFileName is the settings file's stem. Its extension is one of the two a
// project file may carry, so a user who writes ".yml" everywhere is not told they
// must write ".yaml" here.
const settingsFileName = "settings"

// Where a resolved settings value came from. `feat settings show` prints one beside
// every value, because the file is optional and a user otherwise cannot tell a value
// they set from one Feat chose for them.
const (
	originFile    = "configured"
	originDefault = "default"
	// originEditor is why review is a settings section rather than a project one: the
	// editor default already reaches for a user-level source, and until this file
	// there was none for it to reach for.
	originEditor = "from $EDITOR"
)

// Settings is what Feat is told once for this machine and this user, rather than once
// per project.
//
// Three sections are here rather than in project configuration because none of them
// is a fact about a project. Sampling the machine's resources produces one
// machine-wide number, so a per-project interval needed a reconciliation rule that
// existed only because the setting was misplaced; notifications are about the person
// at the keyboard and the desktop they are using; and the review commands are that
// person's tools, which the editor default reaches for by falling back to $EDITOR
// (ADR-079).
//
// There is deliberately no per-project override. Precedence rules are load-bearing
// and hard to remove once written, and nothing has yet asked for one.
//
// The file is optional. Every value has a documented default, so the file exists to
// change one, and a machine that has written none still has settings.
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
	// origins records where each described value came from, keyed by the dotted path
	// Describe prints. Resolve fills it, because that is the last moment the file's
	// own values and Feat's are still distinguishable.
	origins map[string]string
}

// SettingsFile returns the settings file path. It sits beside the projects directory
// rather than inside it, because a file in projects/ is named after the project it
// describes and this one describes none.
func SettingsFile(dir string) string {
	return filepath.Join(dir, settingsFileName+extensions[0])
}

// FindSettings returns the settings file, or an empty path when there is none. Both
// accepted extensions are looked for, and finding two is an error for the reason a
// project configured twice is: which one Feat used would depend on a rule the user
// has no reason to know.
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

// LoadSettings reads, resolves, and validates the global settings. A missing file is
// not an error, which is the one place this differs from loading a project: every
// value here has a default, so a machine that wrote no file still has settings.
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

// DefaultSettings returns the settings of a machine that has written no file. Every
// value is a default and Describe says so of each one, so `feat settings show` prints
// the same shape whether or not a file exists.
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

// ParseSettings decodes one settings document. Decoding is strict in both directions
// that matter to a hand-edited file, for the reasons Parse gives: a field Feat does
// not know is an error rather than a value silently ignored, and so is a key given
// twice.
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

// SettingsTemplate is the file `feat settings init` writes, and the documented
// example in docs/examples/settings.yaml. A test holds the two together.
//
// Every value is commented out and only the version is live, as in `feat project
// init`: a default written down stops following Feat when Feat's own changes, and
// this whole file is defaults. It still shows each value where it goes rather than
// leaving the user to find it in a schema, and `feat settings show` reports every one
// as a default until a line is uncommented (ADR-062, ADR-079).
const SettingsTemplate = `# Feat's settings for this machine and this user.
#
# Everything here is global: it applies to every project, and there is no
# per-project override. What belongs to one project — its repositories, its
# agent, its services, its checks — is in
# ~/.config/feat/projects/<project-id>.yaml instead.
#
# This file is optional and every value below has a default, so a machine that
# has written none is fully configured. That is why they are all commented out:
# a default written down is a value that stops following Feat when Feat's own
# changes. Uncomment what you want to change, and leave the rest to Feat.
#
#   feat settings show    prints what Feat will act on, with the defaults filled
#                         in and each value marked as yours or as a default
#   feat settings path    prints where this file belongs
#   feat settings edit    opens it
#
# A running daemon reads this once, when it starts. Restart it after changing
# something here.
#
# Editors that understand JSON Schema can be pointed at
# schema/feat-settings.schema.json in the Feat repository.

version: 1

# The external commands review opens. Feat renders no diffs itself.
#
# They are settings rather than project configuration because they are your own
# tools: one person opens a diff the same way whichever repository it is in.
# review.editor has always said so by falling back to $EDITOR.
#
# The first element is the program and is never templated. Arguments may use
# {repository_path}, {base_commit}, {branch}, {project_id}, {repository_id},
# {task_id}, and {task_key}, and each expands into one argument.
#review:
#  diff:
#    command: ["git", "diff", "{base_commit}"]
#  # Defaults to $EDITOR with {repository_path}.
#  editor:
#    command: ["nvim", "{repository_path}"]
#  status:
#    command: ["git", "status", "--short", "--branch"]

# Being interrupted, which is about you and the machine you are at rather than
# about any project.
#notifications:
#  # Desktop notifications for the things worth interrupting you: an agent that
#  # has gone quiet, a review request, a failed check, a failed session, and
#  # failed application services. macOS only in this version; Linux arrives with
#  # v0.2. The dashboard's attention badges work either way.
#  desktop: true
#  # How long a task must have *been* idle before it is worth telling you,
#  # measured from the moment it became idle. That moment is itself
#  # agent.claude.idle_grace_period after the agent's turn ended — which stays in
#  # project configuration, because when an ended turn counts as idle is a fact
#  # about how the agent is driven. Raising this one alone means "only tell me
#  # about a long silence".
#  idle_grace_period: 5s
#  # Drop a notification about a task you are already watching. Feat asks tmux
#  # which window your client is on rather than remembering that you attached.
#  suppress_while_attached: true

# Watching what the machine is doing.
#resources:
#  # How often the machine, and each task's containers and processes, are
#  # observed. One sample measures the whole machine, whatever project asked for
#  # it, which is why this is a setting rather than a project's to decide.
#  # Sampling is observational: it never refuses a task and never slows a
#  # request. Asking the container runtime what it is using takes a second or
#  # two, so a shorter interval than that is treated as "as often as possible".
#  sample_interval: 2s
`

// DocumentEditor returns the editor command for opening one document that is not a
// repository, with the argument that would have named one left off.
//
// The configured command names an editor, its flags, and the thing it opens. A
// publication draft and this settings file are the third of those without being the
// first two's subject, so the flags are kept: `nvim --clean` has to stay `nvim
// --clean`, or the editor behaves differently from everywhere else. A placeholder
// argument is dropped rather than expanded, because every placeholder in this command
// is about a repository.
//
// It returns nothing when no editor is configured, which is the documented case: the
// editor falls back to $EDITOR, and the client's process can see one where the
// daemon's cannot (FR-REV-003).
func (r ReviewSection) DocumentEditor() []string {
	if r.Editor.Empty() {
		return nil
	}
	command := r.Editor.Command

	vector := make([]string, 0, len(command))
	vector = append(vector, command[0])
	for _, argument := range command[1:] {
		if strings.Contains(argument, "{") {
			continue
		}
		vector = append(vector, argument)
	}
	return vector
}

// Resolve fills the defaults and records which values it had to fill. It touches no
// file: everything it needs beyond the document is in Options, which is the
// environment $EDITOR is read from and nothing else.
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

// resolveReview fills the review commands and records where each came from. The
// editor has three origins rather than two, because an unset command falls back to
// $EDITOR, which is the user's own tool named by their own environment.
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

// resolveIntervals fills and parses the durations, which are held as strings so a
// malformed one is reported against its own field rather than by the YAML decoder,
// which cannot name the field a custom scalar type failed in.
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

// Validate reports every rule the resolved settings break. It checks shape only,
// exactly as Validate does for a project: whether the configured editor is installed
// on this machine is a host question, and it belongs to `feat doctor`.
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

// Describe renders the resolved settings. It is what `feat settings show` prints: the
// values Feat will act on with the defaults filled in, each marked with where it came
// from, so a user can tell a value they set from one Feat chose.
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
