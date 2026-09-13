# ADR-099 — An invocation that says what the task is creates it, and the commands that read print a document when asked

Status: accepted
Recorded: 2026-09-13, with the implementation

ADR-095 scheduled this into `v0.1.1` as one item and said what it did not
decide: "Taking it up still needs an ADR of its own, which moves ADR-028's
terminal-independence principle onto task creation and decides what confirming
means without a screen." This is that decision, and it decides the printed shape
with it, because the roadmap held the two together on the grounds that a command
surface and the JSON shape it prints are one decision.

It departs from the shape ADR-095 sketched. That decision described "one command
resolves a draft and prints its plan, a second launches it by the fingerprint",
which is the shape the mechanism suggests. Evidence 3 is why the command is one
command instead.

Evidence:

1. **A script cannot create a task at all, and the mechanism that would let it
   already exists.** `feat implement` refuses without a terminal — "a task is not
   created until you confirm it" — and the refusal is right about what it
   protects. The conclusion drawn from it was too narrow: it treats the
   confirmation as a key press, while the daemon behind it already treats it as a
   value. ADR-031's plan fingerprint is carried back by `launch`, so what confirms
   a task is data the caller holds.

2. **The daemon already implements "the invocation is the confirmation", and
   nothing calls it.** `service.PrepareTask` records a selection, resolves it,
   and creates the resulting plan's Git resources in one call, confirming with
   `api.Confirmation{Fingerprint: plan.Fingerprint}`. Its own comment says it is
   the Git half of a launch "run end to end with the confirmation supplied by the
   plan that was just resolved". Ten tests in `internal/daemon/prepare_test.go`
   cover it and every reference to it in the tree is one of them. The mode is not
   new and is not untested; what never existed is a command that reaches it.

3. **Two commands would tax the verb the product is built around.** `feat
   implement` is the one command a user runs to start work, and the pair would
   have made the scripted form of it two commands neither of which is called
   `implement`, with a digest carried between them by the caller. Against that,
   what the second command buys a script is narrow: the base is pinned when the
   plan is recorded, fingerprint or no fingerprint, so between two calls a script
   fires back to back the digest defends against nothing that can happen. What it
   would buy is the chance to read the resolved plan first, and that is a
   property of *reading*, not of *confirming* — which is why it becomes a flag on
   the one command rather than a command of its own.

4. **What a caller supplies is not what gets created, so reading first has to be
   possible.** `--project` and a brief say nothing about which repositories the
   task takes, which commits they start from, or which branches and worktree
   paths it will own. FR-TASK-003 lists all of those as part of the draft a user
   confirms. A caller that wants them before they exist needs a way to ask, and a
   caller that does not should not pay for one.

5. **This repository already answers "show me what this would do" with a flag.**
   `feat project init --dry-run` and `feat skill install --dry-run` each run the
   same decision without writing, and ADR-093 states the property in its own
   words: "the words and the verdict, each readable before anything is on disk."
   A third command spelling the same idea as a separate verb would be a second
   grammar for a question this one already has an answer to.

6. **A brief composed from a ticket is not the caller's own words.** CLAUDE.md
   states the inbound rule — what the user approves is the composed brief, not
   the ticket it came from — and `docs/08-v0-scope.md` names the unattended path
   between a ticket and a merge request as an explicit v0 non-goal. ADR-070 put
   the same rule on the outbound half. A ticket is a document somebody else may
   have written which becomes an agent's instructions, and in a pipe there is
   nobody to read it.

7. **Four commands print a table a person reads and nothing else can parse.**
   `feat task list`, `feat task review`, `feat runtime status`, and `feat project
   show` each have something to say and say it only to a screen, so a user
   scripting around Feat has the socket or screen-scraping. Three of the four
   already hold a value the daemon composed and serialised: `api.Task`,
   `api.ReviewStatus`, `api.RuntimeStatus`, each already pinned by a golden file
   in `internal/api/testdata`. The shape exists; what was missing was a way to
   see it without writing a socket client.

8. **A second output model would be a second thing to keep in step.** The
   alternative to printing the daemon's answer is a trimmed per-command shape
   carrying what the table shows. It publishes less, and it costs a second task
   model, a second mapping, and a caller who cannot ask where a worktree is.
   ADR-094 recorded what this repository does about duplication that is not
   mandated by a boundary, and nothing mandates this one.

