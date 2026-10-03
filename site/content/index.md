---
seo:
  title: Feat — a terminal control plane for parallel coding agents
  description: Feat gives each task its own Git worktrees, Claude Code session, and
    application runtime, so several agents work on one codebase at once.
---

::u-page-hero
---
ui:
  container: py-10 sm:py-14 lg:py-16 gap-12 sm:gap-y-16
  title: text-6xl sm:text-7xl lg:text-8xl tracking-tighter
  description: mx-auto max-w-xl text-base sm:text-lg text-toned
  links: justify-center
---
#top
  :::hero-grid
  :::

#title
[Wait less.]{.block} [Implement more.]{.block .bg-gradient-to-r .from-primary .to-amber-200 .bg-clip-text .text-transparent .drop-shadow-[0_0_32px_rgba(245,166,35,0.35)]}

#description
Implement, review, and run several features in parallel with Claude Code. All from your terminal.

#links
  :::u-button
  ---
  class: shadow-[0_0_24px_rgba(245,166,35,0.35)]
  size: xl
  to: /getting-started/installation
  ---
  Get started
  :::

  :::u-button
  ---
  color: neutral
  icon: i-simple-icons-github
  size: xl
  to: https://github.com/ma8el/feat
  variant: soft
  ---
  View on GitHub
  :::

#default
  :::terminal-frame
  ---
  src: /assets/demo.gif
  alt: Two Feat tasks launched from the CLI; the dashboard starts one task's runtime and curl reaches its new endpoint
  ---
  :::
::

::u-page-section
---
ui:
  container: py-20 sm:py-28 gap-12 sm:gap-16
  wrapper: text-center
  headline: mb-4 font-mono text-xs uppercase tracking-[0.25em] text-primary
  title: mx-auto max-w-2xl text-4xl sm:text-5xl lg:text-6xl tracking-tighter
  description: mx-auto max-w-xl text-toned
---
#headline
How it works

#title
One task, one environment.

#description
Feat keeps parallel work apart. Each agent gets its own branch, its own containers, and its own ports.

#body
  :::feature-grid
  ---
  features:
    - icon: i-lucide-git-branch
      title: Worktrees from a recorded base
      description: Each task gets its own branch and worktree in every repository it touches. Your own checkout stays as it was.
    - icon: i-lucide-folders
      title: Tasks that span repositories
      description: A task can read and write several repositories at once. You choose which it may edit and which it may only read.
    - icon: i-lucide-terminal
      title: The agent you already use
      description: Each task runs a native Claude Code session in tmux. Attach to it whenever you like.
    - icon: i-lucide-box
      title: The application, once per task
      description: Each task's Docker Compose services get their own project and host ports, so several tasks run the whole application side by side.
    - icon: i-lucide-shield
      title: An agent with no keys to the host
      description: Run the agent in a devcontainer with no Docker socket and no provider token. Pushing and publishing happen on the host.
    - icon: i-lucide-git-pull-request
      title: Review, then publish
      description: Review a task in your own diff tool and editor. Feat opens the merge request with gh or glab after you have read what the agent wrote.
  ---
  :::
::

::u-page-section
---
ui:
  root: bg-[radial-gradient(ellipse_at_bottom,rgba(245,166,35,0.10),transparent_60%)]
  container: py-28 sm:py-40 gap-10
  title: mx-auto max-w-xl text-4xl sm:text-5xl lg:text-6xl tracking-tighter
  wrapper: text-center
  body: mt-10 flex flex-col items-center [&_p]:my-0
---
#title
Try it out now

#body
  :::copy-command
  ---
  command: |-
    brew tap ma8el/feat
    brew install --cask feat
  ---
  :::

  [or]{class="!my-6 flex w-48 items-center gap-3 text-xs uppercase tracking-widest text-dimmed before:h-px before:flex-1 before:bg-(--ui-border) after:h-px after:flex-1 after:bg-(--ui-border)"}

  :::u-button
  ---
  class: shadow-[0_0_24px_rgba(245,166,35,0.35)]
  size: xl
  to: /getting-started/installation
  ---
  Read the installation guide
  :::
::
