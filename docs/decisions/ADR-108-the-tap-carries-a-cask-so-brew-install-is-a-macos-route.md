# ADR-108 — The tap carries a cask, so `brew install` is a macOS route

Status: accepted
Recorded: 2026-09-27, while wiring the tap the public-preview milestone asks for

The milestone asked for "a formula to keep in step". GoReleaser has deprecated the
mechanism that writes one, and its replacement does not install on Linux, so the
item delivers something narrower than the sentence that scheduled it.

Evidence:

1. **`brews` is deprecated and this repository's gate refuses it.** Against the
   pinned GoReleaser v2.17.1, `goreleaser check` reports "DEPRECATED: brews should
   not be used anymore" and exits non-zero, so `make release-check` fails and
   `make release-snapshot` with it. The block still renders a formula; the gate
   still rejects the configuration.

2. **The replacement is `homebrew_casks`, and Homebrew does not install casks on
   Linux.** GoReleaser's own note gives the reason it moved: "Historically,
   GoReleaser would generate *hackyish* formulas that would install the
   pre-compiled binaries. This was the only way to do it for Linuxbrew at the time,
   but this is no longer true, and *Casks* should be used instead." Formulae carried
   Linuxbrew; casks do not.

3. **The generated cask carries `on_linux` blocks anyway.** GoReleaser writes URLs
   and checksums for the Linux archives beside the macOS ones. They are inert,
   because the tool that would read them refuses casks on that platform. Nothing to
   remove and nothing to rely on.

4. **A cask can clear the quarantine attribute and a formula could not.** The
   archives are not notarized, so macOS quarantines a downloaded binary and it will
   not run — which is why the README tells an archive user to run
   `xattr -d com.apple.quarantine feat` by hand. A `postflight` block does it, so
   this is the one install route that needs no such step.
   Amended by ADR-109: Homebrew deprecated the `postflight` block, and the cask
   now clears the attribute in `postflight_steps`.

Decisions:

- **The tap carries a cask, and `brew install ma8el/feat/feat` is a macOS route.**
  A Linux user installs with `go install` or an archive, which is what the README
  already documents. This narrows what the milestone's item 5 promised, and the
  narrowing is the tool's rather than a choice: there is no supported way to put a
  pre-built binary in a tap for Linux any more.

- **`git` and `tmux` are declared dependencies.** Feat drives both for every task,
  so an install that brought neither would hand somebody a binary its own
  `feat doctor` refuses. Claude Code is the one tool without which nothing runs and
  the one Homebrew cannot deliver, so it is in `caveats` with its setup link.

- **No completions stanza.** A cask's `bash_completion` takes a path to a file
  inside the staged archive, and these archives hold the binary, the README and the
  licence. Pointing it at `feat completion bash` would make Homebrew look for a file
  of that name and fail the install. The README says how to source them (ADR-102).

- **`skip_upload: auto`**, so a pre-release never becomes what `brew install`
  hands somebody while a stable tag exists. `v0.1.0` and `v0.1.1` were both
  pre-releases, which is exactly the case this guards.

- **Verified as far as it can be before a tag.** `goreleaser check` validates the
  configuration and a local `release --skip=publish` renders the cask, which was
  read: the right URLs, four checksums, `depends_on`, `binary`, the `postflight`,
  and the caveats. What cannot be exercised without publishing is `brew install`
  itself, because GoReleaser writes a cask only on a real release. So item 5 closes
  after the tag that item 9 cuts, and the tag is what first tests it.

Consequence: `roadmap-v-2.md` item 5 says formula and means cask, and its
done-when gains the platform. The README's install section gains a `brew` route
when `v0.2.0` exists, which is item 9's change for the same reason the Linux
archive paragraph is: saying it before the tag makes it false.
