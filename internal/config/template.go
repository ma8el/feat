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

// Values are the values Feat substitutes for the placeholders of a template.
//
// A field left empty is a value this expansion does not have. Using a
// placeholder whose value is empty is an error rather than a silent gap,
// because an empty expansion is how two tasks end up sharing one branch or one
// directory.
type Values struct {
	// ProjectID is the project identifier.
	ProjectID string
	// TaskID is the task's UUID.
	TaskID string
	// TaskKey is the task's human-facing short identifier.
	TaskKey string
	// RepositoryID is the repository identifier.
	RepositoryID string
	// Slug is the short slug derived from the task title.
	Slug string
	// RepositoryPath is the absolute path of a task worktree.
	RepositoryPath string
	// BaseCommit is the immutable base commit recorded for a task repository.
	BaseCommit string
	// Branch is the generated task branch.
	Branch string
}

// value returns the substitution for one placeholder name.
func (v Values) value(name string) (string, bool) {
	switch name {
	case PlaceholderProjectID:
		return v.ProjectID, true
	case PlaceholderTaskID:
		return v.TaskID, true
	case PlaceholderTaskKey:
		return v.TaskKey, true
	case PlaceholderRepositoryID:
		return v.RepositoryID, true
	case PlaceholderSlug:
		return v.Slug, true
	case PlaceholderRepositoryPath:
		return v.RepositoryPath, true
	case PlaceholderBaseCommit:
		return v.BaseCommit, true
	case PlaceholderBranch:
		return v.Branch, true
	default:
		return "", false
	}
}

// Expand fills a template's placeholders.
//
// The vocabulary is the same closed one validation checks, and it is checked
// again here: a template reaches this function from a configuration that was
// validated, and the day the two disagree, expansion should fail rather than
// produce a name containing a literal "{repo}".
//
// Expansion is not recursive. A value that happens to contain braces is
// substituted once and never looked at again, so no expanded value can
// introduce a placeholder.
func Expand(template string, values Values) (string, error) {
	// A stray brace is a mistyped placeholder, and it is checked against the
	// template rather than against the result: a substituted value may contain
	// a brace of its own, and that value has already been decided.
	if stripped := placeholderPattern.ReplaceAllString(template, ""); strings.ContainsAny(stripped, "{}") {
		return "", fmt.Errorf("%q contains an unmatched %q or %q: a placeholder is written {name}",
			template, "{", "}")
	}

	var failure error
	expanded := placeholderPattern.ReplaceAllStringFunc(template, func(match string) string {
		name := strings.Trim(match, "{}")
		value, known := values.value(name)
		switch {
		case !known:
			if failure == nil {
				failure = fmt.Errorf("%q uses the placeholder %q, which Feat does not expand: the placeholders are %s",
					template, match, list(allPlaceholders()))
			}
		case value == "":
			if failure == nil {
				failure = fmt.Errorf("%q uses the placeholder %q, and this expansion has no value for it",
					template, match)
			}
		}
		return value
	})
	if failure != nil {
		return "", failure
	}
	return expanded, nil
}

// Uses reports whether a template contains one particular placeholder.
//
// The worktree root is the caller: a root that already names the repository
// expands to one directory per repository, and a root that does not needs the
// repository appended, or every repository of a task would share one worktree.
func Uses(template, placeholder string) bool {
	return contains(placeholders(template), placeholder)
}

// slugSeparator joins the words of a slug. It is safe in a branch name, in a
// path, and in a Compose project name, which is more than can be said for most
// characters a task title contains.
const slugSeparator = "-"

// slugLimit bounds a slug. A branch name has room for a title, but not for a
// whole paragraph pasted into one.
const slugLimit = 40

// Slug derives a short, safe slug from a task title.
//
// Everything outside the lowercase ASCII alphanumerics becomes a separator,
// runs of separators collapse, and the result is cut to slugLimit at a
// separator where possible. A title with nothing to keep — one written entirely
// in a non-Latin script, for instance — produces "task", because the branch
// template still has to expand to something, and the task key beside it is what
// makes the name unique.
func Slug(title string) string {
	var b strings.Builder
	b.Grow(len(title))

	separated := false
	for _, r := range strings.ToLower(title) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			if separated && b.Len() > 0 {
				b.WriteString(slugSeparator)
			}
			separated = false
			b.WriteRune(r)
		default:
			separated = true
		}
	}

	slug := b.String()
	if len(slug) > slugLimit {
		slug = slug[:slugLimit]
		if cut := strings.LastIndex(slug, slugSeparator); cut > 0 {
			slug = slug[:cut]
		}
	}
	if slug == "" {
		return "task"
	}
	return slug
}

// allPlaceholders returns every placeholder name, for an error message that has
// to name the vocabulary without knowing which template it came from.
func allPlaceholders() []string {
	return []string{
		PlaceholderProjectID, PlaceholderTaskID, PlaceholderTaskKey,
		PlaceholderRepositoryID, PlaceholderSlug, PlaceholderRepositoryPath,
		PlaceholderBaseCommit, PlaceholderBranch,
	}
}

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
