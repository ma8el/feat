package ui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// diagnosisModel is one `feat doctor` run, read on a screen.
//
// It holds a report and a cursor over it and nothing else: the checks run in
// the backend, where the host commands are built, and what arrives here is
// data (ADR-064). The same body is drawn inside the project wizard, which is
// why the rendering is on this type rather than on the screen that opens it.
type diagnosisModel struct {
	// project is what was checked, empty for every configured project.
	project string
	report  api.Diagnosis
	// scroll is the first line drawn. A report is longer than a dialog on any
	// machine with more than one project on it.
	scroll int

	// running reports that a pass is in flight. It walks repositories, asks Git
	// and Compose about them, and inspects containers, so it takes long enough
	// that a user who pressed the key has to be told it is happening.
	running bool
	// ran records that one has finished, so that an empty report and a report
	// nobody has asked for are different screens.
	ran bool
	err error
}

// Messages diagnosis sends itself.
type diagnosedMsg struct {
	project string
	report  api.Diagnosis
	err     error
}

// diagnose runs the checks for a project, or for every configured one.
func diagnose(backend Backend, project string) tea.Cmd {
	return func() tea.Msg {
		report, err := backend.Diagnose(context.Background(), project)
		return diagnosedMsg{project: project, report: report, err: err}
	}
}

// apply records what a pass found, and opens it where the user has to look.
//
// A report is mostly passes — sixteen of them on an ordinary project — and the
// pane it is drawn in holds a handful of lines. Opening at the top means opening
// on the checks that are fine, with the one that is not below the fold; opening
// at the first thing that needs attention puts it on the screen, and the count
// of what is above says the rest is still there.
func (d diagnosisModel) apply(message diagnosedMsg) diagnosisModel {
	d.running, d.ran = false, true
	d.project, d.err = message.project, message.err
	if message.err == nil {
		d.report = message.report
	}
	d.scroll = d.firstProblem()
	return d
}

// start reports that a pass was asked for.
func (d diagnosisModel) start(project string) diagnosisModel {
	d.running, d.project, d.err = true, project, nil
	return d
}

// scrollBy moves the report under the window, without moving past either end.
func (d diagnosisModel) scrollBy(delta int) diagnosisModel {
	d.scroll = min(max(0, d.scroll+delta), max(0, len(d.lines())-1))
	return d
}

// lines renders the whole report, one line at a time, so that scrolling and
// drawing agree about what a line is.
//
// A report is built rather than printed because the screen scrolls it: the
// terminal renderer can write straight to a stream and this cannot.
func (d diagnosisModel) lines() []string {
	lines, _ := d.render()
	return lines
}

// firstProblem is the line the report opens on: the first finding that is not a
// pass, or the top when every check passed.
func (d diagnosisModel) firstProblem() int {
	_, problem := d.render()
	return problem
}

