package tracker

import (
	"context"
	"errors"
	"flag"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeRunner answers with what a tracker is meant to have printed.
type fakeRunner struct {
	output  []byte
	err     error
	command Command
}

func (f *fakeRunner) Run(_ context.Context, command Command) ([]byte, error) {
	f.command = command
	return f.output, f.err
}

// TestTheCommandIsRunAsAnArgumentVectorWithNoFilter is the rule that Feat passes
// a tracker command no filter: a filter vocabulary would have to map onto every
// tracker's query language, so what the user's tickets are is the command's
// decision (ADR-071). What reaches the runner is therefore exactly what the
// project configured, each element separate.
func TestTheCommandIsRunAsAnArgumentVectorWithNoFilter(t *testing.T) {
	runner := &fakeRunner{output: []byte(`[]`)}
	command := Command{
		Program:   "gh",
		Arguments: []string{"issue", "list", "--assignee", "@me"},
		Directory: "/work/home",
	}

	if _, err := List(context.Background(), runner, command); err != nil {
		t.Fatalf("listing tickets: %v", err)
	}

	if runner.command.Program != "gh" {
		t.Errorf("program = %q", runner.command.Program)
	}
	if got, want := strings.Join(runner.command.Arguments, "\x00"),
		strings.Join(command.Arguments, "\x00"); got != want {
		t.Errorf("arguments = %q, want %q; Feat adds nothing to a tracker command", got, want)
	}
	if runner.command.Directory != "/work/home" {
		t.Errorf("directory = %q, and it is explicit so that the daemon and `feat doctor` "+
			"ask the same question of the same machine", runner.command.Directory)
	}
}

// TestACommandThatCannotBeRunSafelyIsRefused covers the vectors a project could
// configure that must never reach a process.
func TestACommandThatCannotBeRunSafelyIsRefused(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		command Command
		want    string
	}{
		{
			name:    "no program",
			command: Command{Directory: "/work/home"},
			want:    "names no command",
		},
		{
			name:    "a program that would be read as an option",
			command: Command{Program: "--version", Directory: "/work/home"},
			want:    "would be read as an option",
		},
		{
			name: "an argument carrying a newline",
			command: Command{Program: "gh", Arguments: []string{"issue\nlist"},
				Directory: "/work/home"},
			want: "NUL or a newline",
		},
		{
			name:    "no directory to run in",
			command: Command{Program: "gh"},
			want:    "no directory to run in",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &fakeRunner{output: []byte(`[]`)}
			_, err := List(context.Background(), runner, testCase.command)
			if err == nil {
				t.Fatal("the command was accepted")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("the refusal does not say %q: %v", testCase.want, err)
			}
			if runner.command.Program != "" {
				t.Error("the command reached the runner, and it should have been refused first")
			}
		})
	}
}

// TestAFailingCommandIsReportedInItsOwnWords checks that the reason a tracker
// gave reaches the user, since the command is theirs to fix.
func TestAFailingCommandIsReportedInItsOwnWords(t *testing.T) {
	runner := &fakeRunner{err: errors.New("gh: HTTP 401: Bad credentials")}

	_, err := List(context.Background(), runner,
		Command{Program: "gh", Arguments: []string{"issue", "list"}, Directory: "/work/home"})
	if err == nil {
		t.Fatal("a failing tracker command was accepted")
	}
	if !strings.Contains(err.Error(), "Bad credentials") {
		t.Errorf("the failure does not carry the tracker's own words: %v", err)
	}
}

// The host runner is exercised against a real process, because what it owns is
// what happens to one: a pipe closed at the bound, an exit status, and a command
// that never answers. The process is this test binary re-run as the helper
// below, so these need no tracker, no account, and no network — which is why
// they are ordinary tests rather than the opt-in tier.

// TestARealCommandIsReadFromStandardOutput checks that what Feat parses is
// standard output, and that a tracker writing to standard error as well is
// still read.
func TestARealCommandIsReadFromStandardOutput(t *testing.T) {
	skipWithoutHelper(t)

	output, err := HostRunner{}.Run(context.Background(), helperCommand(t, "tickets"))
	if err != nil {
		t.Fatalf("running the helper: %v", err)
	}
	tickets, err := Parse(output)
	if err != nil {
		t.Fatalf("the helper's output was refused: %v", err)
	}
	if len(tickets) != 1 || tickets[0].Reference != "ACME-14" {
		t.Errorf("read %+v", tickets)
	}
}

// TestARealCommandPastTheBoundIsRefusedBySize is the rule that a tracker's
// output is bounded: a command that keeps printing is refused by size rather
// than read to the end (ADR-071).
func TestARealCommandPastTheBoundIsRefusedBySize(t *testing.T) {
	skipWithoutHelper(t)

	_, err := HostRunner{}.Run(context.Background(), helperCommand(t, "flood"))
	if err == nil {
		t.Fatal("an oversized answer was accepted")
	}
	var rejection *RejectionError
	if !errors.As(err, &rejection) {
		t.Fatalf("the refusal is %T, and an oversized answer is refused by size: %v", err, err)
	}
	if !strings.Contains(err.Error(), "larger than") {
		t.Errorf("the refusal does not say the output was too large: %v", err)
	}
}