Decisions:

- **An invocation that fully specifies a task creates it, terminal or not; one
  that does not opens the preparation screen with whatever it was given.** Fully
  specified is `--project` and one of `--brief` or `--file`. The confirmation
  FR-TASK-003 requires is the invocation: a person who typed the whole task on
  one line has composed and confirmed it in one act, and there is no interval
  between a display and a key press for anything to drift across. The fingerprint
  is still carried — the command sends back the one the plan just returned — so a
  draft that moved underneath the run is refused rather than launched. Nothing
  that works today changes: `--project` alone still pre-fills the screen, which is
  what ADR-031 gave it.

- **`--tui` opens the screen anyway, and `--dry-run` prints the proposal and
  creates nothing.** `--tui` is the way back to composition once the flags are
  enough without it. `--dry-run` resolves the draft, prints what would be
  created, and discards the draft before printing, so a run that cannot discard
  one reports where it is instead of printing a plan and then failing. What it
  does not promise is that a later run starts from the same commit: a rehearsal
  and a real run each resolve separately, and a fetch between them can move a
  remote-tracking ref. A caller who needs a base pinned is describing the screen's
  job, not this one's.

- **`--ticket` never creates a task and needs a terminal.** Evidence 6. It keeps
  pre-filling the screen and refuses in a headless run, saying why. This is not a
  flag that was forgotten: the unattended path from a ticket is excluded from v0,
  and `--brief` or `--file` is how a caller who wrote the words launches them.

- **There is no `--yes` and no `--force`.** A flag that skips a confirmation
  somebody was shown is the excluded thing; an invocation that was never shown
  anything has nothing to skip. The distinction is the whole of why this is a
  decision rather than a convenience.

- **`--json` prints the document the command already holds, and it is never the
  default.** `feat task list`, `feat task review`, `feat runtime status`, `feat
  project show`, and `feat implement` take the flag; a person at a terminal is
  the default reader and the table is for them. `feat task list` wraps its array
  in an object so that every printed document is one, and carries the archived
  tasks the table hides, because room on a screen is why a table hides them and a
  document has no such limit. `feat project show` is the one command with no
  document already: it reads the configuration directory rather than the socket,
  so `api.ProjectConfiguration` describes what it prints — the mount mapping
  typed, and the rest as the resolved names and values the table shows, rather
  than a second model of the file `schema/feat-project.schema.json` describes.

- **An error does not appear in the document.** Standard output carries one
  document or nothing; the message goes to standard error and the process exits
  with the code it would have used anyway. ADR-027 gave an absent daemon its own
  exit code precisely so that a script would not have to parse output, and an
  error object on standard output would be a second surface saying what the first
  already says — one a naive parser would read as a result.

- **The printed shape is pinned by a test and promised by nothing.**
  `schema/feat-output.schema.json` describes every document, and
  `internal/api/output_schema_test.go` holds it to the Go types in both
  directions, which is the treatment ADR-028 already gives the configuration
  schema. What a caller may rely on across versions is a separate decision, which
  the public preview makes about the configuration schema; this one makes the
  shape deliberate and visible. The walker both checks share is
  `internal/schematest`, because `config-stays-testable` denies `internal/api` to
  files under `internal/config` and the technique is one technique.

- **Extends ADR-031.** That decision gave `feat implement` its `--project` flag
  and recorded that the flag "pre-fills rather than making the command headless:
  confirmation is required before anything is created, so a terminal is
  required". The first half stands and the second is narrowed here: a terminal is
  required to *compose* a task, and not to create one. Everything ADR-031 decided
  about drafts, fingerprints, and what confirming creates is unchanged and is
  what makes this possible.

Consequence: `internal/cli/implement.go` grows the headless path and the flags
that reach it; `internal/cli/json.go` holds the one flag five commands offer;
`aliasOf` in `internal/cli/root.go` carries a command's flags across to its
alias, without which `feat review --json` and `feat task review --json` would be
two commands rather than one under two names, and the golden surface test now
pins that too. `brief.Title` moves out of `internal/ui` for the reason ADR-083
put the file reading there, and `brief.ReadFrom` is the same size limit applied
to a stream. `docs/README.md`'s v0 command shape gains the flags and the rule.
Shell completion is untouched and stays hidden with the published schema in
`v0.2`.
