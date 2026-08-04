package domain

import (
	"errors"
	"strings"
	"testing"
)

// TestSafeIdentifiersRejectUnsafeValues checks the documented safe pattern for
// user-chosen identifiers. These reach file paths, branch names, and Compose
// project names, so a value that traverses a directory or that a tool would
// reinterpret must not be an identifier at all.
func TestSafeIdentifiersRejectUnsafeValues(t *testing.T) {
	valid := []string{"core", "a", "team-core", "team_core", "core2", strings.Repeat("a", 64)}
	for _, value := range valid {
		if err := ProjectID(value).Validate(); err != nil {
			t.Errorf("%q is rejected: %v", value, err)
		}
		if err := RepositoryID(value).Validate(); err != nil {
			t.Errorf("%q is rejected as a repository: %v", value, err)
		}
	}

	invalid := []string{
		"",
		".",
		"..",
		"../escape",
		"team/core",
		`team\core`,
		"Core",
		"-core",
		"_core",
		"core.state",
		"core project",
		"café",
		"core\n",
		strings.Repeat("a", 65),
	}
	for _, value := range invalid {
		err := ProjectID(value).Validate()
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%q is accepted as a project identifier", value)
		}
		var validation *ValidationError
		if errors.As(err, &validation) && validation.Field != "id" {
			t.Errorf("%q: error names the field %q", value, validation.Field)
		}
	}
}

// TestNewTaskIDIsAUniqueVersion4UUID checks the identity Feat generates for
// every task.
func TestNewTaskIDIsAUniqueVersion4UUID(t *testing.T) {
	seen := make(map[TaskID]bool, 512)
	for range 512 {
		id := NewTaskID()
		if err := id.Validate(); err != nil {
			t.Fatalf("generated %q: %v", id, err)
		}
		if seen[id] {
			t.Fatalf("generated %q twice", id)
		}
		seen[id] = true
	}
}

// TestTaskKeyIsDerivedFromTheIdentifier checks that the human-facing short
// identifier cannot drift from the identity it abbreviates.
func TestTaskKeyIsDerivedFromTheIdentifier(t *testing.T) {
	if got := testTask.Key(); got != "7f3a1c2e" {
		t.Errorf("key of %s is %q", testTask, got)
	}

	task := draftTask(t)
	if task.Key() != task.ID.Key() {
		t.Errorf("the task key %q does not abbreviate %q", task.Key(), task.ID)
	}
}

// TestTaskIDRejectsMalformedValues checks that only the generated form is
// accepted, so that a task identifier can never be a relative path.
func TestTaskIDRejectsMalformedValues(t *testing.T) {
	invalid := []string{
		"",
		"..",
		"../7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c",
		"7f3a1c2e",
		"7F3A1C2E-5B6D-4A80-9C1F-2D3E4F5A6B7C",
		"7f3a1c2e-5b6d-1a80-9c1f-2d3e4f5a6b7c", // version 1
		"7f3a1c2e-5b6d-4a80-1c1f-2d3e4f5a6b7c", // wrong variant
		"7f3a1c2e5b6d4a809c1f2d3e4f5a6b7c",
	}
	for _, value := range invalid {
		if err := TaskID(value).Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q is accepted as a task identifier", value)
		}
	}
}
