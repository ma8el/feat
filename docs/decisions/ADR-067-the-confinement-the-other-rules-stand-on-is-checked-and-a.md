# ADR-067 — The confinement the other rules stand on is checked, and a policy Feat cannot read is disclosed

Status: accepted
Recorded: 2026-08-22, round-2 review, batch 2's outstanding half, from the last field of
`G4-04`'s own claim

Batch 2 closed six of the seven fields `G4-04` named. `security_opt` was decoded
nowhere, so `seccomp=unconfined`, `apparmor=unconfined`, and
`systempaths=unconfined` passed a check that refuses a home-directory mount.

The distinction that decides this ADR is what those entries are. Every other rule
in `inspect.go` compares a name a project wrote — a path, a capability, a
namespace, an environment entry — and each of them is worth what its enforcement
is worth. `cap_add: [SYS_ADMIN]` is refused because `CAP_SYS_ADMIN` is `mount(2)`;
the default syscall filter is what stops a process obtaining the same capability
without asking Docker for it. `security_opt` is where that enforcement is
switched off, which is why it belongs with the rules rather than beside them —
and ADR-066's residual guarantee, the read-only control workspace holding by the
kernel's rule, is the thing it stands under.

Evidence, measured on Docker 29.5.2 and Compose v5.1.4 on 2026-08-22 rather than
reasoned about, because each of the first three decides the shape of the rule and
not only its wording:

1. **`systempaths=unconfined` is reported nowhere.** A container given it —
   through `docker run` and through Compose alike — reports
   `.HostConfig.SecurityOpt` *without* the entry, and reports
   `.HostConfig.MaskedPaths` and `.HostConfig.ReadonlyPaths` as `[]`. An
   ordinary container reports twelve masked paths and five read-only ones. The
   daemon spends the option at create time rather than recording it. So the fix
   as the finding states it — decode `SecurityOpt` — would have refused two of
   the three and stayed blind to the third, which is the one the finding calls
   the least ambiguous: unmasked `/proc/kcore` is this host's physical memory and
   a writable `/proc/sysrq-trigger` reboots it, neither needing a capability any
   rule reads or a mount any rule sees.
2. **A privileged container reports both lists as `null` and carries a
   `label=disable` nobody wrote.** Docker adds the label option itself. So the
   unmasked-paths rule must not fire for a privileged container — whose refusal
   already names `privileged: true`, the line that produced it — and `label=…`
   is not reliably a line a project can be sent to look for.
3. **Both separators reach the daemon.** Compose passes `seccomp:unconfined`
   through exactly as it passes `seccomp=unconfined`. A rule split on `=` alone
   would be a deny-list with a spelling as its bypass, which is what
   `capabilityName` exists to prevent one field over.
4. **A custom profile arrives whole.** `seccomp=./profile.json` is reported as
   `seccomp={"defaultAction":"SCMP_ACT_ALLOW"}`: the client sends the contents,
   not the path. The value of a `security_opt` entry is therefore sometimes a
   policy hundreds of lines long, and never something a message prints.
5. **A permissive profile is `unconfined` under another name.** That four-line
   profile allows every syscall. Whatever the rule refuses by name a project can
   express by file, and telling it from a stricter-than-default profile means
   interpreting a syscall list.
6. **The set of options is closed by the daemon and still open to Feat.** An
   unknown entry is refused at create time (`invalid --security-opt`), so what
   reaches a container is what Docker knows — and that grows: `writable-cgroups`
   is accepted by 29.5.2 and appears in no list this repository could have
   written before today.

Decisions:

- **Refuse what switches a layer off; report what replaces one.** The three
  `unconfined` forms are refused, because "unconfined" is Docker's own word for
  no policy and there is nothing to evaluate. A profile is reported, because
  evaluating it is the one thing these checks do not do.
- **`systempaths` is found by its effect and named by its cause.** The rule reads
  `MaskedPaths` and `ReadonlyPaths`, both empty and not privileged; the message
  states what was observed before it names `security_opt: systempaths=unconfined`
  as the line that produces it. A reader on a runtime that reports the lists
  differently is then told something true rather than sent to a line that is not
  there.
- **`label=…` is reported rather than refused, and the asymmetry with
  `apparmor=unconfined` is deliberate.** `docker-default` is loaded for every
  container and costs a project nothing to keep, so removing that line is a
  remedy anyone can act on. `label=disable` is not the same: on a host with
  SELinux enforcing, Feat generates its bind mounts with no `:z` and offers no
  configuration key for one, so that entry may be what makes Feat's *own* mounts
  work there. A refusal whose remedy the product does not offer is a refusal
  nobody can act on, and evidence 2 adds that Docker writes the entry itself.
- **`no-new-privileges` is silent.** It only tightens. A warning about a
  container doing better than the default is one people learn to skip, which is
  the reasoning ADR-066 applies to an image that merely carries `sudo`.
- **Anything else is reported.** Evidence 6 says the list Feat holds will be
  incomplete before Feat notices. An entry nobody recognised is exactly the case
  where silence would be the wrong answer, and it costs one line.
- **The evidence type carries both halves and judges neither.**
  `ObservedPrivileges` gains the options split into name and value, and the two
  path lists verbatim; `CheckPrivileges` decides. It is the split the rest of
  the report has, and it is what lets the same reading serve a refusal and a
  disclosure.
- **A value is never printed unless it is a word.** `SecurityOption.Describe`
  renders `label=disable` and renders a profile as `<a policy Feat did not
  evaluate>`. A warning whose subject is that Feat did not read a policy cannot
  be a warning that prints it, and `Endpoints` already returns names and never
  values for the same reason.
- **The fake describes a container rather than the queries the product makes.**
  `composetest` carries Docker's own masked and read-only lists verbatim and
  owns the `.HostConfig` defaults both fixtures build on, so the healthy
  container and a container granted one thing cannot disagree about the fields
  neither of them names. That is `G6-17` applied to the field this fix added.

What this does not claim: this is not an evaluation of anybody's security policy,
and a permissive custom profile passes with a warning — evidence 5 is the honest
limit of a rule that compares names. Feat does not read the host's AppArmor or
SELinux policy and does not know whether the host enforces either;
`apparmor=unconfined` is refused on macOS too, where it does nothing, because a
Compose file is read on every machine it is opened on. Nothing here is a defence
against a kernel or runtime exploit — § Container limitation still governs — and
the unmasked-paths consequences are consequences for a root process in the
container, which ADR-066 reports separately and does not refuse.

Consequence: `execution.ObservedPrivileges` carries `SecurityOptions`,
`MaskedPaths`, and `ReadOnlyPaths`; `execution.SecurityOption` is added with
`Describe`; the Compose adapter gains `ConfinementLayers`, `SystemPathsOption`,
and `UnevaluatedOptions`, and `Warnings` gains its second entry.
[05-security-model.md](05-security-model.md) § Dogfood security profile requires
the runtime's confinement to be left on, and § What "non-root" is a statement
about says what stands behind the kernel's rule it already cites.
