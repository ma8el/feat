package tmux

import (
	"strings"
	"testing"
)

// output builds the three list outputs a discovery pass reads.
type output struct {
	sessions []string
	windows  []string
	panes    []string
}

// healthy is a complete, consistent terminal for a task.
func healthy(session, window, pane, project, task string) output {
	return output{
		sessions: []string{join(session, "1", metadataVersion, project)},
		windows:  []string{join(session, window, "1", metadataVersion, project, task, "0")},
		panes: []string{
			join(session, window, pane, "0", "", "/work", "1", metadataVersion, project, task, roleAgent, "4242"),
		},
	}
}

func (o output) merge(other output) output {
	return output{
		sessions: append(append([]string(nil), o.sessions...), other.sessions...),
		windows:  append(append([]string(nil), o.windows...), other.windows...),
		panes:    append(append([]string(nil), o.panes...), other.panes...),
	}
}

func join(fields ...string) string { return strings.Join(fields, "\t") }

// discovered assembles one pass over the given output.
func discovered(o output) Discovery {
	sessions, damaged := parseSessions(strings.Join(o.sessions, "\n"))
	windows, windowDamage := parseWindows(strings.Join(o.windows, "\n"))
	panes, paneDamage := parsePanes(strings.Join(o.panes, "\n"))
	damaged = append(damaged, windowDamage...)
	damaged = append(damaged, paneDamage...)
	return assemble("/run/feat/tmux.sock", sessions, windows, panes, damaged)
}

const (
	goodTask = "7f3a1c2e-9b5d-4a1f-8c3e-2d4b6a8f1e0c"
	badTask  = "1a2b3c4d-5e6f-4a1b-9c8d-7e6f5a4b3c2d"
)

// TestOneDamagedObjectDoesNotHideTheHealthyOnes is the quarantine rule, stated
// as the defect it replaces.
//
// Every row is an inconsistency that previously ended discovery for the whole
// server: `EnsureTask` failed for every unrelated task, and startup
// reconciliation stopped before it reached any of them (ADR-030 evidence 9).
// Each one must now leave the healthy terminal intact.
func TestOneDamagedObjectDoesNotHideTheHealthyOnes(t *testing.T) {
	good := healthy("$1", "@1", "%1", "app", goodTask)

	for _, test := range []struct {
		name   string
		broken output
	}{
		{
			name: "a session written by a newer Feat",
			broken: output{
				sessions: []string{join("$2", "1", "99", "other")},
				windows:  []string{join("$2", "@2", "1", "99", "other", badTask, "0")},
				panes: []string{join("$2", "@2", "%2", "0", "", "/work", "1", "99",
					"other", badTask, roleAgent, "1")},
			},
		},
		{
			name: "a window whose task metadata is not an identifier",
			broken: output{
				sessions: []string{join("$2", "1", metadataVersion, "other")},
				windows:  []string{join("$2", "@2", "1", metadataVersion, "other", "not-a-task", "0")},
			},
		},
		{
			// Inside the healthy window, so this one also proves that a pane
			// Feat cannot read does not take its own task's terminal with it
			// when the terminal is otherwise complete.
			name: "a pane with an unknown role",
			broken: output{
				panes: []string{join("$1", "@1", "%9", "0", "", "/work", "1", metadataVersion,
					"app", goodTask, "sidecar", "1")},
			},
		},
		{
			name: "a pane whose dead flag tmux could not render",
			broken: output{
				sessions: []string{join("$2", "1", metadataVersion, "other")},
				windows:  []string{join("$2", "@2", "1", metadataVersion, "other", badTask, "0")},
				panes: []string{join("$2", "@2", "%2", "?", "", "/work", "1", metadataVersion,
					"other", badTask, roleAgent, "1")},
			},
		},
		{
			name: "a pane tagged for a session nothing manages",
			broken: output{
				panes: []string{join("$9", "@9", "%9", "0", "", "/work", "1", metadataVersion,
					"other", badTask, roleAgent, "1")},
			},
		},
		{
			name: "a window whose agent pane was killed while its shell survived",
			broken: output{
				sessions: []string{join("$2", "1", metadataVersion, "other")},
				windows:  []string{join("$2", "@2", "1", metadataVersion, "other", badTask, "0")},
				panes: []string{join("$2", "@2", "%2", "0", "", "/work", "1", metadataVersion,
					"other", badTask, roleShell, "1")},
			},
		},
		{
			name: "two panes claiming the agent role for one task",
			broken: output{
				sessions: []string{join("$2", "1", metadataVersion, "other")},
				windows:  []string{join("$2", "@2", "1", metadataVersion, "other", badTask, "0")},
				panes: []string{
					join("$2", "@2", "%2", "0", "", "/work", "1", metadataVersion,
						"other", badTask, roleAgent, "1"),
					join("$2", "@2", "%3", "0", "", "/work", "1", metadataVersion,
						"other", badTask, roleAgent, "2"),
				},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			found := discovered(good.merge(test.broken))

			if len(found.Damaged) == 0 {
				t.Fatal("the inconsistency was neither reported nor quarantined")
			}
			for _, damaged := range found.Damaged {
				if damaged.Reason == "" {
					t.Error("an object was quarantined without a reason")
				}
			}

			terminal, ok := found.Terminal("app", goodTask)
			if !ok {
				t.Fatalf("the healthy terminal was lost: %d terminals, damage %+v",
					len(found.Terminals), found.Damaged)
			}
			if terminal.Target.Pane != "%1" {
				t.Errorf("healthy target = %+v, want pane %%1", terminal.Target)
			}
		})
	}
}

