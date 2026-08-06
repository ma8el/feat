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
	"github.com/ma8el/feat/internal/agent/agenttest"
	"github.com/ma8el/feat/internal/agent/claude"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
)

const (
	testProject = "app"
	testTask    = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
	testBrief   = "# Add a health endpoint\n\nReturn 200 from /health.\n"
)

func prepared(t *testing.T, outsideBoundary bool) (agent.LaunchSpec, *control.Workspace) {
	t.Helper()

	workspace, err := control.Open(t.TempDir(), testProject, testTask, control.Options{})
	if err != nil {
		t.Fatalf("opening a control workspace: %v", err)
	}
	if err := workspace.Create(); err != nil {
		t.Fatalf("creating the control workspace: %v", err)
	}

	task, err := domain.NewTask(testTask, testProject, "Add a health endpoint",
		domain.TaskSource{Kind: domain.SourcePrompt}, time.Now())
	if err != nil {
		t.Fatalf("building a task: %v", err)
	}
	if err := task.SetBrief(testBrief, time.Now()); err != nil {
		t.Fatalf("setting the brief: %v", err)
	}

	spec, err := claude.New().Prepare(context.Background(), agent.PrepareRequest{
		Task: task,
		Workspace: agent.Workspace{
			WorkingDirectory: "/srv/api",
			ControlPath:      "/feat",
		},
		Control:     workspace,
		Environment: agent.Environment{Mode: domain.ExecutionHost, OutsideConfiguredBoundary: outsideBoundary},
	})
	if err != nil {
		t.Fatalf("preparing a launch: %v", err)
	}
	return spec, workspace
}

// TestClaudeLaunchesInTheTaskDirectoryWithTheFinalBrief is the adapter half of
// the first slice 7 acceptance criterion. The daemon half, which proves the
// directory is the task's own primary worktree, lives in internal/daemon.
func TestClaudeLaunchesInTheTaskDirectoryWithTheFinalBrief(t *testing.T) {
	spec, workspace := prepared(t, false)

	if spec.Program != claude.Executable {
		t.Errorf("program = %q, want %q", spec.Program, claude.Executable)
	}
	if spec.Directory != "/srv/api" {
		t.Errorf("working directory = %q, want the agent's view of the task worktree", spec.Directory)
	}

	// The brief reaches the agent as a file rather than as an argument, so a
	// quarter-megabyte brief neither lands in the process argument vector nor
	// appears in ps output for every user on the machine.
	stored, err := os.ReadFile(workspace.BriefPath())
	if err != nil {
		t.Fatalf("reading the written brief: %v", err)
	}
	if string(stored) != testBrief {
		t.Errorf("written brief = %q, want the confirmed brief verbatim", stored)
	}
	for _, argument := range spec.Arguments {
		if strings.Contains(argument, "Return 200 from /health") {
			t.Errorf("the brief body reached the argument vector in %q", argument)
		}
	}

	// The initial prompt has to name the brief, or the session starts without
	// knowing where its own task is.
	last := spec.Arguments[len(spec.Arguments)-1]
	if !strings.Contains(last, "/feat/task.md") {
		t.Errorf("initial prompt = %q, want it to name the brief inside the agent's own filesystem", last)
	}

	settings := flagValue(t, spec.Arguments, "--settings")
	if settings != "/feat/agent/settings.json" {
		t.Errorf("--settings = %q, want the generated file as the agent sees it", settings)
	}

	// The brief lives outside the working directory, so without this the
	// session's first act is to ask permission to read the document Feat wrote
	// for it — on every task launch. A permission dialog nobody needed teaches a
	// user to click through the ones that matter.
	if added := flagValue(t, spec.Arguments, "--add-dir"); added != "/feat" {
		t.Errorf("--add-dir = %q, want the control workspace", added)
	}

	// --add-dir takes a list, so a prompt directly after it is read as a second
	// directory and the session starts with no task at all. The symptom is a
	// session that runs perfectly and simply never does anything, which is worth
	// a test rather than a careful reader.
	for i, argument := range spec.Arguments {
		if argument == "--add-dir" && i+2 >= len(spec.Arguments) {
			t.Errorf("--add-dir is the last flag before the prompt, so the prompt would be read as a directory: %v",
				spec.Arguments)
		}
	}
	// Narrowing the setting sources would switch off the project's checked-in
	// configuration, which docs/06 requires to keep applying.
	for _, argument := range spec.Arguments {
		if argument == "--setting-sources" {
			t.Error("the launch narrows --setting-sources; a project's own CLAUDE.md and settings must keep applying")
		}
	}
}

