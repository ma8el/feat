package api

// Diagnosis is one `feat doctor` run, in the shape a screen can draw.
//
// It crosses this package rather than internal/project's own types because the
// dashboard is a client and reaches no adapter (ADR-031): the checks run where
// the host commands are built, and what comes back is data. Nothing publishes
// it over the socket today — diagnosis works before a daemon exists (ADR-028),
// so it is run by the process the user is in front of — and it is described
// here so that a daemon that publishes one later changes no renderer.
type Diagnosis struct {
	// Host holds the findings about this machine, which no project changes.
	Host []Finding `json:"host"`
	// Projects holds the findings for each project that was checked.
	Projects []ProjectDiagnosis `json:"projects"`
	// Environment says where the checks were run from, because that is what
	// they are about. A check is only true of the process that ran it: a tool
	// on this terminal's PATH is not necessarily on the daemon's.
	Environment string `json:"environment"`
}

// ProjectDiagnosis is what was found out about one project.
type ProjectDiagnosis struct {
	// ID is the project identifier.
	ID string `json:"id"`
	// File is the configuration file the findings are about.
	File string `json:"file"`
	// Findings are the results for this project.
	Findings []Finding `json:"findings"`
}

// Finding is one diagnostic result.
type Finding struct {
	// Check names what was checked, in the same dotted form configuration uses
	// where there is a corresponding field.
	Check string `json:"check"`
	// Severity is how much the finding demands: ok, skipped, warning, or error.
	Severity string `json:"severity"`
	// Summary says what was found.
	Summary string `json:"summary"`
	// Action says what to do about it, and is empty when there is nothing to do.
	Action string `json:"action,omitempty"`
}

// Severities a finding may carry.
//
// Skipped is not a pass. A diagnostic that claimed a check it did not run would
// be worse than no diagnostic at all, so a skipped check says why it did not
// run and is counted separately wherever findings are counted (ADR-033).
const (
	SeverityOK      = "ok"
	SeveritySkipped = "skipped"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// Failed reports whether any finding is an error, which is what stops a project
// from working.
func (d Diagnosis) Failed() bool {
	for _, finding := range d.Host {
		if finding.Severity == SeverityError {
			return true
		}
	}
	for _, project := range d.Projects {
		for _, finding := range project.Findings {
			if finding.Severity == SeverityError {
				return true
			}
		}
	}
	return false
}

// Counts returns how many findings there are of each severity.
func (d Diagnosis) Counts() map[string]int {
	counts := make(map[string]int, 4)
	count := func(findings []Finding) {
		for _, finding := range findings {
			counts[finding.Severity]++
		}
	}
	count(d.Host)
	for _, project := range d.Projects {
		count(project.Findings)
	}
	return counts
}
