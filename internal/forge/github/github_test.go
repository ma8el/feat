package github_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/forge"
	"github.com/ma8el/feat/internal/forge/github"
)

// fakeGh answers one gh invocation and records what it was asked.
type fakeGh struct {
	commands []forge.Command
	output   forge.Output
	err      error
}

func (f *fakeGh) Run(_ context.Context, command forge.Command) (forge.Output, error) {
	f.commands = append(f.commands, command)
	return f.output, f.err
}

// request is a well-formed pull request.
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

// TestTheAdapterBuildsAnArgumentVectorAndRunsItWhereTheRepositoryIs pins the
// command.
//
// The title and the body are agent-authored text the user approved, and each is
// one element of a vector: nothing is handed to a shell to re-split.
func TestTheAdapterBuildsAnArgumentVectorAndRunsItWhereTheRepositoryIs(t *testing.T) {
	gh := &fakeGh{output: forge.Output{
		Stdout: "https://github.com/example/api/pull/42\n",
		Stderr: "Creating pull request for feat/7f3a1c2e-rate-limit into main in example/api\n",
	}}

	opened, err := github.New(gh).Open(context.Background(), request())
	if err != nil {
		t.Fatalf("opening a pull request: %v", err)
	}
	if opened.URL != "https://github.com/example/api/pull/42" {
		t.Errorf("url = %q", opened.URL)
	}
	if opened.Reference != "#42" {
		t.Errorf("reference = %q, want GitHub's own name for the request", opened.Reference)
	}

	if len(gh.commands) != 1 {
		t.Fatalf("gh ran %d times, want once", len(gh.commands))
	}
	command := gh.commands[0]
	if command.Program != github.Executable {
		t.Errorf("program = %q, want %q", command.Program, github.Executable)
	}
	if command.Directory != request().Directory {
		t.Errorf("directory = %q, want the task worktree the repository resolves from", command.Directory)
	}

	arguments := strings.Join(command.Arguments, "\x00")
	for _, want := range []string{
		"pr\x00create",
		"--head\x00feat/7f3a1c2e-rate-limit",
		"--base\x00main",
		"--title\x00Add a rate limit to the public API",
		"--body\x00It is per token.",
	} {
		if !strings.Contains(arguments, want) {
			t.Errorf("the command %v does not carry %q", command.Arguments, want)
		}
	}
}

// TestATitleAndBodyAreWhatMakeGhNonInteractive is the property that stands in
// for glab's --yes.
//
// gh has no confirmation flag. Supplying both is what keeps it from prompting,
// and without them it refuses rather than waiting for a terminal the daemon does
// not have — so the body is always passed, empty included.
func TestATitleAndBodyAreWhatMakeGhNonInteractive(t *testing.T) {
	gh := &fakeGh{output: forge.Output{Stdout: "https://github.com/example/api/pull/7"}}

	one := request()
	one.Body = ""
	if _, err := github.New(gh).Open(context.Background(), one); err != nil {
		t.Fatalf("opening a pull request: %v", err)
	}

	arguments := gh.commands[0].Arguments
	var body, title bool
	for i, argument := range arguments {
		switch argument {
		case "--body":
			body = true
			if i+1 >= len(arguments) || arguments[i+1] != "" {
				t.Errorf("--body is not followed by an empty argument: %v", arguments)
			}
		case "--title":
			title = true
		}
	}
	if !body || !title {
		t.Errorf("the command does not pass both --title and --body: %v", arguments)
	}
}

// TestABodyOfAHyphenIsOrdinaryTextHere is the difference from GitLab, and it is
// deliberate.
//
// glab reads a description of exactly "-" as a request to open an editor and its
// adapter refuses that one value. gh puts that meaning on --body-file and treats
// --body as text whatever it says, so copying the refusal here would refuse
// something that works.
func TestABodyOfAHyphenIsOrdinaryTextHere(t *testing.T) {
	gh := &fakeGh{output: forge.Output{Stdout: "https://github.com/example/api/pull/8"}}

	one := request()
	one.Body = "-"
	if _, err := github.New(gh).Open(context.Background(), one); err != nil {
		t.Fatalf("a body of \"-\" was refused: %v", err)
	}
	if !strings.Contains(strings.Join(gh.commands[0].Arguments, "\x00"), "--body\x00-") {
		t.Errorf("the body was not passed through: %v", gh.commands[0].Arguments)
	}
}