func TestGeneratedFilesStayInTheHostOnlyArea(t *testing.T) {
	_, workspace := prepared(t, false)

	// Everything generated belongs under agent/. A file in outbox/ would be a
	// message; a file in the repository would be the thing docs/06 says must
	// never happen.
	for _, dir := range []string{workspace.OutboxDir(), workspace.InboxDir(), workspace.ReportsDir()} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		if len(entries) != 0 {
			t.Errorf("%s holds %d generated entries, want none", dir, len(entries))
		}
	}
	for _, name := range []string{
		"settings.json",
		"instructions.md",
		filepath.Join("hooks", "session-start.sh"),
		filepath.Join("hooks", "user-prompt-submit.sh"),
		filepath.Join("hooks", "stop.sh"),
		filepath.Join("hooks", "stop-failure.sh"),
		filepath.Join("hooks", "notification.sh"),
		filepath.Join("hooks", "session-end.sh"),
		filepath.Join("bin", "feat-report"),
	} {
		if _, err := os.Stat(filepath.Join(workspace.AgentDir(), name)); err != nil {
			t.Errorf("generated file %s: %v", name, err)
		}
	}
}

func TestGeneratedSettingsInstallTheVerifiedHooks(t *testing.T) {
	_, workspace := prepared(t, false)

	data, err := os.ReadFile(filepath.Join(workspace.AgentDir(), "settings.json"))
	if err != nil {
		t.Fatalf("reading the generated settings: %v", err)
	}

	var document struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("the generated settings are not valid JSON: %v", err)
	}

	// The names are Claude's, verified against the installed version. A typo
	// here produces a session that runs perfectly and never reports anything,
	// which is the failure this test exists to make loud.
	want := []string{"SessionStart", "UserPromptSubmit", "Stop", "StopFailure", "Notification", "SessionEnd"}
	if len(document.Hooks) != len(want) {
		t.Errorf("installed %d hook events, want %d: %v", len(document.Hooks), len(want), keys(document.Hooks))
	}
	for _, event := range want {
		installed, ok := document.Hooks[event]
		if !ok {
			t.Errorf("hook %s is not installed", event)
			continue
		}
		if len(installed) != 1 || len(installed[0].Hooks) != 1 {
			t.Errorf("hook %s has an unexpected shape", event)
			continue
		}
		command := installed[0].Hooks[0]
		if command.Type != "command" {
			t.Errorf("hook %s type = %q, want %q", event, command.Type, "command")
		}
		if command.Timeout <= 0 {
			t.Errorf("hook %s has no timeout", event)
		}
		// Claude runs the command through a shell, so a path with a space in it
		// must still be one word.
		if !strings.HasPrefix(command.Command, "'/feat/agent/hooks/") {
			t.Errorf("hook %s command = %q, want a quoted absolute path in the agent's filesystem", event, command.Command)
		}
	}

	// The generated settings carry hooks and nothing else. A model, a
	// permission mode, or a tool policy here would be Feat quietly deciding how
	// somebody else's agent behaves.
	var everything map[string]any
	if err := json.Unmarshal(data, &everything); err != nil {
		t.Fatalf("re-reading the generated settings: %v", err)
	}
	if len(everything) != 1 {
		t.Errorf("the generated settings carry %v, want only hooks", keysOf(everything))
	}
}

