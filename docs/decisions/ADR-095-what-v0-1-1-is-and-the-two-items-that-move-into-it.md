# ADR-095 — What v0.1.1 is, and the two items that move into it

Status: accepted
Recorded: 2026-09-12, before the work it schedules

`v0.1.0` was tagged on 2026-09-03 and packages the finished dogfood scope for its
author (ADR-090). The next milestone is the public preview, and most of what it
is made of is documentation, packaging, platforms, and a promise — written
against runs on a machine that has never seen Feat.

`v0.1.1` is the part of that milestone that changes what Feat **does**, released
ahead of it so that it can be installed and used on real work before the
documentation is written against it. It carries three bug fixes measured
from the daemon's own event log, one diagnostic the `v0.1.0` acceptance run asked
for, and two items [09-roadmap.md](../09-roadmap.md) scheduled inside the public
preview until this decision. Moving those two is what this decision records; the
rest is already inside the milestone it ships from.

Evidence:

1. **The preview's documentation is written against runs, and a run needs an
   installed build.** `docs/08-v0-scope.md` puts reproducing a setup from
   documentation in the definition of done for public v0, and Phase 1 says the
   first-task documentation is best written against what the runs turn out to
   need. Two things follow. A run against a build whose known defects are still
   in it documents the wrong product. And the runs worth writing against are on
   an installed build being used for real work, rather than in a development
   checkout of Feat itself — which is what a release produces and a merge does
   not. The `v0.1.0` acceptance run of 2026-09-05 is the first of those, and part
   of what this release carries is what it found.
2. **The two-phase `implement` and machine-readable output are one item, and
   this milestone is not what finalizes their shape.** The roadmap bullet held
   them together on its own argument: the work "publishes a command surface and a
   JSON shape together, and building it first would fix the shape before the
   decision that finalizes it". That argument is about the two halves of the item
   and not about anything else Phase 1 supplies. The compatibility surface Phase 1
   does finalize is `schema/feat-project.schema.json`, which describes
   configuration a user writes rather than output Feat prints, and which keeps its
   bullet there. What holds the printed shape to the Go types is a test in both
   directions — the technique ADR-028 already applies to the configuration
   schema — and that test travels with the commands that print it. ADR-040 first
   named the gap and put it "with slice 17's JSON Schema", which is the pairing
   this separates: the technique is shared and the milestone is not.
3. **A script cannot create a task at all, and that bites before any public
   audience exists.** `feat implement` refuses without a terminal
   (`internal/cli/implement.go:92`), while the daemon behind it already treats
   the confirmation as a value rather than a key press, through the plan
   fingerprint ADR-031 has `launch` carry back. `task list`, `task review`,
   `runtime status`, and `project show` print a table a person reads and nothing
   else can parse. Both are functional gaps a user hits on a working machine, not
   properties of a public audience.
4. **Every input the wizard's second pass was waiting for is recorded.** The
   roadmap holds it last within Phase 1 because it waits on dogfood runs rather
   than on anything listed there, and ADR-072 says the same in its evidence 4 and
   its decision. The runs have happened, and all four inputs exist: the
   managed-services proposal offers every service a repository's files declare
   (`internal/wizard/wizard.go:629`, which proposes the whole of
   `w.composition.Services`); the agent's environment is answered before
   the application's, so the agent's Compose question cannot exclude the files the
   application will claim; and the acceptance run found that the wizard asks about
   neither the forge nor the tracker command, both of which had to be added by
   hand afterwards. The last two are verifiable from the source tree rather than
   only from the run: `internal/wizard` contains neither word. A condition that
   has been met is not a reason to keep waiting.
5. **The wizard has to precede the first-task documentation, not follow it.**
   Phase 1 lists both as coming last, which is an order that cannot hold: the
   documentation is written on a machine that has never run Feat, and a wizard
   that asks about neither the forge nor the tracker is a wizard whose gaps that
   documentation has to fill in with hand-edited YAML. Writing it first would
   publish the hand-editing as the path, and then invalidate it.
6. **Nothing about Linux is testable by this release.** ADR-090 declined to claim
   Linux because compilation is evidence about a compiler and support is a claim
   about a machine, and this release is deployed to macOS, so no machine it
   reaches will run a task on Linux. The notification backend is the tempting half
   to pull forward, and pulling it forward would produce exactly what ADR-090
   refused: a backend nobody has run, on a platform nobody has run a task on.
