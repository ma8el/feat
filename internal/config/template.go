package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Placeholders a template may contain.
//
// The vocabulary is closed. An unknown placeholder is rejected rather than left
// in place, because a name Feat does not expand survives into a branch name, a
// path, or a command argument, and the failure then happens somewhere with no
// idea what "{repo}" was meant to be.
const (
	// PlaceholderProjectID is the project identifier.
	PlaceholderProjectID = "project_id"
	// PlaceholderTaskID is the task's UUID.
	PlaceholderTaskID = "task_id"
	// PlaceholderTaskKey is the task's human-facing short identifier.
	PlaceholderTaskKey = "task_key"
	// PlaceholderRepositoryID is the repository identifier.
	PlaceholderRepositoryID = "repository_id"
	// PlaceholderSlug is a short slug derived from the task title.
	PlaceholderSlug = "slug"
	// PlaceholderRepositoryPath is the absolute path of a task worktree.
	PlaceholderRepositoryPath = "repository_path"
	// PlaceholderBaseCommit is the immutable base commit recorded for a task
	// repository.
	PlaceholderBaseCommit = "base_commit"
	// PlaceholderBranch is the generated task branch.
	PlaceholderBranch = "branch"
)

// placeholderPattern matches one "{name}" placeholder.
var placeholderPattern = regexp.MustCompile(`\{([^{}]*)\}`)

// Placeholder sets, by the thing they name.
var (
	// branchPlaceholders may appear in git.branch_template.
	branchPlaceholders = []string{
		PlaceholderProjectID, PlaceholderTaskID, PlaceholderTaskKey,
		PlaceholderRepositoryID, PlaceholderSlug,
	}
	// worktreePlaceholders may appear in git.worktree_root.
	worktreePlaceholders = []string{
		PlaceholderProjectID, PlaceholderTaskID, PlaceholderTaskKey, PlaceholderRepositoryID,
	}
	// runtimePlaceholders may appear in runtime.project_name_template.
	runtimePlaceholders = []string{
		PlaceholderProjectID, PlaceholderTaskID, PlaceholderTaskKey,
	}
	// commandPlaceholders may appear in a review or check command argument.
	commandPlaceholders = []string{
		PlaceholderProjectID, PlaceholderTaskID, PlaceholderTaskKey,
		PlaceholderRepositoryID, PlaceholderRepositoryPath,
		PlaceholderBaseCommit, PlaceholderBranch,
	}
	// taskScopedPlaceholders distinguish one task from another. A template
	// that names a per-task resource must contain one, or two tasks resolve to
	// the same branch, the same worktree, or the same Compose project, and an
	// action meant for one of them reaches the other.
	taskScopedPlaceholders = []string{PlaceholderTaskID, PlaceholderTaskKey}
)

// probe supplies representative values for checking what a template produces.
//
// Every value Feat substitutes is an identifier it has already validated, so
// the only text a template can contribute that is not safe is its own literal
// part. Expanding with safe values and checking the result therefore tests
// exactly what the user wrote.
type probe struct {
	projectID    string
	repositoryID string
}

// expand fills a template with the probe's values.
func (p probe) expand(template string) string {
	return placeholderPattern.ReplaceAllStringFunc(template, func(match string) string {
		switch strings.Trim(match, "{}") {
		case PlaceholderProjectID:
			return p.projectID
		case PlaceholderRepositoryID:
			return p.repositoryID
		case PlaceholderTaskID:
			return "0f8fad5b-d9cb-469f-a165-70867728950e"
		case PlaceholderTaskKey:
			return "0f8fad5b"
		case PlaceholderSlug:
			return "a-task-title"
		case PlaceholderRepositoryPath:
			return "/tmp/feat-probe/worktree"
		case PlaceholderBaseCommit:
			return strings.Repeat("a", 40)
		case PlaceholderBranch:
			return "feat/0f8fad5b-a-task-title"
		default:
			return match
		}
	})
}