// TestGeneratedHooksObserveWithoutChangingTheSession pins the two properties
// that decide whether a hook observes a session or interferes with it.
//
// Claude injects a UserPromptSubmit hook's standard output into the model's
// context, and a Stop hook exiting 2 blocks the agent. A hook that printed or
// failed would therefore change the conversation it exists to watch, and the
// failure would look like the model behaving strangely rather than like a bug
// in Feat.
func TestGeneratedHooksObserveWithoutChangingTheSession(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("no POSIX shell available: %v", err)
	}
	_, workspace := prepared(t, false)

	payload := `{"session_id":"abc","transcript_path":"/tmp/t.jsonl","cwd":"/srv/api",` +
		`"hook_event_name":"Stop","stop_hook_active":false}`

	for _, script := range []string{
		"session-start.sh", "user-prompt-submit.sh", "stop.sh",
		"stop-failure.sh", "notification.sh", "session-end.sh",
	} {
		t.Run(script, func(t *testing.T) {
			path := filepath.Join(workspace.AgentDir(), "hooks", script)

			// The generated hook writes into the agent's view of the outbox,
			// which is /feat here. A real launch mounts it there; this test
			// rewrites the one path so the script can run on the host.
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", script, err)
			}
			local := strings.ReplaceAll(string(source), "'/feat/outbox'", "'"+workspace.OutboxDir()+"'")
			runnable := filepath.Join(t.TempDir(), script)
			if err := os.WriteFile(runnable, []byte(local), 0o700); err != nil {
				t.Fatalf("writing the runnable hook: %v", err)
			}

			command := exec.Command("/bin/sh", runnable)
			command.Stdin = strings.NewReader(payload)
			var stdout, stderr strings.Builder
			command.Stdout = &stdout
			command.Stderr = &stderr

			if err := command.Run(); err != nil {
				t.Fatalf("the hook exited non-zero: %v (stderr: %s)", err, stderr.String())
			}
			if stdout.String() != "" {
				t.Errorf("the hook wrote %q to standard output; Claude injects that into the model's context",
					stdout.String())
			}
		})
	}

	// Having run every hook, the outbox holds one well-formed message per hook
	// and no leftover staging file.
	entries, err := os.ReadDir(workspace.OutboxDir())
	if err != nil {
		t.Fatalf("reading the outbox: %v", err)
	}
	var messages int
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Errorf("the outbox holds a leftover staging file %q", entry.Name())
			continue
		}
		messages++
	}
	if messages != 6 {
		t.Errorf("the hooks wrote %d messages, want 6", messages)
	}

	pending, rejected, err := workspace.Pending()
	if err != nil {
		t.Fatalf("reading pending messages: %v", err)
	}
	if len(rejected) != 0 {
		t.Errorf("the generated hooks produced messages the protocol refused: %v", rejected)
	}
	if len(pending) != 6 {
		t.Fatalf("the protocol accepted %d of 6 generated messages", len(pending))
	}

	// The whole point: what the hooks wrote parses back into the events the
	// daemon acts on.
	kinds := make(map[agent.EventKind]int)
	for _, message := range pending {
		event, ok, err := claude.New().ParseEvent(context.Background(), message)
		if err != nil {
			t.Fatalf("parsing a generated message: %v", err)
		}
		if !ok {
			t.Errorf("a generated message produced no event: %s", message.File())
			continue
		}
		kinds[event.Kind]++
		if event.ProviderSessionID != "abc" {
			t.Errorf("event %s lost the provider session id", event.Kind)
		}
	}
	for _, kind := range []agent.EventKind{
		agent.KindSessionStarted, agent.KindPromptSubmitted, agent.KindTurnEnded,
		agent.KindSessionFailed, agent.KindNotification, agent.KindSessionEnded,
	} {
		if kinds[kind] != 1 {
			t.Errorf("kind %s was produced %d times, want once", kind, kinds[kind])
		}
	}
}

func TestReportHelperWritesAWellFormedMessage(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("no POSIX shell available: %v", err)
	}
	_, workspace := prepared(t, false)

	source, err := os.ReadFile(filepath.Join(workspace.AgentDir(), "bin", "feat-report"))
	if err != nil {
		t.Fatalf("reading the report helper: %v", err)
	}
	local := strings.ReplaceAll(string(source), "'/feat/outbox'", "'"+workspace.OutboxDir()+"'")
	helper := filepath.Join(t.TempDir(), "feat-report")
	if err := os.WriteFile(helper, []byte(local), 0o700); err != nil {
		t.Fatalf("writing the runnable helper: %v", err)
	}

	run := func(t *testing.T, body string, args ...string) error {
		t.Helper()
		command := exec.Command("/bin/sh", append([]string{helper}, args...)...)
		command.Stdin = strings.NewReader(body)
		command.Stdout = &strings.Builder{}
		command.Stderr = &strings.Builder{}
		return command.Run()
	}

	// Unlike a hook, the helper is run deliberately by the agent, so misuse must
	// fail loudly: the agent is the one who needs to know its review request did
	// not land.
	if err := run(t, "{}", "delete_everything"); err == nil {
		t.Error("the helper accepted an unknown message type")
	}
	if err := run(t, "{}"); err == nil {
		t.Error("the helper accepted a missing message type")
	}

	body := `{"summary":"Added the endpoint and a test.","checks":[{"id":"test","status":"passed","detail":"84 passed"}]}`
	if err := run(t, body, "review_requested"); err != nil {
		t.Fatalf("the helper failed on a valid request: %v", err)
	}

	pending, rejected, err := workspace.Pending()
	if err != nil {
		t.Fatalf("reading pending messages: %v", err)
	}
	if len(rejected) != 0 {
		t.Fatalf("the helper produced a message the protocol refused: %v", rejected)
	}
	if len(pending) != 1 {
		t.Fatalf("pending messages = %d, want 1", len(pending))
	}
	if pending[0].Type != control.TypeReviewRequested {
		t.Errorf("message type = %q, want %q", pending[0].Type, control.TypeReviewRequested)
	}

	event, ok, err := claude.New().ParseEvent(context.Background(), pending[0])
	if err != nil || !ok {
		t.Fatalf("parsing the review request: %v, ok=%v", err, ok)
	}
	if event.Kind != agent.KindReviewRequested {
		t.Errorf("kind = %q, want %q", event.Kind, agent.KindReviewRequested)
	}
	if len(event.Checks) != 1 || event.Checks[0].ID != "test" || event.Checks[0].Status != domain.CheckPassed {
		t.Fatalf("checks = %+v, want one passed check named test", event.Checks)
	}
	if event.Checks[0].Reporter != domain.ReporterAgent {
		t.Errorf("check reporter = %q, want %q: a claimed result is not an enforced one",
			event.Checks[0].Reporter, domain.ReporterAgent)
	}
}

