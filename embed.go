// Package feat holds the repository documents the binary embeds and emits.
//
// schema/feat-project.schema.json and docs/examples/project.yaml are what a
// project configuration is written against, and both are held to the Go types
// by tests in both directions (ADR-028). A user who installed a release binary
// has the binary and none of the repository, so the binary carries both and
// `feat project schema` and `feat project example` print them (ADR-093).
//
// The package sits at the module root because go:embed resolves paths inside
// the package's own directory, and these two files stay where every reference
// to them points: a copy under internal/ would be a second document to keep in
// step, which is the drift ADR-093 refuses.
package feat

import (
	"bytes"
	_ "embed"
)

// projectSchema is schema/feat-project.schema.json, as this build shipped it.
//
//go:embed schema/feat-project.schema.json
var projectSchema []byte

// projectExample is docs/examples/project.yaml, as this build shipped it.
//
//go:embed docs/examples/project.yaml
var projectExample []byte

// ProjectSchema returns the JSON Schema a project configuration file is
// described by. It is returned as a copy, so what the build embedded is what
// every caller prints.
func ProjectSchema() []byte { return bytes.Clone(projectSchema) }

// ProjectExample returns the worked example of a project configuration file,
// as a copy for the same reason.
func ProjectExample() []byte { return bytes.Clone(projectExample) }
