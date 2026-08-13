package claude

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/ma8el/feat/internal/agent"
)

// Generated file names inside the host-only area of the control workspace.
const (
	settingsFile     = "settings.json"
	instructionsFile = "instructions.md"
	hooksDir         = "hooks"
	binDir           = "bin"
	reportHelper     = "feat-report"
)

// hookTimeout bounds one generated hook, in seconds.
//
// The hooks write a file and exit. A bound this short means a hook that somehow
// blocks holds up a turn briefly rather than indefinitely, and Claude's own
// timeout handling then reports it.
const hookTimeout = 10

// installedHooks are the Claude hook events Feat observes.
//
// The set is what docs/08-v0-scope.md asks for — starting, working, idle,
// failure, prompt submission, and notification — mapped onto the events this
// Claude version actually exposes. Tool-level hooks are deliberately absent:
// they fire many times a turn and tell Feat nothing about the task's state that
// these six do not.
var installedHooks = []string{
	hookSessionStart,
	hookUserPromptSubmit,
	hookStop,
	hookStopFailure,
	hookNotification,
	hookSessionEnd,
}

// generated is what Prepare produced, in the terms the agent will see.
type generated struct {
	// settingsPath is the generated settings file, as the agent sees it.
	settingsPath string
	// instructionsPath is the generated protocol document, as the agent sees
	// it. It is a file rather than an argument for two reasons: instructions
	// generated outside the repositories are what
	// docs/06-technical-architecture.md asks the adapter to produce, and a
	// multi-line argument cannot be passed through the terminal backend, whose
	// argument rules exist to keep its own discovery parseable (ADR-030).
	instructionsPath string
	// initialPrompt is the first user message of the session.
	initialPrompt string
}

// generate writes the settings, the hooks, and the report helper.
//
// Every generated file goes into the host-only area of the control workspace,
// so nothing is written into a repository and nothing the agent writes can
// change what the next launch installs.
func (a Adapter) generate(req agent.PrepareRequest) (generated, error) {
	// Paths in two vocabularies at once: the scripts are written to the host
	// path, and every path inside them is how the agent will see it. Confusing
	// the two would produce a session that reports into a directory nobody
	// reads.
	seen := agentPaths{control: req.Workspace.ControlPath}
	task := req.Task.ID.String()

	for _, event := range installedHooks {
		script := hookScript(event, task, seen.outbox())
		if err := req.Control.WriteAgentFile(path.Join(hooksDir, hookFileName(event)), []byte(script), true); err != nil {
			return generated{}, err
		}
	}
	if err := req.Control.WriteAgentFile(
		path.Join(binDir, reportHelper), []byte(reportScript(reportOptions{
			task:        task,
			outbox:      seen.outbox(),
			inbox:       seen.inbox(),
			gate:        req.Gate.Configured,
			acknowledge: seconds(req.Gate.Acknowledge),
			verdict:     seconds(req.Gate.Verdict),
		})), true,
	); err != nil {
		return generated{}, err
	}

	settings, err := settingsDocument(seen)
	if err != nil {
		return generated{}, err
	}
	if err := req.Control.WriteAgentFile(settingsFile, settings, false); err != nil {
		return generated{}, err
	}
	if err := req.Control.WriteAgentFile(instructionsFile, []byte(systemPrompt(req, seen)), false); err != nil {
		return generated{}, err
	}

	return generated{
		settingsPath:     seen.settings(),
		instructionsPath: seen.instructions(),
		initialPrompt:    initialPrompt(seen),
	}, nil
}

// agentPaths renders the control workspace as the agent sees it.
//
// Every path is built with the slash-separated path package rather than
// filepath, because these are the agent's paths and the agent's separator is
// not necessarily the host's.
type agentPaths struct{ control string }

func (p agentPaths) outbox() string   { return path.Join(p.control, "outbox") }
func (p agentPaths) inbox() string    { return path.Join(p.control, "inbox") }
func (p agentPaths) brief() string    { return path.Join(p.control, "task.md") }
func (p agentPaths) settings() string { return path.Join(p.control, "agent", settingsFile) }
func (p agentPaths) instructions() string {
	return path.Join(p.control, "agent", instructionsFile)
}
func (p agentPaths) report() string {
	return path.Join(p.control, "agent", binDir, reportHelper)
}
func (p agentPaths) hook(event string) string {
	return path.Join(p.control, "agent", hooksDir, hookFileName(event))
}

// settingsHook is one entry in Claude's hook configuration.
//
// The shape is Claude's, verified against the installed version: an event maps
// to a list of matcher groups, each holding a list of commands. The matcher is
// omitted, because it selects tool names and none of the installed events is a
// tool event.
type settingsHook struct {
	Hooks []hookCommand `json:"hooks"`
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// settingsDocument renders the settings file Feat adds to the user's own.
//
// It carries hooks and nothing else. A generated settings file that also set a
// model, a permission mode, or a tool policy would be Feat quietly deciding how
// somebody else's agent behaves.
func settingsDocument(seen agentPaths) ([]byte, error) {
	hooks := make(map[string][]settingsHook, len(installedHooks))
	for _, event := range installedHooks {
		hooks[event] = []settingsHook{{
			Hooks: []hookCommand{{
				Type: "command",
				// Claude runs a hook command through a shell, so the path is
				// quoted here rather than trusted to contain no space.
				Command: shellQuote(seen.hook(event)),
				Timeout: hookTimeout,
			}},
		}}
	}

	document, err := json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("rendering the generated Claude settings: %w", err)
	}
	return append(document, '\n'), nil
}