func TestSessionStartFromAResumeIsNotANewSession(t *testing.T) {
	for _, test := range []struct {
		source    string
		continued bool
	}{
		{"startup", false},
		{"resume", true},
		{"clear", true},
		{"compact", true},
	} {
		t.Run(test.source, func(t *testing.T) {
			event := parse(t, "SessionStart", `{"session_id":"s","source":"`+test.source+`"}`)
			if event.Kind != agent.KindSessionStarted {
				t.Fatalf("kind = %q, want %q", event.Kind, agent.KindSessionStarted)
			}
			// A user typing /clear must not re-run the transition that says the
			// agent has begun the task.
			if event.Continued != test.continued {
				t.Errorf("continued = %v for source %q, want %v", event.Continued, test.source, test.continued)
			}
		})
	}
}

func TestEventsCarryStateRatherThanTranscript(t *testing.T) {
	secret := "the user's private prompt text"
	event := parse(t, "UserPromptSubmit", `{"session_id":"s","prompt":"`+secret+`"}`)
	if strings.Contains(event.Summary, secret) {
		t.Errorf("summary = %q, want no prompt text: events reach the event stream and the task history", event.Summary)
	}

	event = parse(t, "Stop", `{"session_id":"s","last_assistant_message":"`+secret+`"}`)
	if strings.Contains(event.Summary, secret) {
		t.Errorf("summary = %q, want no assistant message", event.Summary)
	}
	if event.Kind != agent.KindTurnEnded {
		t.Errorf("kind = %q, want %q: a Stop is the end of a turn and nothing more", event.Kind, agent.KindTurnEnded)
	}
}

func TestAnUnmodelledHookIsNotAnError(t *testing.T) {
	message := providerMessage(t, "SomethingClaudeAddedLater", `{"session_id":"s"}`)
	event, ok, err := claude.New().ParseEvent(context.Background(), message)
	if err != nil {
		t.Fatalf("an unmodelled hook was an error: %v", err)
	}
	if ok {
		t.Errorf("an unmodelled hook produced event %q; a provider may emit more than Feat represents", event.Kind)
	}
}

func TestAgentReportedSummariesAreBoundedAndSingleLine(t *testing.T) {
	body := `{"summary":"` + strings.Repeat("a", 400) + `\nsecond line"}`
	event := report(t, control.TypeReviewRequested, body)

	if strings.ContainsAny(event.Summary, "\r\n") {
		t.Error("a summary reached the dashboard with a newline in it")
	}
	if len(event.Summary) > 260 {
		t.Errorf("summary is %d bytes; agent-written text that reaches a terminal must be bounded", len(event.Summary))
	}
}

func TestACheckStatusIsNeverGuessed(t *testing.T) {
	// An agent that reports something Feat cannot record is told so, rather
	// than having its ambiguity turned into Feat's claim.
	body := `{"summary":"done","checks":[{"id":"test","status":"mostly passed"}]}`
	message := reportMessage(t, control.TypeReviewRequested, body)
	if _, _, err := claude.New().ParseEvent(context.Background(), message); err == nil {
		t.Error("a check with an unknown status was accepted")
	}

	body = `{"summary":"done","checks":[{"status":"passed"}]}`
	message = reportMessage(t, control.TypeReviewRequested, body)
	if _, _, err := claude.New().ParseEvent(context.Background(), message); err == nil {
		t.Error("a check with no id was accepted")
	}
}

