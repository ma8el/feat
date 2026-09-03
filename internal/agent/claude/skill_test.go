package claude_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/agent/claude"
)

// noEnv is a machine with no Claude-related environment set.
func noEnv(string) string { return "" }

// readSkill reads what an install put on disk.
func readSkill(t *testing.T, dir string) (document []byte, record map[string]string) {
	t.Helper()

	document, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatalf("reading the installed skill: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".feat-skill.json"))
	if err != nil {
		t.Fatalf("reading the install record: %v", err)
	}
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatalf("parsing the install record: %v", err)
	}
	return document, record
}

// TestSkillDirIsClaudesOwnDiscoveryPath pins where the skill goes: the skills
// directory under ~/.claude, or under CLAUDE_CONFIG_DIR when Claude Code has
// been told its configuration lives elsewhere — a skill written to ~/.claude
// on such a machine is one Claude Code never reads.
func TestSkillDirIsClaudesOwnDiscoveryPath(t *testing.T) {
	home := filepath.Join("/", "home", "someone")

	if got, want := claude.SkillDir(noEnv, home),
		filepath.Join(home, ".claude", "skills", "feat-setup"); got != want {
		t.Errorf("SkillDir = %q, want %q", got, want)
	}

	moved := filepath.Join("/", "elsewhere", "claude")
	relocated := func(key string) string {
		if key == "CLAUDE_CONFIG_DIR" {
			return moved
		}
		return ""
	}
	if got, want := claude.SkillDir(relocated, home),
		filepath.Join(moved, "skills", "feat-setup"); got != want {
		t.Errorf("SkillDir under CLAUDE_CONFIG_DIR = %q, want %q", got, want)
	}
}

// TestInstallSkillWritesTheDocumentAndItsRecord covers a first install: the
// document Claude Code discovers, and beside it the record that lets the next
// install tell Feat's file from an edited one (ADR-093).
func TestInstallSkillWritesTheDocumentAndItsRecord(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills", "feat-setup")

	installed, err := claude.InstallSkill(dir, "v1.2.3", false)
	if err != nil {
		t.Fatalf("InstallSkill: %v", err)
	}
	if installed.Replaced {
		t.Error("a first install reported that it replaced something")
	}
	if want := filepath.Join(dir, "SKILL.md"); installed.Path != want {
		t.Errorf("installed.Path = %q, want %q", installed.Path, want)
	}

	document, record := readSkill(t, dir)

	// The frontmatter is what Claude Code discovers the skill by, so the
	// document must lead with it and it must name the directory it lives in.
	text := string(document)
	if !strings.HasPrefix(text, "---\n") {
		t.Errorf("the skill does not lead with frontmatter:\n%.80s", text)
	}
	head, _, found := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n")
	if !found {
		t.Fatalf("the skill's frontmatter never closes:\n%.200s", text)
	}
	if !strings.Contains(head, "name: feat-setup") {
		t.Errorf("the frontmatter does not name the skill for its directory:\n%s", head)
	}
	if !strings.Contains(head, "description:") {
		t.Errorf("the frontmatter carries no description, which is what a session decides by:\n%s", head)
	}

	sum := sha256.Sum256(document)
	if got, want := record["checksum"], "sha256:"+hex.EncodeToString(sum[:]); got != want {
		t.Errorf("the record's checksum is %q, and the document on disk sums to %q", got, want)
	}
	if got := record["version"]; got != "v1.2.3" {
		t.Errorf("the record's version is %q, want %q", got, "v1.2.3")
	}
}