// placeholders returns the placeholder names a template contains, in order of
// first appearance and without repeats.
func placeholders(template string) []string {
	var names []string
	seen := make(map[string]bool)
	for _, match := range placeholderPattern.FindAllStringSubmatch(template, -1) {
		name := match[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// checkPlaceholders reports any placeholder outside the allowed set, and any
// stray brace, which is a typo that would otherwise survive into a name.
func checkPlaceholders(found *problems, path, template string, allowed []string) {
	for _, name := range placeholders(template) {
		if contains(allowed, name) {
			continue
		}
		found.add(path, fmt.Sprintf(
			"uses the placeholder %q, which Feat does not expand: the placeholders here are %s",
			"{"+name+"}", list(allowed)))
	}

	// A brace that is not part of a placeholder means the user meant one and
	// mistyped it. Left alone it becomes a literal brace in a branch name.
	stripped := placeholderPattern.ReplaceAllString(template, "")
	if strings.ContainsAny(stripped, "{}") {
		found.add(path, fmt.Sprintf(
			"contains an unmatched %q or %q: a placeholder is written {name}", "{", "}"))
	}
}

// checkTaskScoped reports a template that would give two tasks the same name.
func checkTaskScoped(found *problems, path, template, resource string) {
	for _, name := range placeholders(template) {
		if contains(taskScopedPlaceholders, name) {
			return
		}
	}
	found.add(path, fmt.Sprintf(
		"must contain %s so that each task gets its own %s; without one, two tasks share it",
		list(taskScopedPlaceholders), resource))
}

// Git rejects these characters anywhere in a ref name, plus the space and the
// ASCII control characters, which are checked by range.
const invalidBranchCharacters = " ~^:?*[\\"

// checkBranchName reports a template that expands to a name Git will not
// accept. The rules are the ones `git check-ref-format` applies to a branch.
func checkBranchName(found *problems, path, template string, p probe) {
	name := p.expand(template)

	problem := func(reason string) {
		found.add(path, fmt.Sprintf("expands to %q, which Git rejects as a branch name: %s", name, reason))
	}

	switch {
	case name == "":
		problem("it is empty")
		return
	case strings.HasPrefix(name, "/"), strings.HasSuffix(name, "/"):
		problem("it starts or ends with \"/\"")
	case strings.Contains(name, "//"):
		problem("it contains \"//\"")
	case strings.Contains(name, ".."):
		problem("it contains \"..\"")
	case strings.HasSuffix(name, "."):
		problem("it ends with \".\"")
	case strings.HasSuffix(name, ".lock"):
		problem("it ends with \".lock\"")
	case strings.Contains(name, "@{"):
		problem("it contains \"@{\"")
	case name == "@":
		problem("it is \"@\"")
	case strings.HasPrefix(name, "-"):
		problem("it starts with \"-\", which Git reads as an option")
	}

	if index := strings.IndexAny(name, invalidBranchCharacters); index >= 0 {
		problem(fmt.Sprintf("it contains %q", string(name[index])))
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			problem("it contains a control character")
			break
		}
	}
	for _, component := range strings.Split(name, "/") {
		if strings.HasPrefix(component, ".") {
			problem(fmt.Sprintf("the path component %q starts with \".\"", component))
			break
		}
		if component == "" {
			break
		}
	}
}

// composeNamePattern is what Docker Compose accepts as a project name.
var composeNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// checkComposeName reports a template that expands to a name Compose rejects.
// The Compose project name is what keeps one task's services separate from
// another's, so an invalid one fails at the worst possible moment.
func checkComposeName(found *problems, path, template string, p probe) {
	name := p.expand(template)
	if !composeNamePattern.MatchString(name) {
		found.add(path, fmt.Sprintf(
			"expands to %q, which Docker Compose rejects as a project name: it must start with a lowercase letter or digit and contain only lowercase letters, digits, %q, and %q",
			name, "-", "_"))
	}
}

// contains reports membership in a small set.
func contains(haystack []string, needle string) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}

// list renders accepted placeholder names for an error message.
func list(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = "{" + value + "}"
	}
	sort.Strings(quoted)
	switch len(quoted) {
	case 0:
		return "none"
	case 1:
		return quoted[0]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
	}
}

// words renders accepted plain values for an error message, so that a
// rejection always says what would have been accepted instead.
func words(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = `"` + value + `"`
	}
	switch len(quoted) {
	case 0:
		return "none"
	case 1:
		return quoted[0]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
	}
}