// TestMissingRequiredProviderCLIPreventsLaunch is the adapter half of the sixth
// slice 7 acceptance criterion. The daemon half, which proves nothing was
// created, lives in internal/daemon.
func TestMissingRequiredProviderCLIPreventsLaunch(t *testing.T) {
	installed := agent.Output{Stdout: "2.1.220 (Claude Code)"}

	t.Run("absent", func(t *testing.T) {
		runner := agenttest.New().
			Answer(installed, "claude", "--version").
			Absent("glab", "auth", "status")

		err := claude.New().Validate(context.Background(), agent.Environment{
			Mode: domain.ExecutionHost, Runner: runner, GitLabCLI: agent.CapabilityRequired,
		})
		if err == nil {
			t.Fatal("launch validation passed with a required CLI absent")
		}
		for _, want := range []string{"glab", "agent.capabilities.gitlab_cli", "optional"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message = %q, want it to mention %q", err, want)
			}
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		runner := agenttest.New().
			Answer(installed, "claude", "--version").
			Fail(1, "not logged in to gitlab.com", "glab", "auth", "status")

		err := claude.New().Validate(context.Background(), agent.Environment{
			Mode: domain.ExecutionHost, Runner: runner, GitLabCLI: agent.CapabilityRequired,
		})
		if err == nil {
			t.Fatal("launch validation passed with a required CLI unauthenticated")
		}
		// The message has to name the remedy, or the user is told only that
		// something is wrong.
		for _, want := range []string{"glab auth login", "not authenticated", "not logged in to gitlab.com"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message = %q, want it to mention %q", err, want)
			}
		}
	})

	t.Run("optional is not required", func(t *testing.T) {
		runner := agenttest.New().
			Answer(installed, "claude", "--version").
			Absent("gh", "auth", "status")

		// An optional capability that is missing is `feat doctor`'s business.
		// Failing a launch over it would make "optional" mean nothing.
		if err := claude.New().Validate(context.Background(), agent.Environment{
			Mode: domain.ExecutionHost, Runner: runner, GitHubCLI: agent.CapabilityOptional,
		}); err != nil {
			t.Errorf("launch validation failed on an optional CLI: %v", err)
		}
		if runner.Ran("gh", "auth", "status") {
			t.Error("launch probed an optional CLI; only a required one blocks a launch")
		}
	})

	t.Run("a missing agent executable is named", func(t *testing.T) {
		err := claude.New().Validate(context.Background(), agent.Environment{
			Mode: domain.ExecutionHost, Runner: agenttest.New().Absent("claude", "--version"),
		})
		if err == nil {
			t.Fatal("launch validation passed with no agent executable")
		}
		if !strings.Contains(err.Error(), "claude") || !strings.Contains(err.Error(), "not installed") {
			t.Errorf("message = %q, want it to name the missing executable", err)
		}
	})
}

func TestAnUnverifiedVersionIsReportedRatherThanRefused(t *testing.T) {
	runner := agenttest.New().
		Succeed("9.0.1 (Claude Code)", "claude", "--version")

	// A newer Claude is not an outage. It is a reason to say that the hook
	// integration was verified against something else.
	if err := claude.New().Validate(context.Background(), agent.Environment{
		Mode: domain.ExecutionHost, Runner: runner,
	}); err != nil {
		t.Fatalf("launch validation refused a newer version: %v", err)
	}

	version, err := claude.New().Version(context.Background(), agent.Environment{
		Mode: domain.ExecutionHost, Runner: runner,
	})
	if err != nil {
		t.Fatalf("probing the version: %v", err)
	}
	warning := version.Unverified()
	if warning == "" {
		t.Fatal("a different major version produced no warning")
	}
	if !strings.Contains(warning, claude.Verified()) {
		t.Errorf("warning = %q, want it to name the verified version %s", warning, claude.Verified())
	}

	current := claude.ParseVersion(claude.Verified() + " (Claude Code)")
	if warning := current.Unverified(); warning != "" {
		t.Errorf("the verified version warned about itself: %s", warning)
	}
}