// TestAForgeThatRefusesIsTheRepositorysFailureAndNotThePublications is ADR-073's
// rule as this adapter sees it.
func TestAForgeThatRefusesIsTheRepositorysFailureAndNotThePublications(t *testing.T) {
	gh := &fakeGh{output: forge.Output{
		ExitCode: 1,
		Stderr:   "HTTP 403: Resource not accessible by personal access token",
	}}

	_, err := github.New(gh).Open(context.Background(), request())
	if err == nil {
		t.Fatal("a refused pull request was reported as opened")
	}
	var refusal *forge.Error
	if !errors.As(err, &refusal) {
		t.Fatalf("the failure is %T, want a forge refusal", err)
	}
	if refusal.Forge != domain.ForgeGitHub || refusal.ExitCode != 1 {
		t.Errorf("the refusal is %+v", refusal)
	}
	if !strings.Contains(refusal.Detail, "not accessible") {
		t.Errorf("the refusal does not carry what gh said: %q", refusal.Detail)
	}
}

// TestSuccessWithNoURLIsRefusedRatherThanRecordedAsNothing is the case a record
// cannot be honest about.
func TestSuccessWithNoURLIsRefusedRatherThanRecordedAsNothing(t *testing.T) {
	gh := &fakeGh{output: forge.Output{Stdout: "done\n"}}

	_, err := github.New(gh).Open(context.Background(), request())
	if err == nil {
		t.Fatal("a pull request with no address was recorded")
	}
	if !strings.Contains(err.Error(), "Look on the forge before publishing again") {
		t.Errorf("the failure does not say what to do: %q", err)
	}
}

// TestAnEnterpriseHostIsReadTheSameWay keeps the pattern off github.com.
//
// GitHub Enterprise is on whatever host the user runs, so the URL is matched on
// the path GitHub uses for a pull request rather than on a domain.
func TestAnEnterpriseHostIsReadTheSameWay(t *testing.T) {
	gh := &fakeGh{output: forge.Output{
		Stdout: "https://github.example.internal/example/api/pull/915\n",
	}}

	opened, err := github.New(gh).Open(context.Background(), request())
	if err != nil {
		t.Fatalf("opening a pull request: %v", err)
	}
	if opened.Reference != "#915" || !strings.HasPrefix(opened.URL, "https://github.example.internal/") {
		t.Errorf("the request reads as %+v", opened)
	}
}

// TestARequestThatCannotBeSentIsRefusedBeforeTheCLIRuns checks the shared rules.
func TestARequestThatCannotBeSentIsRefusedBeforeTheCLIRuns(t *testing.T) {
	cases := map[string]func(*forge.Request){
		"no title":                 func(r *forge.Request) { r.Title = "  " },
		"a title over two lines":   func(r *forge.Request) { r.Title = "one\ntwo" },
		"a title that is a flag":   func(r *forge.Request) { r.Title = "--web" },
		"no directory":             func(r *forge.Request) { r.Directory = "" },
		"a branch merging itself":  func(r *forge.Request) { r.TargetBranch = r.SourceBranch },
		"a NUL in the description": func(r *forge.Request) { r.Body = "a\x00b" },
	}

	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			gh := &fakeGh{}
			one := request()
			damage(&one)

			if _, err := github.New(gh).Open(context.Background(), one); err == nil {
				t.Fatal("the request was accepted")
			}
			if len(gh.commands) != 0 {
				t.Errorf("a refused request still ran %v", gh.commands)
			}
		})
	}
}

// TestACLIThatCouldNotBeStartedIsNotARefusal keeps the two apart.
func TestACLIThatCouldNotBeStartedIsNotARefusal(t *testing.T) {
	gh := &fakeGh{err: errors.New("exec: \"gh\": executable file not found in $PATH")}

	_, err := github.New(gh).Open(context.Background(), request())
	if err == nil {
		t.Fatal("a CLI that never ran was reported as having opened a request")
	}
	var refusal *forge.Error
	if errors.As(err, &refusal) {
		t.Errorf("a CLI that never ran was reported as a forge refusal: %v", err)
	}
}
