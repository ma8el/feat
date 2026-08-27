package gitlab_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/forge"
	"github.com/ma8el/feat/internal/forge/gitlab"
)

// fakeGlab answers one glab invocation and records what it was asked.
type fakeGlab struct {
	commands []forge.Command
	output   forge.Output
	err      error
}

func (f *fakeGlab) Run(_ context.Context, command forge.Command) (forge.Output, error) {
	f.commands = append(f.commands, command)
	return f.output, f.err
}

// request is a well-formed merge request.
func request() forge.Request {
	return forge.Request{
		Directory:    "/state/feat/worktrees/app/7f3a1c2e/api",
		Remote:       "origin",
		SourceBranch: "feat/7f3a1c2e-rate-limit",
		TargetBranch: "main",
		Title:        "Add a rate limit to the public API",
		Body:         "It is per token.\n\n## Why\n\nBecause.",
	}
}

// TestTheAdapterBuildsAnArgumentVectorAndRunsItWhereTheProjectIs pins the
// command.
//
// The title and the description are agent-authored text the user approved, and
// each is one element of a vector: nothing is handed to a shell to re-split. The
// description is always passed, empty included, because a missing --description
// is what sends glab to an editor a daemon has no terminal for.
func TestTheAdapterBuildsAnArgumentVectorAndRunsItWhereTheProjectIs(t *testing.T) {
	glab := &fakeGlab{output: forge.Output{
		Stdout: "Creating merge request for feat/7f3a1c2e-rate-limit into main\n" +
			"https://gitlab.example.com/app/api/-/merge_requests/42\n",
	}}

	opened, err := gitlab.New(glab).Open(context.Background(), request())
	if err != nil {
		t.Fatalf("opening a merge request: %v", err)
	}
	if opened.URL != "https://gitlab.example.com/app/api/-/merge_requests/42" {
		t.Errorf("url = %q", opened.URL)
	}
	if opened.Reference != "!42" {
		t.Errorf("reference = %q, want GitLab's own name for the request", opened.Reference)
	}

	if len(glab.commands) != 1 {
		t.Fatalf("glab ran %d times, want once", len(glab.commands))
	}
	command := glab.commands[0]
	if command.Program != gitlab.Executable {
		t.Errorf("program = %q, want %q", command.Program, gitlab.Executable)
	}
	if command.Directory != request().Directory {
		t.Errorf("directory = %q, want the task worktree the project resolves from", command.Directory)
	}

	arguments := strings.Join(command.Arguments, "\x00")
	for _, want := range []string{
		"mr\x00create",
		"--source-branch\x00feat/7f3a1c2e-rate-limit",
		"--target-branch\x00main",
		"--title\x00Add a rate limit to the public API",
		"--description\x00It is per token.",
		"--yes",
	} {
		if !strings.Contains(arguments, want) {
			t.Errorf("the command %v does not carry %q", command.Arguments, want)
		}
	}
	// The description is one element, newlines and all.
	for _, argument := range command.Arguments {
		if strings.Contains(argument, "## Why") && !strings.Contains(argument, "It is per token.") {
			t.Errorf("the description was split across arguments: %q", argument)
		}
	}
}

// TestAnEmptyDescriptionIsStillPassed keeps glab out of an editor.
func TestAnEmptyDescriptionIsStillPassed(t *testing.T) {
	glab := &fakeGlab{output: forge.Output{
		Stdout: "https://gitlab.example.com/app/api/-/merge_requests/7",
	}}

	one := request()
	one.Body = ""
	if _, err := gitlab.New(glab).Open(context.Background(), one); err != nil {
		t.Fatalf("opening a merge request: %v", err)
	}

	arguments := glab.commands[0].Arguments
	for i, argument := range arguments {
		if argument == "--description" {
			if i+1 >= len(arguments) || arguments[i+1] != "" {
				t.Errorf("--description is not followed by an empty argument: %v", arguments)
			}
			return
		}
	}
	t.Errorf("the command does not pass --description at all: %v", arguments)
}

// TestAForgeThatRefusesIsTheRepositorysFailureAndNotThePublications is
// ADR-073's rule as this adapter sees it.
//
// A CLI that ran and refused is an answer — a protected branch, a project that
// is not there, a session that is no longer authenticated — and what it said is
// what the user would have read had they run it themselves.
func TestAForgeThatRefusesIsTheRepositorysFailureAndNotThePublications(t *testing.T) {
	glab := &fakeGlab{output: forge.Output{
		ExitCode: 1,
		Stderr:   "x GitLab: You are not allowed to push code to protected branches on this project.",
	}}

	_, err := gitlab.New(glab).Open(context.Background(), request())
	if err == nil {
		t.Fatal("a refused merge request was reported as opened")
	}
	var refusal *forge.Error
	if !errors.As(err, &refusal) {
		t.Fatalf("the failure is %T, want a forge refusal", err)
	}
	if refusal.Forge != domain.ForgeGitLab || refusal.ExitCode != 1 {
		t.Errorf("the refusal is %+v", refusal)
	}
	if !strings.Contains(refusal.Detail, "protected branches") {
		t.Errorf("the refusal does not carry what glab said: %q", refusal.Detail)
	}
}

