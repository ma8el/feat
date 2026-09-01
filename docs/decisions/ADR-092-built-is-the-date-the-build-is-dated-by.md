# ADR-092 — `built` is the date the build is dated by

Status: accepted
Recorded: 2026-09-01, with the implementation

`feat version` renders one date, labelled `built`. Four kinds of build now supply
it, and they do not all mean the same thing by it.

Evidence:

1. **Only one of the four dates itself by a clock.** `make build` links
   `date -u`, the moment the developer built it. The release configuration on
   branch `feat/71591eee-add-goreleaser-configuration` links `{{ .CommitDate }}`,
   the moment of the commit, and says why in a comment in `.goreleaser.yaml`: a
   build clock cannot be reproduced, and between a timestamp that is reproducible
   and one that is more precise about a build nobody witnessed, a published
   checksum wants the first. The fallback added with this decision reads
   `vcs.time` for a build inside a checkout and the timestamp inside a
   pseudo-version for an installed binary, both of which are the commit's.
2. **Each branch is internally consistent and neither records a contract.** The
   reasoning for the release's divergence is a comment on a branch that has not
   landed; the reasoning for the fallback's was a comment in
   `internal/version/version.go`. Two changes in flight, each defensible, and
   nothing either could be checked against. Left alone, v0.1.0 ships a field
   whose meaning has to be reconstructed from two files that never mention each
   other.
3. **The field ships in v0.1.0 either way.** ADR-090 puts a build attached to
   the tag and `go install github.com/ma8el/feat/cmd/feat@latest` inside the
   release, which are the two kinds of build whose date nobody present can
   explain afterwards. A meaning reconstructed later from two comments on two
   branches is not one a user can be told.
4. **The disagreement is bounded and small.** Where the value is a commit date it
   is exact. Where it is a build clock, the distance between the two is a
   developer's own working time on an unpublished binary. No published artifact
   reports a clock, and no case reports a date that is not a date of the source
   it was built from or the moment it was built.

Decisions:

- The field means: the date the build is dated by. Where the source can date
  itself, that is the commit; where a build clock is the only thing that saw the
  build, that is the clock. Both are statements about when the binary's contents
  came to be, and neither is a claim about the other.
- No source changes what it stamps. The release keeps `.CommitDate`, because the
  reproducible value is the one a checksum is published against; `make build`
  keeps `date -u`, because it is the one case where nothing published depends on
  it and a developer asking which of two local binaries is newer is asking about
  a clock.
- The rendered word stays `built`. Relabelling would change what every build
  reports, including the `make build` line another task depends on, to buy a
  wording that is exactly as approximate in the mixed case as the one it
  replaces.
- If the four are made to agree later, the way to do it is to align the Makefile
  to the commit date, not to move the release to a build clock. Recorded as the
  direction, not scheduled: nothing in v0 needs it.

Consequence: no build reports anything different from what it reported before
this decision. What changes is that the meaning is written down once, in the log
both branches read, rather than twice in comments that do not know about each
other.
