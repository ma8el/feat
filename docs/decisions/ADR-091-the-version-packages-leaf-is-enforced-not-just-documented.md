# ADR-091 — The version package's leaf is enforced, not just documented

Status: accepted
Recorded: 2026-09-01, with the implementation

`internal/version` has said of itself, since it was written, that it "must remain
a leaf: it imports only the standard library". Nothing checked it.

Evidence:

1. **ADR-025 already decided how this class of rule is held.** It added the
   package, described it as standard-library-only, and settled that import
   boundaries are enforced with `depguard` rules in `.golangci.yml` rather than
   by convention, because rules left to review attention erode. Fifteen packages
   got a rule. This one got a sentence in a doc comment.
2. **The package just took its first imports in a year.** Giving build identity a
   fallback to `debug.ReadBuildInfo` took it from two imports to six — `fmt`,
   `regexp`, `runtime`, `runtime/debug`, `strings`, `time`, all of them standard
   library, and all of them added while the doc comment's claim was being
   strengthened rather than tested. The direction of travel is toward more: the
   next reader of this package is `feat doctor`, and the shortest path to a
   richer answer runs through `internal/paths` or `internal/config`.
3. **The invariant is load-bearing where it breaks worst.** `feat version`, the
   health screen, and `feat doctor` all read this package, and two of the three
   exist to report that something else is wrong. A version package that imported
   configuration or storage would fail for exactly the reasons it is there to
   report, and it would fail in the one place a user goes to find out why.

Decisions:

- Add a `version-stays-a-leaf` rule to `.golangci.yml` covering
  `**/internal/version/**`, in `strict` list mode allowing `$gostd` and nothing
  else. Denying a list of known-bad packages would not hold the claim the package
  makes; allowing only the standard library is the claim.
- Exempt the package's own test files. Its opt-in tests build and install a real
  binary to check what the toolchain stamps into one, which needs `os/exec` and
  `internal/integrationtest`. What ships is what may not import.
- `internal/paths` carries the same declared invariant from ADR-025 and the same
  absence of a rule. It is not changed here, because this change is about build
  identity and a lint rule is not a place to be quietly thorough. It is recorded
  so that the gap is known rather than unnoticed.

Consequence: `golangci-lint run` fails on an import that leaves the standard
library, which was verified by adding one of each kind — a Feat package and a
third-party one — and watching both be reported. This decision changes no product
behaviour and no scope boundary.
