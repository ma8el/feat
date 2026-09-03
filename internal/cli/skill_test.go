package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installedSkill is where the machine's Claude Code would discover the skill.
func (m *machine) installedSkill() string {
	return filepath.Join(m.home, ".claude", "skills", "feat-setup", "SKILL.md")
}

// TestSkillInstallWritesWhereClaudeDiscoversIt covers the install a fresh
// machine runs: the document lands under the user's own ~/.claude, beside the
// record of what was written, and the command says where it put it.
func TestSkillInstallWritesWhereClaudeDiscoversIt(t *testing.T) {
	m := prepare(t)

	code, stdout, stderr := m.run(t, "skill", "install")
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
	}

	path := m.installedSkill()
	if !strings.Contains(stdout, "installed "+path) {
		t.Errorf("the output does not say what was written where:\n%s", stdout)
	}
	if !strings.Contains(stdout, "next session") {
		t.Errorf("the output does not say when Claude Code will see it:\n%s", stdout)
	}

	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the installed skill: %v", err)
	}
	if !strings.HasPrefix(string(document), "---\n") {
		t.Errorf("the installed skill does not lead with frontmatter:\n%.80s", document)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), ".feat-skill.json")); err != nil {
		t.Errorf("no install record beside the skill: %v", err)
	}
}

// TestSkillInstallFollowsClaudeConfigDir covers a machine where Claude Code's
// configuration has been moved: writing to ~/.claude there would install a
// skill Claude Code never reads.
func TestSkillInstallFollowsClaudeConfigDir(t *testing.T) {
	m := prepare(t)
	moved := filepath.Join(m.home, "claude-elsewhere")
	m.env.Getenv = func(key string) string {
		if key == "CLAUDE_CONFIG_DIR" {
			return moved
		}
		return ""
	}

	code, _, stderr := m.run(t, "skill", "install")
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
	}

	if _, err := os.Stat(filepath.Join(moved, "skills", "feat-setup", "SKILL.md")); err != nil {
		t.Errorf("the skill is not under CLAUDE_CONFIG_DIR: %v", err)
	}
	if _, err := os.Stat(m.installedSkill()); err == nil {
		t.Error("a skill was written under ~/.claude on a machine whose Claude configuration lives elsewhere")
	}
}

// TestSkillInstallRefusesAnEditedSkillWithoutForce is the command-level half
// of the reinstall rule: the refusal reaches the user with the reason and the
// way out, the file is untouched, and --force replaces it (ADR-093).
func TestSkillInstallRefusesAnEditedSkillWithoutForce(t *testing.T) {
	m := prepare(t)

	if code, _, stderr := m.run(t, "skill", "install"); code != ExitOK {
		t.Fatalf("installing: exit %d, stderr:\n%s", code, stderr)
	}

	path := m.installedSkill()
	edited := []byte("---\nname: feat-setup\n---\n\nThe user's own words.\n")
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatalf("editing the installed skill: %v", err)
	}

	code, _, stderr := m.run(t, "skill", "install")
	if code != ExitError {
		t.Fatalf("reinstalling over an edited skill: exit %d, want %d\nstderr: %s",
			code, ExitError, stderr)
	}
	for _, want := range []string{path, "changed since", "--force"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, stderr)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-reading the edited skill: %v", err)
	}
	if string(after) != string(edited) {
		t.Error("the refused install still changed the file")
	}

	code, stdout, stderr := m.run(t, "skill", "install", "--force")
	if code != ExitOK {
		t.Fatalf("forcing: exit %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "replaced "+path) {
		t.Errorf("the forced install does not say it replaced the file:\n%s", stdout)
	}
	replaced, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the replaced skill: %v", err)
	}
	if string(replaced) == string(edited) {
		t.Error("the forced install left the edited file in place")
	}
}

