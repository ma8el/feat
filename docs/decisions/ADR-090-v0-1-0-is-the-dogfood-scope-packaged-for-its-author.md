# ADR-090 — v0.1.0 is the dogfood scope, packaged for its author

Status: accepted
Recorded: 2026-09-01, with the implementation

The v0.1 dogfood milestone is complete and nothing has ever been tagged. What a
first release is therefore has to be decided rather than assumed, and it has to
be decided now: the repository is already public, so the moment an artifact
exists it is visible, and the next milestone in
[09-roadmap.md](../09-roadmap.md) is the one that makes Feat installable by
somebody who is not its author. The one advantage of having waited is that this
is decided against the finished thing rather than against a plan.

v0.1.0 packages the completed dogfood scope for the author. It widens nothing:
no capability is added, no definition of done is relaxed, and the v0.2
public-preview boundary stays exactly where the roadmap puts it. What this
decides is which side of that boundary four items sit on, and each of the four
moves for a reason about the release rather than about the roadmap.

Evidence:

1. **The packaging list is two kinds of thing under one heading.**
   `docs/08-v0-scope.md` lists GitHub release binaries, a Homebrew tap, and
   `go install` together in v0.2. A build attached to the tag and
   `go install github.com/ma8el/feat/cmd/feat@latest` are what tagging emits:
   the first is the tag's own artifact, and the second already works from the
   module path, which has been `github.com/ma8el/feat` since the repository
   existed. A tap is a second repository with a formula to keep in step, and an
   apt path is more again. Those are channels somebody maintains, and
   maintaining a channel for one user is work with no reader.
2. **The release creates the user the setup skill is for.** Feat is configured
   by a YAML file a project author writes, and the two things that say what to
   write are the schema at `schema/feat-project.schema.json` and a worked
   example — both of which are rules about a checkout. A user who installed a
   binary has the binary. The setup skill and its two emitters, `feat project
   schema` and `feat project example`, are what put those two things back
   within reach of somebody who has no clone, so the release is what makes them
   load-bearing rather than convenient. The precedent is ADR-072, which pulled
   publication and the tracker ahead of the public preview for the same shape of
   reason: work is scheduled where its user appears, not where the phase
   numbering first put it.
3. **Linux is built and tested on every commit and has never been run.** CI runs
   the suite on `ubuntu-latest` as well as `macos-latest`, on every push to
   `main` and every pull request, so the claim that it compiles and that its
   tests pass is continuously checked. Nobody has run a task on it. It also has
   no desktop notifications: `internal/notify` has a Darwin implementation and a
   fallback that reports its own absence, and idle notification is how a user
   learns a task wants them. Compilation is evidence about a compiler; support
   is a claim about a machine, and no machine has supplied it.
4. **Host-native execution was delivered in slice 7, and the documents that call
   it missing were written before it existed.** `internal/daemon/launch.go:72`
   dispatches a configuration of `mode: host` straight to `planAgent`;
   `internal/wizard/wizard.go:443` offers `host` and `devcontainer` and proposes
   `host`; `internal/config/validate.go` knows the mode and refuses the
   container-only fields under it by name; and the mode is exercised across the
   daemon, config, wizard, and project suites. The commit is 4500861, which
   added the control workspace and the Claude adapter. There is no second
   implementation behind the execution interface left to write, because host
   mode is the absence of a container environment rather than another kind of
   one: what `planContainerAgent` reaches is the implementation, and host
   execution is the branch that goes past it. The claim to the contrary is
   `internal/execution/host/doc.go`'s closing line, which dates from the project
   skeleton (524b14f) — written before the capability existed and never
   revisited — and it is repeated in `README.md`, `docs/README.md`,
   `docs/08-v0-scope.md`, and Phase 1 of `docs/09-roadmap.md`.
5. **The tag is read by the toolchain and the marking is read by a person.** Go
   resolves `@latest` to the highest release version and falls back to a
   pre-release only when a module has no release version at all, so writing the
   pre-release into the tag — `v0.1.0-alpha.1` — would make
   `go install ...@latest` find this release by that fallback today and stop
   finding it the moment any plain tag exists. An install route this decision
   documents should not depend on the module's future tag history. The
   pre-release marking has a place where the person deciding whether to install
   reads it: the release itself, and the README's status paragraph, which
   already says who this is for.

Decisions:

- **v0.1.0 is the completed dogfood scope, packaged rather than widened.** What
  the tag claims is `docs/08-v0-scope.md`'s v0.1 definition of done, on macOS,
  and it claims nothing else. No milestone boundary moves: the items below cross
  it in both directions and the boundary itself is where the roadmap has it.
- **GitHub release binaries and `go install` are v0.1; the Homebrew tap and any
  apt path stay in v0.2.** Evidence 1 is the line: a release ships what tagging
  produces, and a distribution channel waits for the audience that justifies
  maintaining it. `docs/08-v0-scope.md` places release packaging in v0.2 as a
  single item, and this decision overrides that deliberately rather than by
  omission.
- **The setup skill and its two emitters are v0.1.** `feat project schema` and
  `feat project example` print from the binary what the repository otherwise
  holds, and the skill is what walks a user from an installed binary to a
  configured project. Their own decision is recorded separately and cites this
  one for the schedule.
- **Linux is not claimed by v0.1.0.** It stays in CI unchanged, and `go install`
  will build it for whoever runs it; what the release declines to state is that
  it works there. It is claimed once it has been run rather than only built, and
  the run is the evidence — not a further compilation, and not the absence of a
  bug report.
- **The release is marked pre-release, and the tag is a plain `v0.1.0`.** The
  repository is public, so the choice is not between visible and hidden but
  between advertised and unadvertised, and pre-release is what says unadvertised
  to a reader while leaving the artifact where somebody who was told about it
  can fetch it. The tag stays plain for evidence 5.
- **Host-native execution is recorded here as delivered.** The correction
  belongs in this decision because the stale claim is what would otherwise size
  the release: it is Phase 1's largest single item, and a release list written
  against it would hold v0.1.0 back for work that is finished. The five
  documents named in evidence 4 are corrected in a change of their own, against
  this record.

Consequence: `docs/08-v0-scope.md` gains release binaries, `go install`, and the
setup skill in v0.1 and loses Linux support and host-native execution from v0.2,
`docs/09-roadmap.md`'s Phase 1 loses host-native execution as outstanding work
and keeps the tap, and `README.md`, `docs/README.md`, and
`internal/execution/host/doc.go` stop saying that host-native execution is still
to come — all in a separate change, which this decision is the record for. What
is tagged is `v0.1.0`, marked pre-release, claiming macOS and the v0.1
definition of done.
