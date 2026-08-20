package claude_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/agent"
	"github.com/ma8el/feat/internal/agent/claude"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/integrationtest"
)

// requireClaude ends the test unless a real, authenticated Claude Code is
// available.
//
// These tests cannot run in CI: they need an authenticated installation and
// they spend a model call. They are the only place the hook schema is checked
// against the provider rather than against Feat's own fixtures, which is what
// docs/06-technical-architecture.md means by verifying flags and hook schemas
// against the installed version.
func requireClaude(t *testing.T) {
	t.Helper()

	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to run tests against a real, authenticated Claude Code", integrationtest.Env)
	}
	if _, err := exec.LookPath(claude.Executable); err != nil {
		integrationtest.Unavailable(t, integrationtest.Claude, "claude is not installed")
	}
}

// TestRealClaudeEmitsTheHooksThisAdapterInstalls drives the actual CLI.
//
// The failure it exists to catch is silence: a renamed hook event or a changed
// payload field produces a session that runs perfectly well and never reports
// anything, which no test against Feat's own fixtures can see. Here the fixtures
// are checked against the provider itself.
func TestRealClaudeEmitsTheHooksThisAdapterInstalls(t *testing.T) {
	requireClaude(t)

	workspace, err := control.Open(t.TempDir(), testProject, testTask, control.Options{})
	if err != nil {
		t.Fatalf("opening a control workspace: %v", err)
	}
	if err := workspace.Create(); err != nil {
		t.Fatalf("creating the control workspace: %v", err)
	}

	// A working directory of its own, so nothing here touches a real checkout.
	worktree := t.TempDir()
	task, err := domain.NewTask(testTask, testProject, "Say hello",
		domain.TaskSource{Kind: domain.SourcePrompt}, time.Now())
	if err != nil {
		t.Fatalf("building a task: %v", err)
	}
	if err := task.SetBrief("Reply with the single word: hello.", time.Now()); err != nil {
		t.Fatalf("setting the brief: %v", err)
	}

	adapter := claude.New()
	environment := agent.Environment{Mode: domain.ExecutionHost, Runner: agent.HostRunner{}}

	// Validation first, because a launch that could not have started would make
	// everything below meaningless.
	if err := adapter.Validate(context.Background(), environment); err != nil {
		t.Fatalf("validating a real Claude environment: %v", err)
	}
	version, err := adapter.Version(context.Background(), environment)
	if err != nil {
		t.Fatalf("reading the installed version: %v", err)
	}
	t.Logf("installed Claude Code %s; this adapter was verified against %s", version, claude.Verified())

	spec, err := adapter.Prepare(context.Background(), agent.PrepareRequest{
		Task:        task,
		Workspace:   agent.Workspace{WorkingDirectory: worktree, ControlPath: workspace.Root()},
		Control:     workspace,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("preparing a launch: %v", err)
	}

	// The real CLI, non-interactively, with exactly the flags a launch passes.
	// -p is the only difference from what the task terminal runs, and it is what
	// makes the session end by itself.
	arguments := append([]string{"-p"}, spec.Arguments...)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	session := exec.CommandContext(ctx, spec.Program, arguments...)
	session.Dir = spec.Directory
	output, err := session.CombinedOutput()
	if err != nil {
		t.Fatalf("running a real Claude session: %v\n%s", err, output)
	}

	messages, rejected, err := workspace.Pending()
	if err != nil {
		t.Fatalf("reading the control outbox: %v", err)
	}
	if len(rejected) != 0 {
		t.Errorf("a real session produced messages the protocol refused: %v", rejected)
	}
	if len(messages) == 0 {
		t.Fatalf("a real session produced no control messages; the installed hook schema may have changed.\n"+
			"Installed %s, verified against %s.\n%s", version, claude.Verified(), output)
	}

	seen := make(map[agent.EventKind]int)
	for _, message := range messages {
		event, ok, err := adapter.ParseEvent(context.Background(), message)
		if err != nil {
			t.Errorf("parsing a real hook payload: %v", err)
			continue
		}
		if !ok {
			t.Logf("a real hook produced no modelled event: %s", message.File())
			continue
		}
		seen[event.Kind]++
		if event.ProviderSessionID == "" {
			t.Errorf("event %s carries no provider session id; the payload field may have been renamed",
				event.Kind)
		}
	}

	// A session that started and ended a turn is the minimum a run produces, and
	// they are the two events the task lifecycle depends on most.
	for _, kind := range []agent.EventKind{agent.KindSessionStarted, agent.KindTurnEnded} {
		if seen[kind] == 0 {
			t.Errorf("a real session produced no %s event; the installed hook schema may have changed.\n"+
				"Installed %s, verified against %s.", kind, version, claude.Verified())
		}
	}
	t.Logf("a real session produced %d control messages: %v", len(messages), seen)
}

// TestRealClaudeReadsTheGeneratedSettings checks that the settings document is
// one the installed CLI accepts.
//
// A settings file Claude rejects is loaded as nothing, and the session then runs
// with no hooks at all — the same silence as a renamed event, from a different
// cause.
func TestRealClaudeReadsTheGeneratedSettings(t *testing.T) {
	requireClaude(t)

	_, workspace := prepared(t, false)
	settings, err := os.ReadFile(filepath.Join(workspace.AgentDir(), "settings.json"))
	if err != nil {
		t.Fatalf("reading the generated settings: %v", err)
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(settings, &document); err != nil {
		t.Fatalf("the generated settings are not valid JSON: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// `claude doctor` reads settings files in the current directory without a
	// trust prompt and reports what it found, which is the cheapest way to ask
	// the installed CLI whether it understands this document.
	check := exec.CommandContext(ctx, claude.Executable, "doctor")
	check.Dir = t.TempDir()
	output, err := check.CombinedOutput()
	if err != nil {
		integrationtest.Unavailable(t, integrationtest.Claude, "claude doctor did not run: %v\n%s", err, output)
	}
	if strings.Contains(strings.ToLower(string(output)), "invalid settings") {
		t.Errorf("claude doctor reports invalid settings:\n%s", output)
	}
}
