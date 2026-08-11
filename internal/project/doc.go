// Package project maps validated configuration onto the domain and diagnoses
// the host, backing `feat project` and `feat doctor`.
//
// Responsibilities:
//
//   - map a validated configuration to a domain project, which is identity and
//     repository topology only: the rest of the profile is resolved into an
//     immutable launch snapshot per task, so editing YAML never changes what a
//     running task is doing;
//   - check host prerequisites such as Git, tmux, and the Docker Compose CLI;
//   - check that configured repositories, remotes, branches, Compose files, and
//     services resolve;
//   - report repository and container path mappings accurately;
//   - report each provider CLI capability as disabled, optional, or required.
//
// Diagnostics run without a daemon and without a registered project, because
// docs/02-user-workflows.md §1 puts `feat doctor` before both: the user writes
// their configuration, diagnoses it, and registers it once the diagnosis is
// clean. Nothing in this package writes persistent state; registration is a
// daemon operation, and the daemon is its only writer.
//
// Two rules shape what a finding is allowed to say:
//
//   - a check this build cannot run is reported as skipped, never as passing,
//     and the reason is named. FR-PROJ-004 asks for checks inside the agent's
//     execution environment, which is this machine for a host-mode project and
//     a running container of the project for a devcontainer one; `feat doctor`
//     starts neither, so a project with no live task has nothing to look inside
//     and is told so. Whether such a check can run is a fact about the machine
//     rather than about which slice this build is (ADR-033). A diagnostic that
//     claims a check it did not run is worse than no diagnostic.
//   - secret file contents never reach a finding. Environment files are
//     examined by path and metadata only, and the sole Compose command used is
//     `config --services`, which lists service names. Plain `docker compose
//     config` renders the resolved project including values taken from those
//     files, and is never run (docs/05-security-model.md).
//
// Delivered by slice 3. See ADR-028 in
// docs/10-decisions-and-open-questions.md.
package project
