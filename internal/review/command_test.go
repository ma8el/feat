package review

import (
	"strings"
	"testing"
)

// TestCommandsCannotEscapeTheTaskPaths is the rule that a review command may run
// only in its own task's recorded worktrees.
//
// Every case is an expansion that produced something, because a template that
// cannot expand is refused before it reaches here: what this checks is what an
// expansion may turn into. The one that matters most is the second-to-last —
// another task's worktree is an absolute path, a real directory, and a perfectly
// safe place for a command to run, and it is still the wrong one.
func TestCommandsCannotEscapeTheTaskPaths(t *testing.T) {
	const mine = "/work/feat/0f8fad5b/api"
	const yours = "/work/feat/1a2b3c4d/api"

	for _, testCase := range []struct {
		name    string
		request Request
		want    string
	}{
		{
			name: "a directory that is not one of this task's worktrees",
			request: Request{Kind: KindDiff, RepositoryID: "api",
				Vector: []string{"git", "diff"}, Directory: "/etc/ssh", Worktrees: []string{mine}},
			want: "not one of this task's worktrees",
		},
		{
			name: "another task's worktree",
			request: Request{Kind: KindDiff, RepositoryID: "api",
				Vector: []string{"git", "diff"}, Directory: yours, Worktrees: []string{mine}},
			want: "not one of this task's worktrees",
		},
		{
			name: "a shared system directory",
			request: Request{Kind: KindEditor, RepositoryID: "api",
				Vector: []string{"nvim", "/etc"}, Directory: "/etc", Worktrees: []string{"/etc"}},
			want: "shared system directory",
		},
		{
			name: "a relative directory",
			request: Request{Kind: KindStatus, RepositoryID: "api",
				Vector: []string{"git", "status"}, Directory: "api", Worktrees: []string{mine}},
			want: "not an absolute path",
		},
		{
			name: "a directory that climbs out of the worktree",
			request: Request{Kind: KindDiff, RepositoryID: "api",
				Vector: []string{"git", "diff"}, Directory: mine + "/..", Worktrees: []string{mine}},
			want: "not one of this task's worktrees",
		},
		{
			name: "a directory that climbs all the way out",
			request: Request{Kind: KindDiff, RepositoryID: "api",
				Vector: []string{"git", "diff"}, Directory: mine + "/../../../..", Worktrees: []string{mine}},
			want: "shared system directory",
		},
		{
			name: "an argument left holding a placeholder",
			request: Request{Kind: KindDiff, RepositoryID: "api",
				Vector: []string{"git", "diff", "{base_commit}"}, Directory: mine, Worktrees: []string{mine}},
			want: "unexpanded argument",
		},
		{
			name: "a program left holding a placeholder",
			request: Request{Kind: KindEditor, RepositoryID: "api",
				Vector: []string{"{editor}", "."}, Directory: mine, Worktrees: []string{mine}},
			want: "still contains a placeholder",
		},
		{
			name: "an argument carrying a newline",
			request: Request{Kind: KindStatus, RepositoryID: "api",
				Vector: []string{"git", "status\nrm -rf /"}, Directory: mine, Worktrees: []string{mine}},
			want: "NUL or a newline",
		},
		{
			name: "nothing to run",
			request: Request{Kind: KindDiff, RepositoryID: "api",
				Vector: nil, Directory: mine, Worktrees: []string{mine}},
			want: "expands to nothing to run",
		},
		{
			name: "a task with no worktree recorded at all",
			request: Request{Kind: KindDiff, RepositoryID: "api",
				Vector: []string{"git", "diff"}, Directory: mine},
			want: "not one of this task's worktrees",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command, err := New(testCase.request)
			if err == nil {
				t.Fatalf("the command was accepted and would have run %s %v in %s",
					command.Program, command.Arguments, command.Directory)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("the refusal is %q, want it to say %q", err, testCase.want)
			}
		})
	}
}

// TestAnExpandedCommandKeepsItsVector checks that a command that passes is
// carried through exactly, one element at a time.
//
// An argument that arrives holding spaces stays one argument: nothing here
// re-splits a vector, which is what keeps a commit message or a path with a
// space in it from becoming two arguments somewhere downstream.
func TestAnExpandedCommandKeepsItsVector(t *testing.T) {
	const worktree = "/work/feat/0f8fad5b/api"

	command, err := New(Request{
		Kind:         KindDiff,
		RepositoryID: "api",
		Vector:       []string{"git", "diff", strings.Repeat("a", 40), "--", "a file.go"},
		Directory:    worktree + "/",
		Worktrees:    []string{worktree},
	})
	if err != nil {
		t.Fatalf("a well-formed command was refused: %v", err)
	}

	if command.Program != "git" {
		t.Errorf("the program is %q", command.Program)
	}
	if len(command.Arguments) != 4 || command.Arguments[3] != "a file.go" {
		t.Errorf("the arguments are %q, want four with the last one intact", command.Arguments)
	}
	if command.Directory != worktree {
		t.Errorf("the directory is %q, want the cleaned worktree %q", command.Directory, worktree)
	}
}
