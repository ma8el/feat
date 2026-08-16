package compose

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ma8el/feat/internal/runtime"
)

// Ownership labels applied to every resource of a task's runtime.
//
// They make what a task owns discoverable without reading any persistent state,
// which is how a later reconciliation resolves it and how this slice lists a
// task's networks and volumes. The kind label separates an application service
// from the agent's own container: both are Feat's, and only one of them is the
// environment the user is testing.
const (
	LabelOwner   = "dev.feat.owner"
	LabelProject = "dev.feat.project"
	LabelTask    = "dev.feat.task"
	LabelKind    = "dev.feat.kind"
	LabelSchema  = "dev.feat.schema"

	// OwnerValue marks a resource as Feat's own.
	OwnerValue = "feat"
	// KindValue marks a resource as an application runtime rather than an agent
	// execution environment.
	KindValue = "runtime"
	// SchemaValue is the version of the generated override's shape.
	SchemaValue = "1"
)

// File modes for the generated override.
//
// It lives under the state directory, is never mounted anywhere, and is read
// only by Docker Compose running as the user who owns the daemon.
const (
	overrideDirPerm  fs.FileMode = 0o700
	overrideFilePerm fs.FileMode = 0o600
)

// writeOverride renders the generated override and replaces the file
// atomically.
//
// defined is every service the project's own Compose files declare, which is a
// superset of the managed ones; see overrideDocument for what each gets.
func writeOverride(spec runtime.Spec, defined []string) error {
	document, err := overrideDocument(spec, defined)
	if err != nil {
		return err
	}
	return replaceFile(spec.OverridePath, document, "generated Compose override")
}