// render builds the report and reports where the first thing worth reading is.
func (d diagnosisModel) render() ([]string, int) {
	if !d.ran {
		return nil, 0
	}

	problem := -1
	var out []string
	section := func(title string, findings []api.Finding) {
		if len(findings) == 0 {
			return
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		heading := len(out)
		out = append(out, titleStyle.Render(title))

		width := 0
		for _, finding := range findings {
			if len(finding.Check) > width {
				width = len(finding.Check)
			}
		}
		for _, finding := range findings {
			if problem < 0 && finding.Severity != api.SeverityOK {
				// The section's heading, not the finding: a finding on its own
				// says what is wrong without saying what it is wrong with.
				problem = heading
			}
			out = append(out, severityMarker(finding.Severity)+"  "+
				pad(finding.Check, width+2)+finding.Summary)
			// The action goes under the finding it belongs to, indented past both
			// columns, so it reads as an answer to the line above rather than as
			// another finding.
			if finding.Action != "" {
				out = append(out, strings.Repeat(" ", markerWidth+2)+mutedStyle.Render("→ "+finding.Action))
			}
		}
	}

	for _, checked := range d.report.Projects {
		section("project "+checked.ID, checked.Findings)
	}
	section("host", d.report.Host)
	return out, max(0, problem)
}

// markerWidth is the width of the severity column: the longest label, which is
// known here rather than discovered.
const markerWidth = 7

// severityMarker renders a severity as a fixed-width label.
//
// The label is a word, and the colour is what the word is drawn in rather than
// what carries the meaning: a terminal without colour loses nothing, which is
// the rule the rail's attention marks follow (FR-UI-002).
//
// There is no colour for a check that passed, because the palette has none and
// adding one is a change to the palette rather than to this screen (ADR-053).
// The four readings the palette does have are enough: a pass is quiet, a
// skipped check is plain text because it is not a pass and has to be noticed, a
// warning is the attention colour, and an error is the only thing here a user
// must act on.
func severityMarker(severity string) string {
	switch severity {
	case api.SeverityError:
		return failureStyle.Render(pad("error", markerWidth))
	case api.SeverityWarning:
		return attentionStyle.Render(pad("warning", markerWidth))
	case api.SeveritySkipped:
		return pad("skipped", markerWidth)
	default:
		return mutedStyle.Render(pad("ok", markerWidth))
	}
}

// summary counts the findings, so that a long report can be read from its last
// line.
func (d diagnosisModel) summary() string {
	counts := d.report.Counts()
	// Both forms are written out rather than derived, because the plural is not
	// always the singular with an "s" on the end: it is "checks passed", and a
	// summary that says "2 check passeds" is a summary nobody trusts the rest of.
	order := []struct {
		severity string
		one      string
		many     string
	}{
		{api.SeverityError, "error", "errors"},
		{api.SeverityWarning, "warning", "warnings"},
		{api.SeveritySkipped, "skipped check", "skipped checks"},
		{api.SeverityOK, "check passed", "checks passed"},
	}

	var parts []string
	for _, entry := range order {
		if counts[entry.severity] > 0 {
			parts = append(parts, count(counts[entry.severity], entry.one, entry.many))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

// body renders the report into a height, with what is above and below it noted
// rather than silently cut.
func (d diagnosisModel) body(width, height int) string {
	switch {
	case d.err != nil:
		return failureStyle.Render("the checks could not run: " + d.err.Error())
	case d.running:
		return mutedStyle.Render("checking " + d.subject() + "…")
	case !d.ran:
		return mutedStyle.Render("nothing has been checked yet")
	}

	lines := d.lines()
	if len(lines) == 0 {
		return mutedStyle.Render("no project is configured, so there was nothing to check")
	}

	var out strings.Builder
	first := min(d.scroll, max(0, len(lines)-1))
	last := min(len(lines), first+max(1, height))

	if first > 0 {
		out.WriteString(mutedStyle.Render("… "+count(first, "line", "lines")+" above") + "\n")
	}
	for _, line := range lines[first:last] {
		out.WriteString(truncate(line, max(1, width)) + "\n")
	}
	if last < len(lines) {
		out.WriteString(mutedStyle.Render("… " + count(len(lines)-last, "more line", "more lines")))
		out.WriteString("\n")
	}

	// The counts last, where a long report ends, and the sentence a skipped
	// check earns: a check that did not run has told the user nothing, and
	// counting it with the passes would be the claim ADR-033 refused to make.
	if summary := d.summary(); summary != "" {
		out.WriteString("\n" + mutedStyle.Render(summary) + "\n")
	}
	if d.report.Counts()[api.SeveritySkipped] > 0 {
		out.WriteString(mutedStyle.Render(
			"skipped checks are not passing checks; each one says why it did not run") + "\n")
	}
	// What the checks are true of. They ran in this process, and the daemon that
	// launches agents is another one with an environment of its own.
	if d.report.Environment != "" {
		out.WriteString(mutedStyle.Render("checked from "+d.report.Environment) + "\n")
	}
	return out.String()
}

// subject names what is being checked, for a line said while it runs.
func (d diagnosisModel) subject() string {
	if d.project == "" {
		return "every configured project"
	}
	return d.project + " against this machine"
}

// title names the screen for the dialog it is drawn in.
func (d diagnosisModel) title() string {
	if d.project == "" {
		return "diagnosis"
	}
	return "diagnosis · " + d.project
}

// diagnosisTailHeight is what this screen draws around the scrolling report:
// the two notes saying how much of it is above and below the window, the blank
// and the counts, the sentence a skipped check earns, the line saying where the
// checks ran, and the blank and the hints.
//
// The dialog's own cost is dialogVerticalChrome, which the box names so that the
// arithmetic is not written twice. The dialog clamps from the bottom, so an
// overrun costs the user the counts and the key that runs the checks again —
// which is the one action this screen offers. TestTheDiagnosisFitsItsDialog is
// what keeps this honest.
const (
	diagnosisTailHeight = 7
	diagnosisChrome     = dialogVerticalChrome + diagnosisTailHeight
)

// diagnosisHints are the keys the report answers.
func (m Model) diagnosisHints() string {
	if m.diagnosis.running {
		return keyHints(keyHint("esc", "close"))
	}
	return keyHints(
		keyHint("↑↓ pgup/pgdn", "read"),
		keyHint("r", "check again"),
		keyHint("esc", "close"),
	)
}
