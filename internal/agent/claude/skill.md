---
name: feat-setup
description: Help set up, edit, or troubleshoot a Feat project configuration — authoring the project YAML, adding repositories or checks, changing where the agent runs, or working out why a configuration is refused. Use when the user wants help configuring Feat or adopting it on a project. Not for working inside a task Feat has launched.
---

# Configuring Feat

Feat is configured by one YAML file per project. This skill is a method for
getting that file right, not a reference for what goes in it: the `feat`
binary on this machine is the authority on its own configuration, and every
fact you need comes from running it rather than from remembering it. If
anything here disagrees with what `feat` says, `feat` is right.

## Ask the binary, not your memory

- `feat project example` prints a complete, commented configuration. Its
  comments say where the file belongs, what the minimal file is, and what each
  part is for. Start every hand-written configuration from it.
- `feat project schema` prints the JSON Schema the configuration is held to.
  Read it for what a field may hold.
- `feat project show` prints what Feat resolved from a configuration it
  accepted, which is the thing to read when a file loads but behaves
  unexpectedly.
- Every command explains itself. When you need a flag or a detail, read the
  command's own help rather than guessing.

## Where a terminal exists, hand creation to the wizard

`feat project init` writes a configuration by asking about the project, and it
finds out for itself what the machine can answer. It is a conversation and
refuses to run without a terminal — including in this session — so do not run
it here: tell the user to run it in their own terminal, and pick up again once
the file exists.

## Authoring or editing by hand

When there is no terminal to hand off to, or the file already exists and needs
editing:

1. Print the example and put the configuration where its comments say it
   belongs.
2. Establish facts by running things and reading what they say, never by
   assuming: whether a directory is a Git repository, what its remote and
   default branch are, which Compose files sit beside it and which services
   they define. The file should state what is true of this machine, and this
   session can check what is true of this machine.
3. Run `feat doctor` after every change and fix what it marks as an error,
   until nothing is. It validates every configured project and checks the
   host, it changes nothing, and it needs no daemon.

## What validation cannot see yet

`feat doctor` says itself which checks it skipped and why: checks that belong
inside the agent's execution environment are asked where that environment is,
and are skipped until a task is running. The two mistakes that break a
configured project surface at the first task launch, not at validation — a
check command that does not exist where the agent will run it, and a container
service Feat's guard will refuse. So before the first task, verify where the
agent will actually run: for a containerised project, confirm the configured
commands exist inside that service, and read what `feat doctor` reports about
the services rather than what their files imply.

## Finishing is a handoff

Registering a project and starting the daemon work from this session, and each
command says what to run next — follow what they print. Launching a task does
not: `feat implement` needs a terminal, because a task is not created until
the user confirms it, and it will refuse here. End by telling the user to run
`feat implement` in their own terminal — never by running it yourself.