// shellQuote wraps a value so a shell reads it as one word.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// seconds renders a wait for the generated script, which counts in whole
// seconds because `sleep` is the only clock a POSIX shell has.
//
// A wait of zero would make the helper give up before it had asked, so a
// configured period shorter than a second becomes one.
func seconds(d time.Duration) int {
	if d <= time.Second {
		return 1
	}
	return int(d.Round(time.Second) / time.Second)
}

// initialPrompt is the first user message of the session.
//
// It names the brief rather than carrying it. The brief can be a quarter of a
// megabyte, and passing it here would put the whole task text into the process
// argument vector, where every user on the machine can read it in ps output.
// Naming the file also leaves the agent able to re-read it later in the session
// (ADR-032).
func initialPrompt(seen agentPaths) string {
	return "Your task brief is at " + seen.brief() + ". Read it, then begin."
}

// systemPrompt is the Feat protocol, appended to Claude's own system prompt.
//
// It is deliberately short and almost entirely about the protocol. Nothing here
// tells the agent how to do its work: the project's own CLAUDE.md does that, and
// it still applies. The one exception is the stash, which is not advice about
// the work but a fact about the environment Feat built — a task's repositories
// are linked worktrees sharing one Git directory, and a session cannot see from
// inside that its stash is somebody else's too (ADR-056).
func systemPrompt(req agent.PrepareRequest, seen agentPaths) string {
	var b strings.Builder

	fmt.Fprintf(&b, "You are running as Feat task %s in a managed session.\n\n", req.Task.Key())
	fmt.Fprintf(&b, "The task brief is at %s. It is the work you were asked to do.\n\n", seen.brief())

	b.WriteString("Feat observes this session and shows its state on a dashboard. ")
	b.WriteString("Ending a turn tells Feat only that you stopped talking; it never tells Feat that the task is done. ")
	b.WriteString("When you believe the work is ready for a human to review, say so explicitly by running:\n\n")
	fmt.Fprintf(&b, "  %s review_requested\n\n", seen.report())

	b.WriteString("It reads a JSON document on standard input:\n\n")
	b.WriteString("  {\n")
	b.WriteString("    \"summary\": \"one or two sentences on what you changed\",\n")
	b.WriteString("    \"checks\": [\n")
	b.WriteString("      {\"id\": \"test\", \"status\": \"passed\", \"detail\": \"84 passed\"}\n")
	b.WriteString("    ]\n")
	b.WriteString("  }\n\n")

	b.WriteString("Report only checks you actually ran. status is passed, failed, or skipped, ")
	b.WriteString("and Feat shows the result as your claim rather than as a verified one, ")
	b.WriteString("so an honest \"skipped\" is more useful than an optimistic \"passed\".\n\n")

	if req.Gate.Configured {
		// The agent is told what will happen to its request, because a command
		// that takes several minutes and then fails is a great deal easier to
		// act on when it was expected.
		b.WriteString("When you request review, Feat runs the project's own checks itself and the helper waits for them. ")
		if req.Gate.Describe != "" {
			b.WriteString(req.Gate.Describe + " ")
		}
		b.WriteString("If they pass, the task is ready for a human. If any of them fails, the helper exits non-zero ")
		b.WriteString("and prints what failed: fix it and request review again. ")
		b.WriteString("Feat records those results as its own, so there is no need to run them yourself first ")
		b.WriteString("and no way to report a passing check that did not pass.\n\n")
	}

	fmt.Fprintf(&b, "The same helper takes completion_report and open_question. Use %s open_question with ",
		seen.report())
	b.WriteString("{\"question\": \"...\"} when you are blocked on a decision only the user can make.\n\n")

	b.WriteString("Your repositories are Git worktrees. Each has its own working tree, index, and branch, ")
	b.WriteString("and shares one Git directory with the user's own checkout and with every other task — ")
	b.WriteString("including the stash, which Git keeps as a single stack per repository rather than per worktree. ")
	b.WriteString("Entries you cannot see are on it, and `git stash pop` takes the newest one in the repository, ")
	b.WriteString("which may be another task's work or the user's. Commit work in progress on your task branch instead. ")
	b.WriteString("Never run `git stash pop`, `git stash drop`, or `git stash clear`: they destroy an entry, ")
	b.WriteString("and the entry may not be yours. Feat turns off `rebase.autoStash` and `merge.autoStash` ")
	b.WriteString("for this session for the same reason, so a rebase with a dirty tree stops and asks ")
	b.WriteString("rather than stashing onto that shared stack.\n\n")

	if req.Environment.OutsideConfiguredBoundary {
		// The agent is told, because it is true and because a session that
		// believes it is contained may reason about risk differently from one
		// that knows it is not.
		b.WriteString("This session runs directly on the user's host machine, not in the container the project configures. ")
		b.WriteString("Treat the filesystem outside your task worktrees as the user's own.\n")
	}
	return b.String()
}
