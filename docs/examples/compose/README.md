# A worked pair of Compose files

This is a small application shaped the way per-task runtimes need it: an API
and the PostgreSQL database it keeps its data in, in one repository. Copy the
shape, not the names. The rules it follows are in
[What a project's own Compose files must provide](../../07-configuration-model.md#what-a-projects-own-compose-files-must-provide).

| File | What it is |
| --- | --- |
| [`compose.yaml`](compose.yaml) | The application as it runs anywhere. The API's image copies its source in. |
| [`compose.dev.yaml`](compose.dev.yaml) | The development overlay: mounts the source at `/app` and reloads on change. |
| [`notes.yaml`](notes.yaml) | The Feat project configuration for these two files. |

The test suite reads both Compose files with the reader `feat doctor` and the
daemon use, and loads `notes.yaml` with the same validation, so the three
cannot drift apart. Nothing here has been run against Docker by that test.

## What the configuration says

```yaml
repositories:
  notes:
    runtime:
      compose_files: [compose.yaml, compose.dev.yaml]
      container_path: /app
      services: [api]
      reachable: [api]
```

- **`compose_files` names the overlay.** `compose.yaml` alone serves whatever
  the image was built from. Mounting a worktree over a baked image changes
  nothing a running server reads, so a task's edits would never appear.
- **`container_path` is the mount's target, exactly.** `compose.dev.yaml`
  mounts `.` at `/app`. Feat mounts the task's worktree at `/app` too, and
  Compose replaces a service's mount only when the targets match. Any other
  value leaves `.` mounted, which is your ordinary checkout.
- **`services` names what runs this repository's code.** Only `api` does.
  `db` is not listed, and Compose still starts it because `api` depends on it.
  It belongs to the task's Compose project, so Feat stops and destroys it with
  the rest.
- **`reachable` names what you open from this machine.** Feat reads the
  container port, 8000, from the `ports:` entry, and publishes it on a host
  port allocated for the task from `runtime.port_range`.

## What Feat changes and what the files must get right

Feat writes a generated override that Compose merges last. For each task it:

- mounts the task's worktree at `/app` in `api`;
- points `api`'s build context at the worktree, so `feat runtime create`
  rebuilds from the task's code;
- replaces every published port: `api` gets its allocated port, and `db`'s
  `5432` is removed;
- resets every `container_name`;
- names the Compose project per task, so `db-data` is a volume per task;
- sets `FEAT_TASK_ID`, `FEAT_TASK_KEY`, `FEAT_HOST_URL_API`, and the other
  generated variables on `api`.

So the files do not need a `container_name`, and they may fix a host port:
Feat replaces both. The rest is the files' job, and each one is silent when
wrong:

- **The source arrives at one path.** Every service that runs this
  repository's code mounts it at `/app`, the path `container_path` names.
- **A published port is written plainly.** `"8000:8000"` is read. An entry
  like `"${API_PORT}:8000"` or a range like `"8000-8001:8000-8001"` is not,
  and the service is then published on no host port at all.
- **Nothing a browser uses names a fixed port.** `PUBLIC_URL` reads
  `${FEAT_HOST_URL_API:-http://localhost:8000}`, so each task's API hands out
  its own address and the file still works run by hand. The variable name is
  the service name upper-cased, with anything but a letter or digit replaced
  by `_`.
- **A service calls another by its Compose name.** `DATABASE_URL` names
  `db:5432`. Neither differs per task, and a `FEAT_HOST_URL_*` value inside a
  container is that container's own loopback.
- **No volume or network has a fixed `name:`.** A literal name is one
  resource shared by every task. Without one, Compose prefixes the name with
  the per-task project name.
- **Dependencies live outside the mount.** The image this example assumes
  installs its virtualenv at `/opt/venv`. Anything the image puts inside `/app`
  is hidden once a worktree is mounted there.
- **A service that writes into the worktree does not run as root.** On Linux a
  container writes host files as whatever user it runs as, and nothing remaps
  them, so a root process leaves root-owned files in your worktree — a
  `__pycache__` is enough — and `git worktree remove` then cannot delete them.
  Cleanup stops there. `compose.dev.yaml` sets `user:` for this reason; a service
  whose image already creates a user needs nothing. Docker Desktop on macOS maps
  ownership to you, so this costs nothing there and is invisible until Linux.
- **Every environment file used for interpolation is in
  `runtime.env_files`.** Compose's implicit `.env` beside the repository is not
  read, because Feat's Compose project directory is its own. A service's own
  `env_file:` entry still resolves against the ordinary checkout.

## Consequences worth knowing

- **Each task starts with an empty database.** `db-data` is per task, so a
  task's runtime needs its migrations and seed data run once after `feat
  runtime create`. Destroying a runtime keeps the volume.
- **`db` is not reachable from the host during a task.** Feat removed its
  port. `feat runtime status` prints the task's Compose project name, and
  `docker ps --filter label=com.docker.compose.project=<name>` finds its `db`
  container for a `docker exec -it <container> psql -U notes`.
- **Do not mount a file into `/app` that Git does not track.** A
  `./.env:/app/.env` or `/dev/null:/app/.env` entry works in the ordinary
  checkout and fails in a worktree. `feat doctor` reports it; see
  [Troubleshooting](../../../README.md#troubleshooting).

## Several repositories

A second repository brings its own Compose files under its own
`repositories.<id>.runtime`, and Feat joins them with a generated `include`.
Each repository's relative paths resolve against its own checkout, so
`build: .` in each file means that repository. Two repositories may use the
same `container_path`, because a worktree is mounted only in the services its
own repository names. [`../project.yaml`](../project.yaml) shows a two-repository
runtime.
