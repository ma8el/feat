package cli

import (
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/project"
)

const doctorLong = `Check this machine and every configured project.

Diagnostics change nothing. They run before a daemon is started and before a
project is registered, which is the order to work in: write the configuration,
run this, fix what it reports, then register the project.

A check this build cannot run is reported as skipped rather than passed, with
the reason it did not run. The checks inside the agent's execution environment
are asked where that environment is: on this machine for a host-mode project,
and inside a running container of the project for one that configures a
devcontainer. Nothing is started to answer them, so those checks are skipped
until the project has a task running, and running this again then checks them.

The exit code is 0 when nothing failed and 1 when something did. Warnings do not
fail the run.`

func newDoctorCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose host prerequisites and project configuration",
		Long:  doctorLong,
		Args:  checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, options, err := env.project()
			if err != nil {
				return err
			}

			report, err := project.Diagnose(cmd.Context(), project.Options{
				ConfigDir: layout.ProjectConfigDir(),
				Resolve:   options,
				Runner:    env.runner,
				// Registration is reported when a daemon can be asked. Starting
				// one to answer a diagnostic question would make a command that
				// changes nothing change something.
				Registered: registeredProjects(cmd.Context(), layout),
			})
			if err != nil {
				return err
			}

			printReport(cmd.OutOrStdout(), report, layout.ProjectConfigDir())
			if report.Failed() {
				// The findings are already on stdout, so the error adds only
				// what the exit code means.
				return errFailedDiagnosis
			}
			return nil
		},
	}
}

// errFailedDiagnosis reports that diagnostics found an error-level problem.
var errFailedDiagnosis = &diagnosisError{}

// diagnosisError is the failure of `feat doctor` itself.
//
// It carries no detail because the report above it carries all of it; what it
// adds is the exit code, so that a script can tell a clean machine from one
// that needs work without reading the output.
type diagnosisError struct{}

func (e *diagnosisError) Error() string {
	return "the checks above found problems that will stop Feat from working"
}

// printReport renders a diagnostic report.
func printReport(out io.Writer, report project.Report, configDir string) {
	printf(out, "host\n")
	printFindings(out, report.Host)

	for _, diagnosis := range report.Projects {
		// The configuration file is not printed separately: the first finding
		// is about that file and names it, so a line above would only be the
		// same path twice.
		printf(out, "\nproject %s\n", diagnosis.ID)
		printFindings(out, diagnosis.Findings)
		printDiagnosisMounts(out, diagnosis)
	}

	if len(report.Projects) == 0 {
		printf(out, "\nno projects are configured in %s\n", configDir)
		printf(out, "write one there, named <project>.yaml, and register it with `feat project add <project>`\n")
	}
	printSummary(out, report)
}

// printFindings renders one section's findings.
//
// The check column is padded to the widest check in the section, so the
// summaries line up and the section can be scanned down. An action goes on its
// own line under the finding it belongs to, indented past both columns, so it
// reads as an answer to the line above rather than as another finding.
func printFindings(out io.Writer, findings []project.Finding) {
	width := 0
	for _, finding := range findings {
		if len(finding.Check) > width {
			width = len(finding.Check)
		}
	}

	for _, finding := range findings {
		label := marker(finding.Severity)
		printf(out, "  %-*s  %-*s  %s\n", markerWidth, label, width, finding.Check, finding.Summary)
		if finding.Action != "" {
			printf(out, "  %*s  %*s  -> %s\n", markerWidth, "", width, "", finding.Action)
		}
	}
}

// markerWidth is the width of the severity column: the longest label plus
// nothing, since the labels are known here rather than discovered.
const markerWidth = 7

// printDiagnosisMounts prints the repository-to-container path mapping for a
// project whose configuration loaded.
func printDiagnosisMounts(out io.Writer, diagnosis project.Diagnosis) {
	if diagnosis.Config == nil {
		return
	}
	mounts := mountTable(diagnosis.Config)
	if mounts.empty() {
		return
	}
	printf(out, "\n  repositories\n")
	mounts.render(out, "  ")
}

// printSummary counts the findings, so that a long report ends with something
// readable.
func printSummary(out io.Writer, report project.Report) {
	counts := report.Counts()
	order := []project.Severity{
		project.SeverityError, project.SeverityWarning,
		project.SeveritySkipped, project.SeverityOK,
	}

	var parts []string
	for _, severity := range order {
		if counts[severity] > 0 {
			parts = append(parts, label(counts[severity], severity))
		}
	}
	if len(parts) == 0 {
		return
	}
	printf(out, "\n%s\n", join(parts))

	if counts[project.SeveritySkipped] > 0 {
		// What a skipped check says is the reason, not a slice number. Naming
		// the condition is what lets a reader act on it, and it is what the
		// findings actually carry (ADR-033).
		printf(out, "skipped checks are not passing checks; each one says why it did not run\n")
	}
}

// marker renders a severity as a short fixed-width label.
func marker(severity project.Severity) string {
	switch severity {
	case project.SeverityOK:
		return "ok"
	case project.SeverityWarning:
		return "warning"
	case project.SeverityError:
		return "ERROR"
	case project.SeveritySkipped:
		return "skipped"
	default:
		return string(severity)
	}
}

// label renders a count of findings at one severity.
//
// "error" and "warning" are nouns and take a plural; "ok" and "skipped"
// describe the checks and do not, so the summary reads "2 errors, 6 skipped"
// rather than "2 errors, 6 skippeds".
func label(count int, severity project.Severity) string {
	text := strconv.Itoa(count) + " " + string(severity)
	switch severity {
	case project.SeverityError, project.SeverityWarning:
		if count != 1 {
			text += "s"
		}
	}
	return text
}

// join renders a list in prose.
func join(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
	}
}