// TestInstallSkillReplacesItsOwnEarlierInstall is the upgrade path: a file
// still matching what an install recorded is Feat's own words, and replacing
// them loses nothing anyone authored.
func TestInstallSkillReplacesItsOwnEarlierInstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "feat-setup")

	// An older binary's install: a different document, with a record whose
	// checksum matches it — which is all "an older binary wrote this" is.
	older := []byte("---\nname: feat-setup\n---\n\nAn earlier build's words.\n")
	sum := sha256.Sum256(older)
	record, err := json.Marshal(map[string]string{
		"version":  "v1.0.0",
		"checksum": "sha256:" + hex.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatalf("rendering the fixture record: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the skill directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), older, 0o644); err != nil {
		t.Fatalf("writing the older skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".feat-skill.json"), record, 0o644); err != nil {
		t.Fatalf("writing the older record: %v", err)
	}

	installed, err := claude.InstallSkill(dir, "v2.0.0", false)
	if err != nil {
		t.Fatalf("reinstalling over Feat's own file was refused: %v", err)
	}
	if !installed.Replaced {
		t.Error("the install did not report that it replaced the earlier file")
	}

	document, refreshed := readSkill(t, dir)
	if string(document) == string(older) {
		t.Error("the older document is still on disk")
	}
	if got := refreshed["version"]; got != "v2.0.0" {
		t.Errorf("the record still names %q, want %q", got, "v2.0.0")
	}
}

// TestInstallSkillRefusesWhatItDidNotWrite covers both indistinguishable-
// without-the-record causes: a file edited since Feat installed it, and a file
// nothing recorded installing. Each is refused with its reason, left exactly
// as it was, and replaced only by --force (ADR-093).
func TestInstallSkillRefusesWhatItDidNotWrite(t *testing.T) {
	t.Run("edited since install", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "feat-setup")
		if _, err := claude.InstallSkill(dir, "v1.0.0", false); err != nil {
			t.Fatalf("InstallSkill: %v", err)
		}

		path := filepath.Join(dir, "SKILL.md")
		document, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading the installed skill: %v", err)
		}
		edited := append(document, []byte("\nA line the user added.\n")...)
		if err := os.WriteFile(path, edited, 0o644); err != nil {
			t.Fatalf("editing the installed skill: %v", err)
		}

		_, err = claude.InstallSkill(dir, "v2.0.0", false)
		var diverged *claude.SkillDivergedError
		if !errors.As(err, &diverged) {
			t.Fatalf("reinstalling over an edited skill returned %v, want a SkillDivergedError", err)
		}
		for _, want := range []string{path, "changed since Feat v1.0.0", "--force"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not say %q:\n%s", want, err)
			}
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("re-reading the edited skill: %v", err)
		}
		if string(after) != string(edited) {
			t.Error("the refused install still changed the file")
		}

		forced, err := claude.InstallSkill(dir, "v2.0.0", true)
		if err != nil {
			t.Fatalf("InstallSkill with force: %v", err)
		}
		if !forced.Replaced {
			t.Error("the forced install did not report that it replaced the file")
		}
		replaced, record := readSkill(t, dir)
		if string(replaced) == string(edited) {
			t.Error("the forced install left the edited file in place")
		}
		if got := record["version"]; got != "v2.0.0" {
			t.Errorf("the record names %q after the forced install, want %q", got, "v2.0.0")
		}
	})

	t.Run("no record of an install", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "feat-setup")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating the skill directory: %v", err)
		}
		path := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(path, []byte("somebody's own skill\n"), 0o644); err != nil {
			t.Fatalf("writing a hand-authored skill: %v", err)
		}

		_, err := claude.InstallSkill(dir, "v1.0.0", false)
		var diverged *claude.SkillDivergedError
		if !errors.As(err, &diverged) {
			t.Fatalf("installing over an unrecorded file returned %v, want a SkillDivergedError", err)
		}
		if !strings.Contains(err.Error(), "was not recorded") {
			t.Errorf("the refusal does not say the file was never recorded:\n%s", err)
		}

		if _, err := claude.InstallSkill(dir, "v1.0.0", true); err != nil {
			t.Fatalf("InstallSkill with force: %v", err)
		}
	})
}

// TestSkillIsWhatInstallWrites pins the byte-for-byte claim `feat skill show`
// makes: the document Skill returns is the document InstallSkill puts on disk,
// so diffing an installed copy against it shows exactly what an edit changed.
func TestSkillIsWhatInstallWrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "feat-setup")
	if _, err := claude.InstallSkill(dir, "v1.0.0", false); err != nil {
		t.Fatalf("InstallSkill: %v", err)
	}

	document, _ := readSkill(t, dir)
	if string(document) != string(claude.Skill()) {
		t.Error("Skill() is not what InstallSkill wrote")
	}
}

// TestPlanSkillInstallDecidesWithoutWriting holds the dry run to its two
// promises: it returns the decision the install would act on — the same
// refusal included — and it leaves the machine exactly as it found it.
func TestPlanSkillInstallDecidesWithoutWriting(t *testing.T) {
	t.Run("a fresh machine", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "skills", "feat-setup")

		planned, err := claude.PlanSkillInstall(dir, false)
		if err != nil {
			t.Fatalf("PlanSkillInstall: %v", err)
		}
		if planned.Replaced {
			t.Error("the plan claims something is there to replace on a fresh machine")
		}
		if want := filepath.Join(dir, "SKILL.md"); planned.Path != want {
			t.Errorf("planned.Path = %q, want %q", planned.Path, want)
		}
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the dry run created the skill directory: %v", err)
		}
	})

	t.Run("an edited skill", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "feat-setup")
		if _, err := claude.InstallSkill(dir, "v1.0.0", false); err != nil {
			t.Fatalf("InstallSkill: %v", err)
		}
		path := filepath.Join(dir, "SKILL.md")
		edited := []byte("the user's own words\n")
		if err := os.WriteFile(path, edited, 0o644); err != nil {
			t.Fatalf("editing the installed skill: %v", err)
		}

		_, err := claude.PlanSkillInstall(dir, false)
		var diverged *claude.SkillDivergedError
		if !errors.As(err, &diverged) {
			t.Fatalf("planning over an edited skill returned %v, want a SkillDivergedError", err)
		}

		// With force the plan reports the replacement the install would make,
		// and still makes none of it.
		planned, err := claude.PlanSkillInstall(dir, true)
		if err != nil {
			t.Fatalf("PlanSkillInstall with force: %v", err)
		}
		if !planned.Replaced {
			t.Error("the forced plan does not report a replacement")
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("re-reading the edited skill: %v", err)
		}
		if string(after) != string(edited) {
			t.Error("the dry run changed the file")
		}
	})
}

// TestInstallSkillHealsAMissingRecord covers a file that is exactly what this
// binary writes with no record beside it — a record deleted, or an install
// interrupted between the two writes. Refusing it would demand --force for a
// replacement that changes nothing, so it is treated as Feat's.
func TestInstallSkillHealsAMissingRecord(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "feat-setup")
	if _, err := claude.InstallSkill(dir, "v1.0.0", false); err != nil {
		t.Fatalf("InstallSkill: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, ".feat-skill.json")); err != nil {
		t.Fatalf("removing the record: %v", err)
	}

	if _, err := claude.InstallSkill(dir, "v1.1.0", false); err != nil {
		t.Fatalf("reinstalling over Feat's own unmodified file was refused: %v", err)
	}
	_, record := readSkill(t, dir)
	if got := record["version"]; got != "v1.1.0" {
		t.Errorf("the record names %q after the reinstall, want %q", got, "v1.1.0")
	}
}