func TestPrepareRefusesATaskWithNoBrief(t *testing.T) {
	workspace, err := control.Open(t.TempDir(), testProject, testTask, control.Options{})
	if err != nil {
		t.Fatalf("opening a control workspace: %v", err)
	}
	if err := workspace.Create(); err != nil {
		t.Fatalf("creating the control workspace: %v", err)
	}
	task, err := domain.NewTask(testTask, testProject, "No brief",
		domain.TaskSource{Kind: domain.SourcePrompt}, time.Now())
	if err != nil {
		t.Fatalf("building a task: %v", err)
	}

	_, err = claude.New().Prepare(context.Background(), agent.PrepareRequest{
		Task:      task,
		Workspace: agent.Workspace{WorkingDirectory: "/srv/api", ControlPath: "/feat"},
		Control:   workspace,
	})
	if err == nil {
		t.Fatal("a task with no brief was prepared; the brief is what the session starts from")
	}
}

func TestTheHostBoundaryIsStatedRatherThanImplied(t *testing.T) {
	_, insideWorkspace := prepared(t, false)
	_, outsideWorkspace := prepared(t, true)

	inside := instructions(t, insideWorkspace)
	outside := instructions(t, outsideWorkspace)

	if strings.Contains(inside, "directly on the user's host") {
		t.Error("a session inside its configured boundary was told it was outside one")
	}
	if !strings.Contains(outside, "directly on the user's host") {
		t.Error("a session running outside its configured boundary was not told so")
	}
}

// TestArgumentsAreSingleLine keeps the launch passable to the terminal backend.
//
// internal/tmux refuses an argument containing a newline, because its own
// discovery parses tab- and newline-separated output. The protocol text is
// therefore a generated file rather than an argument, which is also what
// docs/06-technical-architecture.md means by generating instructions outside
// the repositories.
func TestArgumentsAreSingleLine(t *testing.T) {
	spec, workspace := prepared(t, true)

	for i, argument := range append([]string{spec.Program}, spec.Arguments...) {
		if strings.ContainsAny(argument, "\n\r\x00") {
			t.Errorf("argument %d contains a newline or NUL: %q", i, argument)
		}
	}

	// The protocol still reaches the session, as a file the agent is pointed at.
	file := flagValue(t, spec.Arguments, "--append-system-prompt-file")
	if file != "/feat/agent/instructions.md" {
		t.Errorf("--append-system-prompt-file = %q, want the generated document as the agent sees it", file)
	}
	if body := instructions(t, workspace); !strings.Contains(body, "feat-report") {
		t.Errorf("the generated instructions do not explain how to request review: %q", body)
	}
}

func instructions(t *testing.T, workspace *control.Workspace) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(workspace.AgentDir(), "instructions.md"))
	if err != nil {
		t.Fatalf("reading the generated instructions: %v", err)
	}
	return string(body)
}

// helpers

func parse(t *testing.T, hook, payload string) agent.Event {
	t.Helper()

	event, ok, err := claude.New().ParseEvent(context.Background(), providerMessage(t, hook, payload))
	if err != nil {
		t.Fatalf("parsing a %s payload: %v", hook, err)
	}
	if !ok {
		t.Fatalf("a %s payload produced no event", hook)
	}
	return event
}

func report(t *testing.T, kind control.MessageType, body string) agent.Event {
	t.Helper()

	event, ok, err := claude.New().ParseEvent(context.Background(), reportMessage(t, kind, body))
	if err != nil {
		t.Fatalf("parsing a %s message: %v", kind, err)
	}
	if !ok {
		t.Fatalf("a %s message produced no event", kind)
	}
	return event
}

// providerMessage builds the message a generated hook would have written.
func providerMessage(t *testing.T, hook, payload string) control.Message {
	t.Helper()
	return control.Message{
		SchemaVersion: control.SchemaVersion,
		ID:            "feat-test",
		TaskID:        testTask,
		Type:          control.TypeProviderEvent,
		OccurredAt:    time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
		Payload:       json.RawMessage(`{"hook":"` + hook + `","event":` + payload + `}`),
	}
}

func reportMessage(t *testing.T, kind control.MessageType, body string) control.Message {
	t.Helper()
	return control.Message{
		SchemaVersion: control.SchemaVersion,
		ID:            "feat-test",
		TaskID:        testTask,
		Type:          kind,
		OccurredAt:    time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
		Payload:       json.RawMessage(body),
	}
}

func flagValue(t *testing.T, arguments []string, flag string) string {
	t.Helper()
	for i, argument := range arguments {
		if argument == flag && i+1 < len(arguments) {
			return arguments[i+1]
		}
	}
	t.Fatalf("the launch carries no %s", flag)
	return ""
}

func keys[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	return names
}

func keysOf(m map[string]any) []string { return keys(m) }
