package project

import (
	"context"
	"errors"
	"fmt"

	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/tracker"
)

// Severity classifies one diagnostic finding.
type Severity string

// Severities, in increasing order of how much they demand.
const (
	// SeverityOK reports a check that passed.
	SeverityOK Severity = "ok"
	// SeveritySkipped reports a check this build cannot run. It is not a pass:
	// a diagnostic that claims a check it did not run is worse than no
	// diagnostic at all.
	SeveritySkipped Severity = "skipped"
	// SeverityWarning reports something that will not stop Feat but is
	// probably not what the user meant.
	SeverityWarning Severity = "warning"
	// SeverityError reports something that stops the project from working.
	SeverityError Severity = "error"
)

// Finding is one diagnostic result.
type Finding struct {
	// Check names what was checked, in the same dotted form configuration uses
	// where there is a corresponding field.
	Check string
	// Severity is how much the finding demands.
	Severity Severity
	// Summary says what was found.
	Summary string
	// Action says what to do about it. It is empty when there is nothing to
	// do, and required for anything worse than a warning: a diagnostic that
	// reports a problem without an action leaves the user where it found them.
	Action string
}

// Report is the result of one `feat doctor` run.
type Report struct {
	// Host holds the findings about this machine, which no project changes.
	Host []Finding
	// Projects holds the findings for each configured project, in order.
	Projects []Diagnosis
}

// Diagnosis is what `feat doctor` found out about one configured project.
type Diagnosis struct {
	// ID is the project identifier, taken from the file name.
	ID string
	// File is the configuration file.
	File string
	// Config is the resolved configuration, or nil when it could not be
	// loaded, in which case Findings says why.
	Config *config.Config
	// Mounts is the repository-to-container path mapping, empty when the
	// configuration could not be loaded.
	Mounts []config.Mount
	// Findings are the results for this project.
	Findings []Finding
}

// Options configure a diagnostic run.
type Options struct {
	// ConfigDir is the directory holding project configuration.
	ConfigDir string
	// SettingsDir is the directory holding the machine's settings file, which is
	// the parent of ConfigDir in a real layout. It is separate rather than
	// derived, so that a test can point the two somewhere unrelated and so that
	// this package never reconstructs a path internal/paths owns.
	SettingsDir string
	// Resolve supplies the environment configuration is resolved against.
	Resolve config.Options
	// Runner runs host commands. A nil value uses the real host.
	Runner Runner
	// Tracker runs a project's configured ticket command. A nil value runs it
	// on this host; a test supplies its own, because whether a project is
	// configured should not depend on the tester holding an account with
	// somebody's tracker.
	Tracker tracker.Runner
	// Projects limits the run to these project identifiers. Empty means every
	// configured project.
	Projects []string
	// Registered reports whether a project is registered with the daemon. A
	// nil function leaves registration unreported, which is what a run with no
	// daemon does.
	Registered func(id string) bool
}

// Failed reports whether any finding is an error.
//
// It is what decides the exit code: warnings are things to look at, and errors
// are things that stop the project from working.
func (r Report) Failed() bool {
	for _, finding := range r.Host {
		if finding.Severity == SeverityError {
			return true
		}
	}
	for _, project := range r.Projects {
		for _, finding := range project.Findings {
			if finding.Severity == SeverityError {
				return true
			}
		}
	}
	return false
}

// Counts returns how many findings there are of each severity.
func (r Report) Counts() map[Severity]int {
	counts := make(map[Severity]int, 4)
	count := func(findings []Finding) {
		for _, finding := range findings {
			counts[finding.Severity]++
		}
	}
	count(r.Host)
	for _, project := range r.Projects {
		count(project.Findings)
	}
	return counts
}

// Diagnose checks the host and every configured project.
//
// It never registers anything, never changes a file, and works before a daemon
// or a registration exists, because docs/02-user-workflows.md §1 puts it before
// both: the user writes their configuration, runs `feat doctor`, and registers
// the project once the diagnosis is clean.
func Diagnose(ctx context.Context, opts Options) (Report, error) {
	if opts.Runner == nil {
		opts.Runner = HostRunner{}
	}

	ids := opts.Projects
	if len(ids) == 0 {
		configured, err := config.List(opts.ConfigDir)
		if err != nil {
			return Report{}, err
		}
		ids = configured
	}

	report := Report{}
	for _, id := range ids {
		report.Projects = append(report.Projects, diagnoseProject(ctx, opts, id))
	}
	// The host checks come last so that they can say whether a missing tool
	// matters, which depends on what the configured projects ask for.
	report.Host = diagnoseHost(ctx, opts, report.Projects)
	return report, nil
}

// diagnoseProject loads and checks one project.
func diagnoseProject(ctx context.Context, opts Options, id string) Diagnosis {
	report := Diagnosis{ID: id}

	file, err := config.Find(opts.ConfigDir, id)
	report.File = file
	if err != nil {
		severity, action := SeverityError, "write a configuration file for this project"
		if !errors.Is(err, config.ErrNotFound) {
			action = "keep one configuration file for this project"
		}
		report.Findings = append(report.Findings, Finding{
			Check:    "configuration",
			Severity: severity,
			Summary:  err.Error(),
			Action:   action,
		})
		return report
	}

	cfg, err := config.LoadFile(file, opts.Resolve)
	if err != nil {
		report.Findings = append(report.Findings, Finding{
			Check:    "configuration",
			Severity: SeverityError,
			Summary:  configSummary(err),
			Action:   "fix " + file,
		})
		return report
	}

	report.Config = cfg
	report.Mounts = cfg.Mounts()
	report.Findings = append(report.Findings, Finding{
		Check:    "configuration",
		Severity: SeverityOK,
		Summary:  fmt.Sprintf("%s is valid", file),
	})
	if opts.Registered != nil {
		report.Findings = append(report.Findings, registrationFinding(id, opts.Registered(id)))
	}

	checks := &checker{
		runner:  opts.Runner,
		tracker: opts.Tracker,
		config:  cfg,
		home:    opts.Resolve.Env.Home,
	}
	report.Findings = append(report.Findings, checks.run(ctx)...)
	return report
}

// registrationFinding reports whether the daemon knows about the project.
func registrationFinding(id string, registered bool) Finding {
	if registered {
		return Finding{Check: "registration", Severity: SeverityOK, Summary: "registered with the daemon"}
	}
	// Not an error: a configuration that is valid but not yet registered is
	// exactly the state the user is in when they run `feat doctor` for the
	// first time.
	return Finding{
		Check:    "registration",
		Severity: SeverityWarning,
		Summary:  "not registered with the daemon",
		Action:   "register it with `feat project add " + id + "`",
	}
}

// configSummary renders a configuration error for a diagnostic.
//
// The annotated form is used where there is one: the location of a mistake in a
// nested YAML document is most of the work of fixing it.
func configSummary(err error) string {
	var invalid *config.Error
	if errors.As(err, &invalid) {
		return invalid.Annotated()
	}
	return err.Error()
}