// TestSuccessWithNoURLIsRefusedRatherThanRecordedAsNothing is the case a record
// cannot be honest about.
//
// The whole point of recording a result before the next repository begins is
// that Feat can name what exists. A run that reported success and printed no URL
// leaves nothing to name, so it is a failure that says where to look rather than
// a merge request recorded without an address.
func TestSuccessWithNoURLIsRefusedRatherThanRecordedAsNothing(t *testing.T) {
	glab := &fakeGlab{output: forge.Output{Stdout: "done\n"}}

	_, err := gitlab.New(glab).Open(context.Background(), request())
	if err == nil {
		t.Fatal("a merge request with no address was recorded")
	}
	if !strings.Contains(err.Error(), "Look on the forge before publishing again") {
		t.Errorf("the failure does not say what to do: %q", err)
	}
}

// TestARequestThatCannotBeSentIsRefusedBeforeTheCLIRuns checks the shared rules.
func TestARequestThatCannotBeSentIsRefusedBeforeTheCLIRuns(t *testing.T) {
	cases := map[string]func(*forge.Request){
		"no title":                  func(r *forge.Request) { r.Title = "  " },
		"a title over two lines":    func(r *forge.Request) { r.Title = "one\ntwo" },
		"a title that is an option": func(r *forge.Request) { r.Title = "--web" },
		"no directory":              func(r *forge.Request) { r.Directory = "" },
		"a branch merging itself":   func(r *forge.Request) { r.TargetBranch = r.SourceBranch },
		"a NUL in the description":  func(r *forge.Request) { r.Body = "a\x00b" },
	}

	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			glab := &fakeGlab{}
			one := request()
			damage(&one)

			if _, err := gitlab.New(glab).Open(context.Background(), one); err == nil {
				t.Fatal("the request was accepted")
			}
			if len(glab.commands) != 0 {
				t.Errorf("a refused request still ran %v", glab.commands)
			}
		})
	}
}

// TestADescriptionGlabWouldReadAsAModeSwitchIsRefused is the one value the
// neutral rules cannot catch.
//
// A description is the one field where a leading hyphen is ordinary prose — a
// Markdown list — so it is not refused for starting with one. A description of
// exactly "-" is glab's own documented shorthand for "open an editor", and a
// daemon has no terminal to open one on. It is refused rather than altered:
// what was displayed is what is sent, so a description Feat quietly changed
// would be worse than one it declined to send (ADR-070).
func TestADescriptionGlabWouldReadAsAModeSwitchIsRefused(t *testing.T) {
	glab := &fakeGlab{}

	one := request()
	one.Body = "-"
	_, err := gitlab.New(glab).Open(context.Background(), one)
	if err == nil {
		t.Fatal("a description glab reads as a request for an editor was sent")
	}
	if !strings.Contains(err.Error(), "open an editor") {
		t.Errorf("the refusal is %q, and it does not say what glab would do", err)
	}
	if len(glab.commands) != 0 {
		t.Errorf("a refused description still ran %v", glab.commands)
	}

	// A description that merely begins with a hyphen is prose and is sent.
	one.Body = "- it is per token\n- and per route"
	glab.output = forge.Output{Stdout: "https://gitlab.example.com/app/api/-/merge_requests/3"}
	if _, err := gitlab.New(glab).Open(context.Background(), one); err != nil {
		t.Fatalf("a Markdown list was refused as a description: %v", err)
	}
}

// TestTheAdapterNeverAsksGlabToRecoverAPreviousAttempt is what keeps the
// approval meaningful across a retry.
//
// glab writes a recovery file when a creation fails and loads the options back
// out of it when --recover is given. Feat must never take that path: what is
// sent has to be the words the user just read, not the ones a previous attempt
// left on disk.
func TestTheAdapterNeverAsksGlabToRecoverAPreviousAttempt(t *testing.T) {
	glab := &fakeGlab{output: forge.Output{
		Stdout: "https://gitlab.example.com/app/api/-/merge_requests/4",
	}}
	if _, err := gitlab.New(glab).Open(context.Background(), request()); err != nil {
		t.Fatalf("opening a merge request: %v", err)
	}

	for _, argument := range glab.commands[0].Arguments {
		if strings.HasPrefix(argument, "--recover") {
			t.Errorf("the command passes %q, which would send words nobody just read", argument)
		}
	}
}

// TestTheURLIsReadFromWhicheverStreamGlabUsed keeps the adapter from depending
// on which stream a version happens to print to.
func TestTheURLIsReadFromWhicheverStreamGlabUsed(t *testing.T) {
	glab := &fakeGlab{output: forge.Output{
		Stderr: "! Warning: something\nhttps://gitlab.example.com/app/api/-/merge_requests/9\n",
	}}

	opened, err := gitlab.New(glab).Open(context.Background(), request())
	if err != nil {
		t.Fatalf("opening a merge request: %v", err)
	}
	if opened.Reference != "!9" {
		t.Errorf("reference = %q", opened.Reference)
	}
}

// TestACLIThatCouldNotBeStartedIsNotARefusal keeps the two apart.
//
// A glab that could not be started establishes nothing about the forge, and it
// is a different thing from one that ran and said no.
func TestACLIThatCouldNotBeStartedIsNotARefusal(t *testing.T) {
	glab := &fakeGlab{err: errors.New("exec: \"glab\": executable file not found in $PATH")}

	_, err := gitlab.New(glab).Open(context.Background(), request())
	if err == nil {
		t.Fatal("a CLI that never ran was reported as having opened a request")
	}
	var refusal *forge.Error
	if errors.As(err, &refusal) {
		t.Errorf("a CLI that never ran was reported as a forge refusal: %v", err)
	}
}