// TestSkillShowPrintsWhatInstallWrites pins `feat skill show` to the install,
// byte for byte and as the whole output: what a user reads before installing,
// and what they diff an installed copy against after a refusal, has to be
// exactly the document the install writes.
func TestSkillShowPrintsWhatInstallWrites(t *testing.T) {
	m := prepare(t)

	if code, _, stderr := m.run(t, "skill", "install"); code != ExitOK {
		t.Fatalf("installing: exit %d, stderr:\n%s", code, stderr)
	}
	installed, err := os.ReadFile(m.installedSkill())
	if err != nil {
		t.Fatalf("reading the installed skill: %v", err)
	}

	code, stdout, stderr := m.run(t, "skill", "show")
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
	}
	if stdout != string(installed) {
		t.Error("`feat skill show` does not print the document the install wrote")
	}
	if stderr != "" {
		t.Errorf("`feat skill show` wrote to stderr, and the document must be the whole output:\n%s", stderr)
	}
}

// TestSkillInstallDryRunReportsWithoutWriting covers the three answers a dry
// run gives — would install, would replace, and the refusal — and the promise
// common to all of them: the machine is left exactly as it was found.
func TestSkillInstallDryRunReportsWithoutWriting(t *testing.T) {
	t.Run("a fresh machine", func(t *testing.T) {
		m := prepare(t)

		code, stdout, stderr := m.run(t, "skill", "install", "--dry-run")
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stdout, "would install "+m.installedSkill()) {
			t.Errorf("the dry run does not say what it would install where:\n%s", stdout)
		}
		if !strings.Contains(stdout, "Nothing was written") {
			t.Errorf("the dry run does not say that nothing was written:\n%s", stdout)
		}
		if _, err := os.Stat(filepath.Join(m.home, ".claude")); !os.IsNotExist(err) {
			t.Errorf("the dry run wrote into ~/.claude: %v", err)
		}
	})

	t.Run("over its own earlier install", func(t *testing.T) {
		m := prepare(t)
		if code, _, stderr := m.run(t, "skill", "install"); code != ExitOK {
			t.Fatalf("installing: exit %d, stderr:\n%s", code, stderr)
		}

		code, stdout, stderr := m.run(t, "skill", "install", "--dry-run")
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stdout, "would replace "+m.installedSkill()) {
			t.Errorf("the dry run does not report the replacement:\n%s", stdout)
		}
	})

	t.Run("over an edited skill", func(t *testing.T) {
		m := prepare(t)
		if code, _, stderr := m.run(t, "skill", "install"); code != ExitOK {
			t.Fatalf("installing: exit %d, stderr:\n%s", code, stderr)
		}
		path := m.installedSkill()
		edited := []byte("---\nname: feat-setup\n---\n\nThe user's own words.\n")
		if err := os.WriteFile(path, edited, 0o644); err != nil {
			t.Fatalf("editing the installed skill: %v", err)
		}

		// The dry run gives the refusal the install would give, exit code and
		// reason included, so a script probing "will this install cleanly"
		// reads the same answer either way.
		code, _, stderr := m.run(t, "skill", "install", "--dry-run")
		if code != ExitError {
			t.Fatalf("dry run over an edited skill: exit %d, want %d\nstderr: %s",
				code, ExitError, stderr)
		}
		for _, want := range []string{path, "changed since", "--force"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, stderr)
			}
		}

		code, stdout, stderr := m.run(t, "skill", "install", "--dry-run", "--force")
		if code != ExitOK {
			t.Fatalf("forced dry run: exit %d, stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stdout, "would replace "+path) {
			t.Errorf("the forced dry run does not report the replacement:\n%s", stdout)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("re-reading the edited skill: %v", err)
		}
		if string(after) != string(edited) {
			t.Error("a dry run changed the file")
		}
	})
}

// TestSkillInstallReplacesItsOwnEarlierInstallSilently is the upgrade path a
// user actually takes: install, upgrade Feat, install again, no questions.
func TestSkillInstallReplacesItsOwnEarlierInstallSilently(t *testing.T) {
	m := prepare(t)

	if code, _, stderr := m.run(t, "skill", "install"); code != ExitOK {
		t.Fatalf("installing: exit %d, stderr:\n%s", code, stderr)
	}
	code, stdout, stderr := m.run(t, "skill", "install")
	if code != ExitOK {
		t.Fatalf("reinstalling: exit %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "replaced "+m.installedSkill()) {
		t.Errorf("the reinstall does not say what it replaced:\n%s", stdout)
	}
}
