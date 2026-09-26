# Contributing

[`CLAUDE.md`](CLAUDE.md) is the working contract for every change, whoever or
whatever writes it. It lists the reading order for the specification, the scope
and architectural rules, and the definition of complete. Read it before your
first patch.

Report a security problem privately, as [`SECURITY.md`](SECURITY.md) describes,
not in a pull request or issue.

## The gate

`make check` is what CI runs, and a change is ready when it passes. It demands
real Git, Docker, and tmux, and fails rather than skips when one is missing. On
a machine without one, name the tools you have:

```sh
make check INTEGRATION_TOOLS=git,tmux
```

Say in the pull request which tools your run left out. The README's
[Development](README.md#development) section explains the tiers and the
demandable tools. [How the design rules are enforced](README.md#how-the-design-rules-are-enforced)
lists the tests and lint rules that check the architecture, so a boundary you
cross fails there rather than in review.

## Changing a decision

The specification in [`docs/`](docs/) is authoritative. When a change
contradicts an accepted decision, or settles a question the specification left
open, record it in the same pull request:

1. Write the ADR as a new file under [`docs/decisions/`](docs/decisions/),
   numbered after the last one.
2. Add its row to the index in
   [`docs/10-decisions-and-open-questions.md`](docs/10-decisions-and-open-questions.md).
3. Update every specification file the decision affects.

The [decision change process](docs/10-decisions-and-open-questions.md#decision-change-process)
is the full list. Behavior the code has drifted into does not become the
specification by being merged.

## Writing

Comments, error messages, and commit messages follow the **Writing** section of
[`CLAUDE.md`](CLAUDE.md). In short: comments say why and cite the ADR, errors fit
in one sentence under 100 characters, and a commit subject is imperative and
under 72 characters.

`CLAUDE-NOTE:` markers are review aids for a change in progress. Never commit
one. `git grep -n CLAUDE-NOTE` finds any that are left before you commit.

## License

Feat is licensed under Apache 2.0. A contribution you submit is licensed under
the same terms, as section 5 of the [LICENSE](LICENSE) provides.
