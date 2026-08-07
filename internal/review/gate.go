package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// Bounds on how long a gate may take.
//
// They are constants rather than configuration, for the reason ADR-032 gave the
// startup grace: the value bounds how long Feat waits while knowing nothing, and
// any value comfortably beyond a working case serves as well as any other. What
// they must never do is turn a slow check into a failed one, so a check that
// exceeds a bound is recorded as not having finished rather than as having
// failed (ADR-036).
const (
	// CheckTimeout bounds one check.
	CheckTimeout = 30 * time.Minute
	// GateTimeout bounds a whole run, however many checks it holds.
	GateTimeout = 60 * time.Minute
)

// Check is one configured verification command, resolved for one task.
//
// Its paths are in the terms of whatever will run it: a host check runs in the
// task worktree, and an agent check runs at the container path the project
// mounts that worktree at. Deciding which is the daemon's job, because only the
// daemon knows where the agent runs.
type Check struct {
	// ID is the configured check identifier, unique within its repository.
	ID string
	// RepositoryID is the repository the check belongs to.
	RepositoryID domain.RepositoryID
	// Program is the executable, as its environment resolves it.
	Program string
	// Arguments are its arguments, each already one vector element.
	Arguments []string
	// Directory is where it runs.
	Directory string
	// OnHost reports that the check runs on the trusted host rather than in the
	// agent's execution environment.
	OnHost bool
}

// Output is what a check produced.
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner runs one check where it belongs.
//
// It is an interface so that the same aggregation covers a command run on the
// host and one run inside a container: the daemon supplies one implementation
// per environment, and this package never learns what a container is.
type Runner interface {
	// Run executes the check and returns what it produced. A check that runs and
	// fails is not an error; a check that could not be started is.
	Run(ctx context.Context, check Check) (Output, error)
}

// Gate runs a task's configured checks and records what they produced.
//
// Every result it produces is attributed to the provider, because the gate ran
// the command itself. That is the difference acceptance criterion 5 is about: an
// agent's report is a claim, and this is evidence.
type Gate struct {
	// Host runs checks configured to run on the trusted host.
	Host Runner
	// Agent runs checks configured to run in the agent's execution environment.
	Agent Runner
	// Now supplies the current time. A nil value uses the wall clock.
	Now func() time.Time
	// CheckTimeout and Total override the bounds above. Only a test sets them:
	// waiting thirty real minutes to prove that a bound exists is not a test.
	CheckTimeout time.Duration
	Total        time.Duration
}

// Run executes each check and returns the results, in the order given.
//
// Nothing here fails the run as a whole. A check that could not be started, one
// that exceeded its bound, and one the run had no time left for are each
// recorded as themselves, because the alternative — abandoning the run — would
// throw away the results of the checks that did finish.
func (g Gate) Run(ctx context.Context, checks []Check) []domain.Check {
	now := g.Now
	if now == nil {
		now = time.Now
	}
	perCheck := g.CheckTimeout
	if perCheck <= 0 {
		perCheck = CheckTimeout
	}
	total := g.Total
	if total <= 0 {
		total = GateTimeout
	}

	deadline := now().Add(total)
	results := make([]domain.Check, 0, len(checks))

	for _, check := range checks {
		result := domain.Check{
			ID:           check.ID,
			RepositoryID: check.RepositoryID,
			Reporter:     domain.ReporterProvider,
			RanAt:        now(),
		}

		switch {
		case ctx.Err() != nil:
			result.Status = domain.CheckUnknown
			result.Detail = "the daemon stopped before this check ran"
		case !now().Before(deadline):
			result.Status = domain.CheckUnknown
			result.Detail = "the gate's bound of " + total.String() + " elapsed before this check ran"
		default:
			g.run(ctx, check, perCheck, &result)
		}
		results = append(results, result)
	}
	return results
}