// TestAQuarantinedTerminalIsNotReturnedAsHalfATerminal keeps the unit whole.
//
// A window whose agent pane is damaged has no agent pane, and returning it with
// a shell pane and an empty agent target would hand a caller something it would
// then have to check for itself.
func TestAQuarantinedTerminalIsNotReturnedAsHalfATerminal(t *testing.T) {
	found := discovered(output{
		sessions: []string{join("$1", "1", metadataVersion, "app")},
		windows:  []string{join("$1", "@1", "1", metadataVersion, "app", goodTask, "0")},
		panes: []string{
			join("$1", "@1", "%1", "0", "", "/work", "1", metadataVersion, "app", goodTask, roleShell, "1"),
		},
	})

	if len(found.Terminals) != 0 {
		t.Fatalf("a window with no agent pane was returned as a terminal: %+v", found.Terminals)
	}
	damage := found.DamageFor("app", goodTask)
	if len(damage) == 0 {
		t.Fatal("the incomplete window was dropped rather than reported")
	}
	if !strings.Contains(damage[0].Reason, "agent pane") {
		t.Errorf("reason = %q, want it to name what is missing", damage[0].Reason)
	}
}

// TestConflictingSessionsQuarantineOnlyTheirOwnProject bounds the damage to
// where it belongs.
//
// Two sessions claiming one project cannot both be it, so neither is trusted and
// the project is refused a third rather than given one — which would make the
// ambiguity permanent. Every other project stays usable.
func TestConflictingSessionsQuarantineOnlyTheirOwnProject(t *testing.T) {
	found := discovered(output{
		sessions: []string{
			join("$1", "1", metadataVersion, "app"),
			join("$2", "1", metadataVersion, "app"),
			join("$3", "1", metadataVersion, "other"),
		},
		windows: []string{join("$3", "@3", "1", metadataVersion, "other", goodTask, "0")},
		panes: []string{join("$3", "@3", "%3", "0", "", "/work", "1", metadataVersion,
			"other", goodTask, roleAgent, "1")},
	})

	if _, err := projectSession(found, "app"); err == nil {
		t.Error("a project with two sessions was given a third")
	}
	session, err := projectSession(found, "other")
	if err != nil {
		t.Fatalf("an unrelated project was refused because another had two sessions: %v", err)
	}
	if session != "$3" {
		t.Errorf("session = %q, want $3", session)
	}
	if _, ok := found.Terminal("other", goodTask); !ok {
		t.Error("an unrelated project's terminal was quarantined")
	}
}

// TestDiscoveryIgnoresPanesTheUserCreated keeps quarantine from turning a
// user's own pane into damage.
//
// A pane a user split inside a managed window inherits the window's options and
// has no role of its own. It is not Feat's and is not broken.
func TestDiscoveryIgnoresPanesTheUserCreated(t *testing.T) {
	found := discovered(healthy("$1", "@1", "%1", "app", goodTask).merge(output{
		panes: []string{join("$1", "@1", "%2", "0", "", "/work", "1", metadataVersion,
			"app", goodTask, "", "9")},
	}))

	if len(found.Damaged) != 0 {
		t.Errorf("a user's own pane was reported as damage: %+v", found.Damaged)
	}
	terminal, ok := found.Terminal("app", goodTask)
	if !ok {
		t.Fatal("the terminal was lost")
	}
	if terminal.Shell != nil {
		t.Error("an untagged user pane was adopted as the task's shell")
	}
}

// TestACommandDirectoryCannotBreakDiscovery closes ADR-030 evidence 10.
//
// The working directory is the one caller-supplied value tmux reports back, and
// it comes back inside a tab-separated list format. A tab in it misaligns every
// pane field and breaks discovery for every terminal on the server, which is the
// blast radius quarantine bounds — reached before quarantine can bound it.
func TestACommandDirectoryCannotBreakDiscovery(t *testing.T) {
	for _, directory := range []string{
		"/work/a\tb",
		"/work/a\nb",
		"/work/a\rb",
		"/work/a\x00b",
	} {
		spec := CommandSpec{Program: "/bin/sh", Directory: directory}
		if err := spec.Validate(); err == nil {
			t.Errorf("a working directory containing a separator was accepted: %q", directory)
		}
	}

	// A tab is refused in an argument too, which the previous rule missed.
	spec := CommandSpec{Program: "/bin/sh", Arguments: []string{"a\tb"}, Directory: "/work"}
	if err := spec.Validate(); err == nil {
		t.Error("an argument containing a tab was accepted")
	}

	// An ordinary directory still passes, so the rule refuses separators rather
	// than everything.
	if err := (CommandSpec{Program: "/bin/sh", Directory: "/work/api"}).Validate(); err != nil {
		t.Errorf("an ordinary working directory was refused: %v", err)
	}
}