// replaceFile writes one generated document and replaces the file atomically.
//
// A half-written Compose document is one Docker Compose would read and refuse,
// and the moment it would read it is the moment a user asked for their
// application: the rename is what keeps a crashed write from becoming a start
// that fails for a reason nothing explains.
func replaceFile(path string, document []byte, what string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, overrideDirPerm); err != nil {
		return fmt.Errorf("creating %s for the %s: %w", dir, what, err)
	}

	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	defer func() { _ = os.Remove(temp.Name()) }()

	if err := temp.Chmod(overrideFilePerm); err != nil {
		return fmt.Errorf("setting permissions on %s: %w", temp.Name(), err)
	}
	if _, err := temp.Write(document); err != nil {
		return fmt.Errorf("writing %s: %w", temp.Name(), err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", temp.Name(), err)
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// overrideDocument renders the Compose override for one task's application.
//
// Four things about it are load-bearing rather than stylistic, and each is
// pinned by a test:
//
//   - every mount uses the long form, so a path containing a colon is a value
//     rather than a syntax error waiting to happen;
//   - every scalar is written as a JSON string, which is a valid YAML
//     double-quoted scalar, so no path, name, or generated value can turn into
//     YAML syntax;
//   - container_name is reset. It is global to the Docker daemon, so a base file
//     carrying one could be brought up for exactly one task, and one task per
//     machine is not the product;
//   - a managed service receives the worktrees of the repository whose services
//     it is among, and no others;
//   - a managed service whose image is built from a repository builds from the
//     task's worktree instead. Only the context is written, so a relative
//     `dockerfile:` beside it in the project's own file follows the new context
//     rather than being replaced by a path that no longer resolves;
//   - published ports are left exactly as the project configured them. They are
//     how the user reaches the application they are testing, and v0 allocates
//     none of its own; two tasks wanting the same host port is explained rather
//     than silently prevented (ADR-034).
//
// Two kinds of service appear in it, and the difference is what Feat was asked
// to do. A managed service — one the project names — is redirected at the task's
// worktrees and told which task it is serving. A service that only appears
// because a managed one depends on it is given the two things without which the
// task's project is not really its own: its container_name reset, and Feat's
// ownership labels. Nothing else, because the project did not ask Feat to manage
// it (ADR-034).
//
// It is generated text and never carries a value read from an environment file,
// because nothing that reads one ever reaches this function.
func overrideDocument(spec runtime.Spec, defined []string) ([]byte, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	dependencies := dependencyServices(spec.Services, defined)

	var b strings.Builder
	fmt.Fprintf(&b, "# Generated by Feat for the application runtime of task %s of project %s.\n",
		spec.Task, spec.Project)
	b.WriteString("#\n")
	b.WriteString("# Do not edit: it is rewritten every time the task's services are created or\n")
	b.WriteString("# started, and it is merged last over the generated include of the repositories\n")
	b.WriteString("# this application is composed of. Compose merges a service's volumes by\n")
	b.WriteString("# target, so each mount below replaces whatever those files mounted at that\n")
	b.WriteString("# path rather than adding a second mount beside it.\n")
	b.WriteString("#\n")
	b.WriteString("# container_name is reset because it is global to the Docker daemon: a base file\n")
	b.WriteString("# carrying one could be started for one task and no more. Published ports are\n")
	b.WriteString("# left as configured, because they are how you reach the application.\n")
	b.WriteString("#\n")
	b.WriteString("# A service that bakes its code into its image has no mount to replace, so its\n")
	b.WriteString("# build context is pointed at the task's worktree instead. Only the context is\n")
	b.WriteString("# written: a relative dockerfile beside it follows the new context.\n")

	b.WriteString("services:\n")
	for _, service := range spec.Services {
		fmt.Fprintf(&b, "  %s:\n", quote(service))
		b.WriteString("    container_name: !reset null\n")

		if build, redirected := spec.BuildFor(service); redirected {
			b.WriteString("    build:\n")
			if build.Description != "" {
				fmt.Fprintf(&b, "      # %s\n", build.Description)
			}
			fmt.Fprintf(&b, "      context: %s\n", quote(build.Context))
		}

		// The worktrees of the repository whose services these are, and no
		// others: a service that runs one repository's code has no reason to
		// hold another's, and mounting every worktree into every service would
		// make two repositories expecting their source at the same path a
		// collision rather than the ordinary arrangement it is.
		if mounts := spec.MountsFor(service); len(mounts) > 0 {
			b.WriteString("    volumes:\n")
			for _, mount := range mounts {
				if mount.Description != "" {
					fmt.Fprintf(&b, "      # %s\n", mount.Description)
				}
				fmt.Fprintf(&b, "      - type: %s\n", quote("bind"))
				fmt.Fprintf(&b, "        source: %s\n", quote(mount.Source))
				fmt.Fprintf(&b, "        target: %s\n", quote(mount.Target))
				fmt.Fprintf(&b, "        read_only: %t\n", mount.ReadOnly)
			}
		}

		if entries := spec.Entries(); len(entries) > 0 {
			b.WriteString("    environment:\n")
			for _, entry := range entries {
				fmt.Fprintf(&b, "      %s: %s\n", quote(entry[0]), quote(entry[1]))
			}
		}

		writeLabels(&b, spec)
	}

	for i, service := range dependencies {
		if i == 0 {
			b.WriteString("\n")
			b.WriteString("  # Compose starts these because a managed service depends on them. They are\n")
			b.WriteString("  # in this task's project because Feat acted, so they carry its labels and\n")
			b.WriteString("  # lose a fixed container_name like the rest; nothing else about them is\n")
			b.WriteString("  # Feat's to change.\n")
		}
		fmt.Fprintf(&b, "  %s:\n", quote(service))
		b.WriteString("    container_name: !reset null\n")
		writeLabels(&b, spec)
	}
	return []byte(b.String()), nil
}

// writeLabels renders the ownership labels of one service.
func writeLabels(b *strings.Builder, spec runtime.Spec) {
	b.WriteString("    labels:\n")
	for _, label := range [][2]string{
		{LabelOwner, OwnerValue},
		{LabelProject, spec.Project.String()},
		{LabelTask, spec.Task.String()},
		{LabelKind, KindValue},
		{LabelSchema, SchemaValue},
	} {
		fmt.Fprintf(b, "      %s: %s\n", quote(label[0]), quote(label[1]))
	}
}

// dependencyServices are the services the project defines and does not manage.
//
// They are sorted, because the order Compose lists them in is the order of the
// project's files and a generated document should be the same document every
// time. Each name reaches the document through quote and never reaches an
// argument vector, so there is nothing here a name could be mistaken for.
func dependencyServices(managed, defined []string) []string {
	named := make(map[string]bool, len(managed))
	for _, service := range managed {
		named[service] = true
	}

	var dependencies []string
	for _, service := range defined {
		if named[service] {
			continue
		}
		named[service] = true
		dependencies = append(dependencies, service)
	}
	sort.Strings(dependencies)
	return dependencies
}

// quote renders a value as a YAML double-quoted scalar.
//
// YAML 1.2 is a superset of JSON and its double-quoted scalars use JSON's
// escaping, so a JSON string is a correct YAML scalar. Doing it this way means
// no path, name, or generated value can be read as YAML syntax, and the rule is
// one function rather than a habit each call site has to remember.
func quote(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		// json.Marshal fails for a string only on invalid UTF-8, which every
		// caller's value has already been validated against.
		return `""`
	}
	return string(encoded)
}
