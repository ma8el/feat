# ADR-038 — Naming a task

Status: accepted  
Recorded: 2026-08-08

Evidence found while running the product by hand, across slices 9 and 11:

1. Every `<task>` argument took the whole 36-character identifier, and no list
   printed one. `feat task list` prints the eight-character key, so does the
   dashboard, and so does every desktop notification slice 10 added. The
   identifier appeared in exactly one place, the dashboard's task detail. The
   identifier a user could see was therefore the one no command accepted, and a
   user reading `feat attach <task>` in the documentation had nowhere to get the
   argument from. Found by running `feat runtime status` with the key the task
   list had just printed.
2. The rejection made it worse rather than better. It explained the format of an
   identifier — "must be a version 4 UUID in canonical lowercase form" — to
   somebody who had no way of seeing one. A message that describes a format is
   only useful to a user who can produce a value in it.
3. It is one defect on the whole command surface rather than one command's:
   `attach`, `review`, every `runtime` action, and `cleanup` all had it, and it
   had been there since slices 5 and 6. Thirteen endpoints read a task
   identifier out of a path, through one helper.
4. A key is unique within a project and not across the machine (ADR-026 resolves
   a collision by generating another task identifier, per project). Two projects
   can therefore hold tasks whose keys share a prefix, and one of the commands
   that takes a task is `feat cleanup`.
5. Storage addresses a task by project and task together, and the daemon already
   resolves the owning project for a caller that holds only an identifier
   (ADR-027). Resolving a key is the same kind of question asked one step
   earlier.

Decisions:

- A task is named by a `domain.TaskRef`: its short key, its whole identifier, or
  any prefix of that identifier. Case is folded, because an identifier copied out
  of somewhere that upper-cased it is still that identifier.
- A reference is a prefix of an identifier and nothing else — lowercase
  hexadecimal with a `-` where the canonical layout has one. It is deliberately
  not a search over titles or branches: a title that became a way of addressing a
  task would make what a command acts on depend on text a user typed for another
  purpose.
- Ambiguity is reported with every candidate, named as the key and project the
  lists print, and never resolved to one of them. This is the rule ADR-029 set
  for a colliding branch name, applied where `feat cleanup` can reach.
- Every task is a candidate, archived ones included, so that a cancelled draft
  can still be cleaned up. An archived task can therefore make a reference
  ambiguous; preferring the live one would be the guess this rule refuses.
- Resolution lives in the daemon, beside the project resolution ADR-027 put
  there, and the matching rule itself is a pure function in `internal/domain`.
  The API resolves at its single task-identifier helper, so no endpoint can be
  added that misses it. A whole identifier is used as it stands rather than
  resolved, which is the path the dashboard takes on every request it makes.
- Both refusals name where a valid value is printed. ADR-027 mapped a domain
  validation error to `400` and a missing record to `404`; ambiguity is a `400`
  through the existing `domain.ErrInvalid` class, so no new error code enters the
  published surface.
- The validation this replaces was justified by keeping a malformed value out of
  a path join. Resolution is stronger: what a handler receives is an identifier
  the daemon read out of storage, so no value from a request reaches a path at
  all.

Consequence: `docs/06-technical-architecture.md`, `docs/README.md`, and the
README were updated in the same change, and the help of every command that takes
a task now says what the argument is. The command surface is unchanged, so its
golden file is unchanged. This amends ADR-027's decision that "the local API
addresses a task by task identifier"; the addressing boundary is where it was,
and only what counts as naming a task moves.
