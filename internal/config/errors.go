package config

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

// ErrNotFound reports that no configuration file exists for a project.
//
// It is separate from an invalid file because the two need different actions
// from the user: one is a file to write and the other is a file to fix.
var ErrNotFound = errors.New("no project configuration")

// Problem is one rule a configuration file breaks.
type Problem struct {
	// Path locates the value in the file, in dotted YAML form such as
	// "repositories.api.host_path". It is empty for a problem about the file as
	// a whole.
	Path string
	// Reason says what is wrong and, where there is a choice to make, what the
	// accepted values are.
	Reason string
	// excerpt is a pre-rendered source excerpt for a decoding problem, which
	// the YAML decoder has already located precisely.
	excerpt string
}

// Error reports configuration Feat refuses to use.
//
// It carries every problem rather than the first one. A configuration file is
// edited by hand, and finding four mistakes one round trip at a time is four
// times the work of seeing them together.
type Error struct {
	// File is the configuration file the problems were found in.
	File string
	// Problems are the rules the file breaks, ordered by their position in the
	// file where that is known.
	Problems []Problem

	// source is the file's bytes, kept so that Annotated can show a problem in
	// place. It is not exported: configuration is the user's own text, and
	// nothing should copy it around by accident.
	source []byte
}

// Error renders the problems without source excerpts, so that the message stays
// usable in a log line, an API response, and a test assertion.
func (e *Error) Error() string {
	var out strings.Builder
	out.WriteString(e.summary())
	for _, problem := range e.Problems {
		out.WriteString("\n  ")
		if problem.Path != "" {
			out.WriteString(problem.Path)
			out.WriteString(": ")
		}
		out.WriteString(problem.Reason)
	}
	return out.String()
}

// Annotated renders the problems with an excerpt of the file around each one.
//
// It is what a terminal should print: the location of a mistake in a nested
// YAML document is most of the work of fixing it.
func (e *Error) Annotated() string {
	var out strings.Builder
	out.WriteString(e.summary())
	for _, problem := range e.Problems {
		out.WriteString("\n\n  ")
		if problem.Path != "" {
			out.WriteString(problem.Path)
			out.WriteString(": ")
		}
		out.WriteString(problem.Reason)

		if excerpt := e.excerpt(problem); excerpt != "" {
			out.WriteString("\n")
			out.WriteString(indent(excerpt, "  "))
		}
	}
	return out.String()
}

func (e *Error) summary() string {
	subject := "the project configuration"
	if e.File != "" {
		subject = e.File
	}
	if len(e.Problems) == 1 {
		return subject + " has a problem:"
	}
	return subject + " has " + plural(len(e.Problems), "problem") + ":"
}

// excerpt returns the lines of the file around a problem.
//
// A decoding problem arrives with its own excerpt, because the decoder knows
// exactly which byte it rejected. A semantic problem is located by looking its
// path up in the document, which fails harmlessly when the problem is that the
// value is not there at all.
func (e *Error) excerpt(problem Problem) string {
	if problem.excerpt != "" {
		return problem.excerpt
	}
	if problem.Path == "" || len(e.source) == 0 {
		return ""
	}
	path, err := yaml.PathString("$." + problem.Path)
	if err != nil {
		return ""
	}
	excerpt, err := path.AnnotateSource(e.source, false)
	if err != nil {
		// The value is absent, which is frequently the problem itself.
		return ""
	}
	return string(excerpt)
}

// problems collects problems during one validation pass.
type problems struct {
	list []Problem
}

// add records a problem at a path.
func (p *problems) add(path, reason string) {
	p.list = append(p.list, Problem{Path: path, Reason: reason})
}

// addf records a problem only when cond holds, which keeps the caller's
// validation rules readable as a list of rules.
func (p *problems) require(cond bool, path, reason string) {
	if !cond {
		p.add(path, reason)
	}
}

func (p *problems) any() bool { return len(p.list) > 0 }

// err returns the collected problems as an error, or nil when there are none.
func (p *problems) err(file string, source []byte) error {
	if !p.any() {
		return nil
	}
	sorted := append([]Problem(nil), p.list...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	return &Error{File: file, Problems: sorted, source: source}
}

// indent prefixes every line of a block.
func indent(block, prefix string) string {
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

// plural renders a count with its noun.
func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(count) + " " + noun + "s"
}