// run executes one check and records what it produced.
func (g Gate) run(ctx context.Context, check Check, bound time.Duration, result *domain.Check) {
	runner := g.Agent
	where := "the agent's execution environment"
	if check.OnHost {
		runner, where = g.Host, "this host"
	}
	if runner == nil {
		result.Status = domain.CheckUnknown
		result.Detail = "this build has nothing to run a check in " + where + " with"
		return
	}

	bounded, cancel := context.WithTimeout(ctx, bound)
	defer cancel()

	output, err := runner.Run(bounded, check)
	switch {
	case errors.Is(bounded.Err(), context.DeadlineExceeded):
		// Deliberately not a failure. The check did not report, and saying it
		// failed would put words in the mouth of a command that never finished.
		result.Status = domain.CheckUnknown
		result.Detail = "did not finish within " + bound.String() + "; " + excerpt(output)
	case err != nil:
		result.Status = domain.CheckUnknown
		result.Detail = "could not be started in " + where + ": " + err.Error()
	case output.ExitCode == 0:
		result.Status = domain.CheckPassed
		result.Detail = excerpt(output)
	default:
		result.Status = domain.CheckFailed
		result.Detail = "exited with status " + fmt.Sprint(output.ExitCode) + "\n" + excerpt(output)
	}
	result.Detail = strings.TrimSpace(result.Detail)
	if len(result.Detail) > domain.MaxCheckDetail {
		result.Detail = result.Detail[:domain.MaxCheckDetail]
	}
}

// Skip records a check that was deliberately not run.
//
// A check that did not run is never absent: a project that configured one and a
// screen that shows nothing would leave a user to work out for themselves which
// of the two it was (ADR-028's rule for diagnostics).
func Skip(check Check, reason string, now time.Time) domain.Check {
	return domain.Check{
		ID:           check.ID,
		RepositoryID: check.RepositoryID,
		Status:       domain.CheckSkipped,
		Reporter:     domain.ReporterProvider,
		Detail:       reason,
		RanAt:        now,
	}
}

// Verdict is what a gate run means for the task.
type Verdict struct {
	// Passed reports that every check the gate ran reported success.
	Passed bool
	// Summary says what happened, in one line.
	Summary string
	// Failed and Inconclusive count the results that decided it.
	Failed       int
	Inconclusive int
	Passing      int
	Skipped      int
}

// Decide reads a gate's results.
//
// A run passes only when every check that ran passed. A check that could not be
// started, or that exceeded its bound, is inconclusive and does not pass: a task
// that reached ready_for_review on the strength of a check nobody managed to run
// would be claiming a verification that did not happen, which is the whole thing
// the reporter distinction exists to prevent.
//
// A deliberately skipped check does not block, because skipping one is Feat's
// own decision and it says so on the screen.
func Decide(results []domain.Check) Verdict {
	verdict := Verdict{}
	for _, result := range results {
		switch result.Status {
		case domain.CheckPassed:
			verdict.Passing++
		case domain.CheckFailed:
			verdict.Failed++
		case domain.CheckSkipped:
			verdict.Skipped++
		default:
			verdict.Inconclusive++
		}
	}
	verdict.Passed = verdict.Failed == 0 && verdict.Inconclusive == 0

	var parts []string
	if verdict.Passing > 0 {
		parts = append(parts, fmt.Sprintf("%d passed", verdict.Passing))
	}
	if verdict.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", verdict.Failed))
	}
	if verdict.Inconclusive > 0 {
		parts = append(parts, fmt.Sprintf("%d did not report", verdict.Inconclusive))
	}
	if verdict.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", verdict.Skipped))
	}
	if len(parts) == 0 {
		verdict.Summary = "the project configures no checks for this task's repositories"
		return verdict
	}
	verdict.Summary = strings.Join(parts, ", ")
	return verdict
}

// excerpt renders the tail of what a check printed.
//
// The tail rather than the head, because a test runner's failure summary is at
// the end and its progress output is at the start. It is bounded here as well as
// in the domain, so that what a review document holds is decided by the code
// that produced it rather than by a limit further downstream.
func excerpt(output Output) string {
	combined := strings.TrimSpace(output.Stdout)
	if trimmed := strings.TrimSpace(output.Stderr); trimmed != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += trimmed
	}
	if combined == "" {
		return ""
	}

	// Half the stored bound, so that the status line a caller prepends to it
	// cannot push the result over the limit.
	const limit = domain.MaxCheckDetail / 2
	if len(combined) <= limit {
		return combined
	}
	cut := combined[len(combined)-limit:]
	if index := strings.IndexByte(cut, '\n'); index >= 0 && index < len(cut)-1 {
		cut = cut[index+1:]
	}
	return "…\n" + cut
}