7. **A patch number is still the right name, for the reason ADR-090 recorded
   about the tag.** Go resolves `@latest` to the highest release version and falls
   back to a pre-release only when a module has no release version at all. With
   `v0.1.0` published, a `v0.2.0-rc.1` tag would be skipped by
   `go install github.com/ma8el/feat/cmd/feat@latest`, which is the install route
   ADR-090 documents and the README prints. The marking that says *unadvertised*
   is the GitHub pre-release flag, which does not have to be in the version
   string to be read.

Decisions:

- **`v0.1.1` is the functional half of the public preview, released ahead of it
  on ADR-090's terms and unchanged in every other respect.** macOS, a plain tag,
  the GitHub pre-release flag, `go install` and release archives, visible and
  unadvertised. `v0.1.x` is the internal band and `v0.2.0` is the public preview:
  this moves no milestone boundary, widens no promise, and relaxes no definition
  of done. What it changes is which side of the `v0.1.0` tag two pieces of work
  sit on, and each moves for a reason about the release rather than about the
  roadmap. The precedent is ADR-090, which moved release binaries, `go install`,
  and the setup skill into `v0.1.0`; behind it is ADR-072, which pulled
  publication and the ticket adapter ahead of the public preview. Both moved work
  to where its user appears.
- **It adds features and is not only a fix.** A patch number carrying a new
  command surface understates what it is, and evidence 7 is why it is the name
  anyway. Recording it once here is cheaper than defending it later, and it is
  the honest reading of a version that a user of `v0.1.0` will otherwise take for
  a bug-fix release.
- **The two-phase command-line `implement` and machine-readable output for the
  reading commands move to `v0.1.1`, as one item.** They travel together because
  the bullet's own argument is that a command surface and the JSON shape it
  prints are one decision; moving half would fix the shape before the decision
  that finalizes it, which is the failure the bullet was written against. What
  moves is the roadmap's shape of the work and not a licence to widen it: one
  command resolves a draft and prints its plan, a second launches it by the
  fingerprint, and that keeps FR-TASK-003 and ADR-031 literally rather than by
  analogy. It remains the opposite of the unattended path
  [08-v0-scope.md](../08-v0-scope.md) excludes, which is what a `--yes` flag
  would build instead. Taking it up still needs an ADR of its own, which moves
  ADR-028's terminal-independence principle onto task creation and decides what
  confirming means without a screen. This decision schedules the work; it does
  not design it.
- **The second pass over the onboarding wizard moves to `v0.1.1`.** Evidence 4
  removes the condition it was waiting on, and evidence 5 makes the order it was
  waiting in wrong. What it changes changes in `internal/wizard` rather than in
  one asker, because ADR-063 made the questions a package so that
  `feat project init` and the dashboard's asker could not drift; ADR-093 is where
  a question that belongs to editing an existing configuration goes instead.
- **The first-task documentation stays in the public preview.** It is the item
  the wizard has to precede, not a second thing to move: it is written against a
  machine that has never run Feat, and this release is deployed to a machine that
  has.
- **Linux stays in `v0.2` entire, including the notification backend.** The item
  is one thing end to end — run a task on it, give it notifications, ship its
  archives, then claim it — and evidence 6 is why splitting the notification
  backend off would produce a claim rather than a capability.

Consequence: `docs/09-roadmap.md`'s Phase 1 loses the two bullets and gains a
paragraph recording where they went and why, in the form that file already uses
for Shortcut and for host-native execution; its lead paragraph stops saying two
items come last within the milestone and names the one that does.
`docs/08-v0-scope.md`'s v0.2 additions list keeps its bullets, and the sentence
below it that already names what has left this list gains both departures — what
remains of *clearer project registration* there is the manual path and its
documentation. ADR-072 gains a back-reference, because its decision put the
wizard's second pass last within Phase 1 and that half of the ordering is now
narrowed to the first-task documentation, and because it once rejected
interleaving machine-readable output between publication and the tracker. That
rejection was about serving an absent public audience while the dogfood could not
finish a task; it can now, and what wants the output is somebody using an
installed build rather than a public reader. The rest of ADR-072 is untouched. The README's status block is
deliberately not rewritten: it says the v0.1 scope is complete, that Feat runs on
macOS, and that the first release is marked pre-release, all of which is still
true of `v0.1.1`, and it is the block the public preview rewrites.
