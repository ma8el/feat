# ADR-109 — The cask clears quarantine with `postflight_steps`

Status: accepted
Recorded: 2026-10-03, after a dry-run install printed a deprecation warning

ADR-108 clears the quarantine attribute in the cask's `postflight` block. Homebrew
has since deprecated that block, and every install now says so.

Evidence:

1. **Homebrew 7.0.7 warns on every install of the published cask.**
   `brew install --dry-run ma8el/feat/feat` prints "Calling `postflight` is
   deprecated! Use `postflight_steps` instead." and asks the user to report it to
   the tap.

2. **The legacy block is on its way out.** The Cask Cookbook says official taps
   reject Ruby flight blocks, and that they "remain available temporarily for
   third-party tap compatibility". `postflight_steps` replaces them with
   declarative steps, among them `run` with literal arguments and an `on_macos`
   guard.

3. **GoReleaser cannot write the new stanza yet.** Neither the pinned v2.17.1 nor
   v2.18.2, the latest release, knows `postflight_steps`. Its `hooks.post.install`
   still renders a `postflight do` block. The open pull request
   goreleaser/goreleaser#6873 adds `hooks.post.install_steps` and a `.StagedPath`
   template field, and is expected in v2.19.

4. **`custom_block` can carry it.** GoReleaser pastes `custom_block` at the top of
   the cask. It then applies its templates to the whole file, so Homebrew's
   install-time `{{staged_path}}` has to be escaped to survive. A snapshot render
   produced the stanza with a literal `{{staged_path}}` and no `postflight do`, and
   Homebrew loaded that cask without the warning.

5. **The same shape clears the attribute in a real install.** A comment on #6873
   reports a hand-converted GoReleaser cask, with a `run` step inside `on_macos`,
   that installs on macOS arm64, strips the quarantine attribute, and passes
   `brew style`.

Decisions:

- **`custom_block` replaces `hooks`.** The step runs `/usr/bin/xattr -dr
  com.apple.quarantine` on the staged binary inside `on_macos`, which takes over
  from the old check that `xattr` exists. `must_succeed: false` keeps the old
  behaviour: a failure to clear the attribute does not fail the install.

- **Move to `hooks.post.install_steps` once GoReleaser ships #6873.**
  `custom_block` bypasses GoReleaser's knowledge of the cask, and the stanza sits
  above `version`, out of Homebrew's stanza order. Homebrew accepts it there, but
  `brew style` would not. The move bumps `.goreleaser-version` and keeps
  `must_succeed: false`, which the pull request's example leaves out.

Consequence: what an install looks like is unchanged, minus the warning. A real
install, and with it the cleared attribute, can only be checked after the next
tag publishes the cask: `xattr -l "$(which feat)"` on a fresh install should list
no `com.apple.quarantine`.
