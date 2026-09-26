# Security

Feat runs a coding agent against your repositories. This page states what that
agent can and cannot reach, so you can decide whether to run Feat on your own
work. The [security model](docs/05-security-model.md) holds the detail behind
each statement.

## What Feat does not protect against

### Host-native execution has no boundary

An agent launched host-native runs as you. It reaches every file, credential,
and provider login your user account has. Everything below about containers
applies only to devcontainer execution.

### A standard container is not hostile-code isolation

The devcontainer protects your machine from ordinary tool use, accidental host
interaction, and prompt-injected shell commands. The agent receives no Docker
socket, no host Docker CLI, no Feat or tmux control socket, and, by default, no
home directory. It sees only the mounts the project declares and the ones Feat
generates for the task.

The container does not protect against a deliberate kernel or container-runtime
exploit. Feat claims **no hostile-kernel isolation**, and undisclosed kernel
vulnerabilities are outside its threat model. That would need a microVM or
hardened-runtime backend, which Feat does not have.

An image that grants passwordless `sudo` lets the agent become root inside its
container. Root there ignores file permissions on every writable bind mount.
Feat reports such an image at launch and does not refuse it.

See [Container limitation](docs/05-security-model.md#container-limitation) and
[Dogfood security profile](docs/05-security-model.md#dogfood-security-profile).

### There is no network data-loss prevention

The agent has general outbound internet access. Anything it can read — source
code, a mounted environment file, a credential — it can send anywhere. Feat
claims **no network data-loss prevention** and does not allowlist destinations.

See [Network and data egress](docs/05-security-model.md#network-and-data-egress).

### Full Git access, and a provider CLI in the image, are not varied or checked

The agent has full Git access. It can change refs, branches, worktree metadata,
and configuration in the Git directory it shares with your own checkout.

For a read-write task that Git directory is writable, which amounts to code
execution on the host. An agent that writes a hook, or sets `core.fsmonitor` in
`.git/config`, gets a program of its choosing run as you, outside the container.
That program runs the next time you or Feat runs Git in that repository. No
exploit is needed; this is the supported configuration (ADR-050).

Feat makes its own GitHub and GitLab calls on the host and gives the agent no
provider token. A project can still install `gh` or `glab` in its own image and
mount its own credential. Feat does not prevent, check, or report that setup
(ADR-075). A token there can change remote repositories within its scope. With
open internet access, any prompt injection can also copy it out, including one
arriving in an issue body the agent fetched.

No Feat setting narrows the agent's Git access, and no check looks for a
provider CLI in the agent's environment. Run agents only against repositories
you are willing to treat as trusted, and keep provider credentials out of
agent images.

See [Git boundary](docs/05-security-model.md#git-boundary) and
[GitHub and GitLab capabilities](docs/05-security-model.md#github-and-gitlab-capabilities).

## Reporting a vulnerability

Report it privately through GitHub:
<https://github.com/ma8el/feat/security/advisories/new>. Do not open a public
issue.

Include the Feat version (`feat version`), the platform, the execution mode, and
the steps that reproduce the problem. The maintainer replies in the advisory.

A report that one of the limits above holds is not a vulnerability; they are
known and stated. A report that Feat breaks a claim it does make is one. For
example, an agent container that can reach a Docker socket, or a secret value
written into a log or a generated file.

## Supported versions

Feat is pre-1.0. Security fixes land on `main` and ship in the next release.
