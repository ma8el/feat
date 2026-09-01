# ADR-066 — What a container grants and no rule refuses is said out loud

Status: accepted
Recorded: 2026-08-22, round-2 review, batch 8, from `G7-05` and the half of `G7-04` the
maintainer's acceptance does not cover

The launch inspection had two answers about a container — accepted, or refused
with a reason — and a security profile has a third kind of fact in it: something
a project is entitled to configure and the next person is not entitled to be
surprised by. Both findings below are that third kind, and both had been written
down as if they were one of the first two.

Evidence:

1. **The non-root answer is about an instant.** `Inspect` runs `id -u` as the
   configured user and `Check` refuses uid 0, which is what the requirement in
   [05-security-model.md](05-security-model.md) § Dogfood security profile says.
   `.devcontainer/Dockerfile` writes `dev ALL=(ALL) NOPASSWD:ALL`, so on the
   machine this alpha is being built on the check reads `dev`, passes, and the
   agent is uid 0 one command later. Nothing anywhere probed for a way back.
2. **What that reaches, and what it does not.** Measured against Docker 29.5.2
   rather than reasoned: `CAP_DAC_OVERRIDE` is in the default capability set, so
   root inside the container writes host-owned files through every writable bind
   mount and the ordinary uid-mismatch failure `probe.go` diagnoses stops
   applying — which is most of what the non-root requirement was written for.
   `CAP_SYS_ADMIN` is not in the default set, so the read-only control workspace
   holds. The residual guarantee of the round-1 control-workspace fix is intact,
   by the kernel's rule rather than by any rule of Feat's, and Feat refuses an
   added `CAP_SYS_ADMIN` separately (ADR-033's grant check).
3. **Real `sudo` is what decides the answer, so it is asked of real sudo.**
   `sudo -n true` exits zero under a `NOPASSWD` rule and non-zero under one that
   asks for a password; `-n` is what turns a prompt nobody can answer into a
   refusal. Both arms run against a real container in the opt-in suite, on one
   container and two sudoers files, because the rule is the only difference
   between them.
4. **A writable mount of the host's Claude configuration is not an exposure.**
   § Claude authentication described mounting `~/.claude` as something that "may
   expose global settings, approvals, and plaintext session data", which is
   accurate for a read-only mount and wrong for a writable one. `settings.json`
   holds `hooks` and `.claude.json` holds `mcpServers`: both are commands, and
   they are run on the *host*, as the user, by their own Claude Code the next
   time they open it. The same directory holds the approvals record that would
   otherwise be asked first. A user can therefore reason their way into a
   writable mount believing they accepted a disclosure risk and have accepted
   deferred host execution.

Decisions:

- **Probed, reported, not refused.** Whether an agent may become root inside its
  own container is the project's decision — a session that installs a package is
  the ordinary reason — and a refusal would be answered by whichever edit made
  the check stop looking. What Feat can do honestly is say what it found, at
  every launch, so the grant is deliberate rather than inherited from a template.
- **A list of tools rather than a check for `sudo`.** `EscalationTools` is the
  shape `ContainerClients` already has, and for the same reason: `sudo` is not
  the only binary that hands back privilege and this image is not the only
  image. Adding one is a line.
- **Presence is not a grant.** Only a tool that ran and exited zero counts. An
  image carrying `sudo` whose sudoers file grants the agent nothing is the
  requirement holding, demonstrated rather than assumed, and warning about it
  would be a warning on every image built from a distribution base — which
  teaches a user to skip the one that means something.
- **`Warnings` sits beside `Check` on the execution interface.** The report is
  evidence, `Check` is judgement, and this is disclosure; folding it into either
  would make it a refusal nobody wanted or a fact nobody reads. It is the first
  thing the launch inspection says about a container it is going ahead with.
- **It reaches the daemon log and the task's own event log.** The log belongs to
  whoever started the daemon; the question — did I mean to give the agent that? —
  belongs to whoever is running the task, so it is recorded against the task
  where the answer is durable and per task.
- **`feat doctor` asks it too, where it already reads the uid.** `feat doctor`
  is where a user asks this question deliberately, and it reported
  `agent.execution.user` as a green line meaning "uid 1000" — which is the shape
  `F6-08` records one check away, a security property stated as verified where
  the claim replaces the reader's own review. It is a separate implementation
  because ADR-064 requires one: doctor runs in the process the user is in front
  of and reaches no daemon. It shares `EscalationTools`, so the two surfaces
  cannot drift about what is asked, and the green line now says what it
  established rather than restating the uid.
- **§ Claude authentication distinguishes the two mounts, and the dogfood image
  states its own grant.** The documentation says what a writable mount is rather
  than how large a disclosure it is, and `.devcontainer/Dockerfile` says why its
  `NOPASSWD` line is there and what it costs. `G7-04` — the maintainer's own
  read-write `~/.claude` mount — stays as an accepted risk of 2026-08-19, and is
  a decision about one machine rather than about what the product tells anybody
  else. The alternative it should be revisited against, `agent.claude.config_volume`,
  already exists and is already the documented recommendation.

What this does not claim: a sudoers rule narrow enough to exclude `true` is not
found this way, and Feat never reads a sudoers file. Setuid binaries, a writable
Docker group, and a container escape are all outside it. The warning says what
was established, which is why it names the tool that answered rather than
describing the container's security posture.

Consequence: `execution.Report` carries `Escalation`, `execution.Environment`
gains `Warnings`, `planContainerAgent` logs and records what it returns, and
`checkContainerUser` asks the same question of a live container.
[05-security-model.md](05-security-model.md) gains § What "non-root" is a
statement about, under the profile whose requirement it qualifies, and rewrites
§ Claude authentication around the read-only/read-write distinction.
