# Worked ticket commands

A project's `tracker.command` is a host command that prints the project's
tickets as JSON conforming to [`schema/feat-tickets.schema.json`](../../../schema/feat-tickets.schema.json).
Feat passes it no filter and reads no field it does not publish: which tickets
are yours is the command's decision, and the shape is the contract (ADR-071).

Each script here is one worked command, and beside it is the document that
command printed. The test suite validates every one of those documents with the
code `feat doctor` uses, so an example cannot drift from what Feat accepts.

| Tracker | Command | What it printed |
| --- | --- | --- |
| GitHub Issues | [`github-issues.sh`](github-issues.sh) | [`github-issues.output.json`](github-issues.output.json) |
| GitHub Projects | [`github-projects.sh`](github-projects.sh) | [`github-projects.output.json`](github-projects.output.json) |
| GitLab Issues | [`gitlab-issues.sh`](gitlab-issues.sh) | [`gitlab-issues.output.json`](gitlab-issues.output.json) |
| Shortcut | [`shortcut.sh`](shortcut.sh) | [`shortcut.output.json`](shortcut.output.json) |

## Using one

Copy the script somewhere on your path and point the project at it:

```yaml
tracker:
  kind: command
  command: ["feat-tickets"]
```

A name is looked up on the path, exactly as `git` and your editor are in the
other command sections. A script that is not on the path is named by an absolute
one: Feat expands nothing in a command, so a leading `~` reaches the program
loader as a `~`.

Then run `feat doctor`. It runs the command and validates what it printed, so a
mapping that is wrong is found there rather than when you are trying to start
work. `feat project tickets <project>` lists what it returns, and
`feat implement --ticket <reference>` composes a task brief from one.

The command is held as an argument vector, so a script is not required: the
whole pipeline can go in the configuration if you would rather keep it there.

```yaml
tracker:
  kind: command
  command: ["gh", "issue", "list", "--repo", "acme/planning", "--assignee", "@me",
            "--json", "number,title,body,url,state",
            "--jq", "map({reference: \"#\\(.number)\", title, body, url, state})"]
```

## Merging two trackers

A project whose planning is in one place and whose bug reports are in another
runs both and concatenates the result, labelling each ticket with where it came
from. Feat records that label as the task's provider, and it is what tells two
tickets apart when they use the same key.

```sh
#!/bin/sh
set -eu
{
	./github-issues.sh | jq 'map(. + {source: "github"})'
	./shortcut.sh | jq 'map(. + {source: "shortcut"})'
} | jq --slurp 'add'
```

Leave the label out where there is only one tracker: `source` is optional
because a project drawing on one has nothing to disambiguate.

## What Feat needs from a command

- **Standard output is a JSON array**, `[]` when you have no tickets. Anything
  the command writes to standard error is ignored unless it fails.
- **Every ticket carries** `reference`, `title`, `body`, `url`, and `state`, all
  strings, and nothing else except the optional `source`.
- **The reference is yours.** Feat never parses one; it matches what you type
  against what the command printed, so an issue number, a story key, or
  `group/project#5` all work.
- **The state is the tracker's own word.** Feat maps it onto no vocabulary of
  its own, so "Ready for Dev" is as good an answer as "open".
- **`source` labels which tracker a ticket came from**, for a command that
  merges two. Feat records it as the task's provider. Leave it out where there is
  only one tracker: there is nothing to disambiguate.
- **The output is bounded** at 256 KiB, because a ticket becomes a task brief and
  a brief is what the agent is told to do. Ask for a page rather than a backlog.
- **Credentials stay on the host.** The command runs as you, in your home
  directory, wherever `feat` is running. The agent's environment never receives
  a tracker token and never runs this command (ADR-070).