// TestACommandThatNarratesIsStillRead is why standard error is truncated rather
// than stopped at its bound: a tracker that reports its progress while working
// is ordinary, and refusing one for saying too much would be a command that
// works everywhere except under Feat.
func TestACommandThatNarratesIsStillRead(t *testing.T) {
	skipWithoutHelper(t)

	output, err := HostRunner{}.Run(context.Background(), helperCommand(t, "narrate"))
	if err != nil {
		t.Fatalf("a command that narrated on standard error was refused: %v", err)
	}
	tickets, err := Parse(output)
	if err != nil {
		t.Fatalf("the command's output was refused: %v", err)
	}
	if len(tickets) != 1 {
		t.Errorf("read %d tickets, want the one it printed", len(tickets))
	}
}

// TestARealCommandThatExitsNonZeroSaysWhy checks that a tracker's own message
// reaches the user rather than an exit status nobody can act on.
func TestARealCommandThatExitsNonZeroSaysWhy(t *testing.T) {
	skipWithoutHelper(t)

	_, err := HostRunner{}.Run(context.Background(), helperCommand(t, "fail"))
	if err == nil {
		t.Fatal("a command that exited non-zero was accepted")
	}
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Errorf("the failure does not carry the command's own words: %v", err)
	}
}

// TestACommandThatDoesNotAnswerIsBounded checks that the caller's bound is what
// ends a stuck tracker, and that the report names the budget that applied.
//
// The bound is the caller's rather than this package's, so that one place holds
// the number: the daemon's is half of a contract its client waits on, and `feat
// doctor` bounds every command it runs the same way.
func TestACommandThatDoesNotAnswerIsBounded(t *testing.T) {
	skipWithoutHelper(t)

	const budget = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	started := time.Now()
	_, err := HostRunner{}.Run(ctx, helperCommand(t, "hang"))
	if err == nil {
		t.Fatal("a command that never answered was accepted")
	}
	if !strings.Contains(err.Error(), "did not answer within") {
		t.Errorf("the failure does not say the command did not answer: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Minute {
		t.Errorf("the run took %s, and the caller's bound was %s", elapsed, budget)
	}
}

// TestACancelledCommandSaysItWasCancelled separates the two ways a run can end
// early, because they mean different things: a bound that expired is a tracker
// that is stuck, and a cancellation is somebody having stopped waiting.
func TestACancelledCommandSaysItWasCancelled(t *testing.T) {
	skipWithoutHelper(t)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	defer cancel()

	_, err := HostRunner{}.Run(ctx, helperCommand(t, "hang"))
	if err == nil {
		t.Fatal("a cancelled command was accepted")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("the failure does not say the run was cancelled: %v", err)
	}
}

// skipWithoutHelper skips a test that re-runs this binary where that cannot
// work.
func skipWithoutHelper(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the helper re-runs this test binary, and Feat targets macOS and Linux")
	}
}

// helperCommand builds a Command that re-runs this test binary as the tracker.
//
// The mode is a positional argument rather than an environment variable, so
// that the command is exactly the argument vector the runner is given and
// nothing about the run is arranged out of band.
func helperCommand(t *testing.T, mode string) Command {
	t.Helper()
	return Command{
		Program:   os.Args[0],
		Arguments: []string{"-test.run=TestHelperTracker", mode},
		Directory: t.TempDir(),
	}
}

// TestHelperTracker is the tracker the host-runner tests drive. It is a tracker
// only when a mode is named, so an ordinary run of this package skips it.
func TestHelperTracker(t *testing.T) {
	if flag.NArg() == 0 {
		t.Skip("not running as the helper tracker")
	}

	switch flag.Arg(0) {
	case "tickets":
		_, _ = os.Stderr.WriteString("fetching…\n")
		_, _ = os.Stdout.WriteString(
			`[{"reference":"ACME-14","title":"t","body":"","url":"u","state":"open"}]`)
	case "fail":
		_, _ = os.Stderr.WriteString("gh: not authenticated\n")
		os.Exit(1)
	case "narrate":
		// More on standard error than Feat will keep, and a valid document on
		// standard output after it.
		line := strings.Repeat("working ", 512) + "\n"
		for range 8 {
			_, _ = os.Stderr.WriteString(line)
		}
		_, _ = os.Stdout.WriteString(
			`[{"reference":"ACME-14","title":"t","body":"","url":"u","state":"open"}]`)
	case "flood":
		block := strings.Repeat("x", 4<<10)
		for range 2 * MaxOutputBytes / len(block) {
			if _, err := os.Stdout.WriteString(block); err != nil {
				break
			}
		}
	case "hang":
		time.Sleep(time.Minute)
	}
	// Exiting here rather than returning keeps the test framework's own report
	// off the standard output the caller is about to parse.
	os.Exit(0)
}
