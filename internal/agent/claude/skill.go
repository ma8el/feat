package claude

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The setup skill, as Claude Code discovers it: a directory named for the
// skill under the skills directory, holding a SKILL.md (ADR-093).
//
// Beside the document sits the install record — the Feat build that wrote the
// file and a checksum of what was written — which is what lets a later install
// tell a file Feat wrote from one the user edited.
const (
	skillName       = "feat-setup"
	skillFileName   = "SKILL.md"
	skillRecordName = ".feat-skill.json"
)

// skillDocument is the canonical setup skill. It is embedded so that the skill
// on disk is written by the build it matches: one binary carries the document,
// the schema, and the example the document sends its reader to (ADR-093).
//
//go:embed skill.md
var skillDocument []byte

// envClaudeConfigDir is Claude Code's own override for where its configuration
// — its skills included — lives. When it is set, a skill written to ~/.claude
// would be one Claude Code never reads.
const envClaudeConfigDir = "CLAUDE_CONFIG_DIR"

// SkillDir returns the directory Claude Code reads the Feat setup skill from.
func SkillDir(getenv func(string) string, home string) string {
	base := filepath.Join(home, ".claude")
	if dir := getenv(envClaudeConfigDir); dir != "" {
		base = dir
	}
	return filepath.Join(base, "skills", skillName)
}

// SkillInstall reports what InstallSkill did.
type SkillInstall struct {
	// Path is the skill document that was written.
	Path string
	// Replaced reports that a file was already there and was replaced.
	Replaced bool
}

// SkillDivergedError refuses an install over a skill file that is not what an
// install recorded writing.
//
// The two causes — the user edited the file, or something else wrote it — are
// exactly what the record exists to separate from Feat's own earlier install,
// and neither may be replaced without being asked (ADR-093).
type SkillDivergedError struct {
	// Path is the file that was refused.
	Path string
	// Cause says what the comparison found, as a clause after the path.
	Cause string
}

func (e *SkillDivergedError) Error() string {
	return fmt.Sprintf("%s %s, so it was not replaced: "+
		"compare it with what `feat skill show` prints, move it aside if its content "+
		"is worth keeping, or re-run with --force to replace it",
		e.Path, e.Cause)
}

// skillRecord is the provenance marker written beside the skill.
type skillRecord struct {
	// Version is the Feat build that wrote the file.
	Version string `json:"version"`
	// Checksum is sha256 over the document as written, prefixed with the
	// algorithm so that a future change of it reads as a difference rather
	// than passing as a silent mismatch.
	Checksum string `json:"checksum"`
}

// Permissions for the skill directory and its files. The document is not a
// secret — it is the same text `feat skill install --help` describes and every
// build carries — and Claude Code has to be able to read it.
const (
	skillDirPerm  os.FileMode = 0o755
	skillFilePerm os.FileMode = 0o644
)

// Skill returns the setup skill document, exactly as InstallSkill writes it.
// It is returned as a copy, so what the build embedded is what every caller
// prints — and what `feat skill show` prints is diffable against an installed
// copy an install refused to replace.
func Skill() []byte { return bytes.Clone(skillDocument) }

// PlanSkillInstall reports what InstallSkill would do to dir, writing nothing:
// the path it would write, whether a file is already there to replace, and —
// as the same SkillDivergedError the install itself would return — a refusal.
//
// InstallSkill runs this same decision before it applies, so a dry run and a
// real one cannot drift.
func PlanSkillInstall(dir string, force bool) (SkillInstall, error) {
	path := filepath.Join(dir, skillFileName)

	// #nosec G304 -- the path is the skills directory joined with a constant
	// file name; nothing here comes from the agent or from a task.
	current, err := os.ReadFile(path)
	replaced := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return SkillInstall{}, fmt.Errorf("reading the installed skill %s: %w", path, err)
	}

	if replaced && !force {
		if cause := skillDiverged(current, filepath.Join(dir, skillRecordName)); cause != "" {
			return SkillInstall{}, &SkillDivergedError{Path: path, Cause: cause}
		}
	}
	return SkillInstall{Path: path, Replaced: replaced}, nil
}

// InstallSkill writes the setup skill into dir, refusing to replace a file
// that is not what an install recorded writing (ADR-093).
//
// The record is written before the document, which is the order everything
// else follows: plan, record, then apply, so that no file exists that the
// record cannot name. An install interrupted between the two leaves a record
// ahead of the document; the next install then refuses with the reason rather
// than guessing, and --force recovers it, losing only Feat's own earlier
// words.
func InstallSkill(dir, version string, force bool) (SkillInstall, error) {
	planned, err := PlanSkillInstall(dir, force)
	if err != nil {
		return SkillInstall{}, err
	}
	path, recordPath := planned.Path, filepath.Join(dir, skillRecordName)

	if err := os.MkdirAll(dir, skillDirPerm); err != nil {
		return SkillInstall{}, fmt.Errorf("creating the skill directory %s: %w", dir, err)
	}
	record, err := json.MarshalIndent(skillRecord{
		Version:  version,
		Checksum: skillChecksum(skillDocument),
	}, "", "  ")
	if err != nil {
		return SkillInstall{}, fmt.Errorf("rendering the skill install record: %w", err)
	}
	if err := writeSkillFile(recordPath, append(record, '\n')); err != nil {
		return SkillInstall{}, err
	}
	if err := writeSkillFile(path, skillDocument); err != nil {
		return SkillInstall{}, err
	}
	return planned, nil
}

// skillDiverged reports why the installed file cannot be replaced silently, or
// "" when it can.
func skillDiverged(current []byte, recordPath string) string {
	sum := skillChecksum(current)
	if sum == skillChecksum(skillDocument) {
		// Already exactly what this binary writes. However it got here — most
		// likely a record lost or never written — replacing it and refreshing
		// the record loses nothing anyone authored.
		return ""
	}

	// #nosec G304 -- the path is the skills directory joined with a constant
	// file name; nothing here comes from the agent or from a task.
	body, err := os.ReadFile(recordPath)
	if err != nil {
		return "was not recorded by `feat skill install`, so Feat cannot tell whether it is yours"
	}
	var record skillRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return fmt.Sprintf("has an install record that cannot be read (%v)", err)
	}
	if sum == record.Checksum {
		return ""
	}
	return fmt.Sprintf("has changed since Feat %s installed it", record.Version)
}

// skillChecksum renders the checksum the install record carries.
func skillChecksum(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// writeSkillFile writes into a temporary file beside path and renames it in,
// so an interruption leaves the previous file rather than half of a new one.
func writeSkillFile(path string, body []byte) error {
	handle, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating a temporary file for %s: %w", path, err)
	}
	name := handle.Name()
	discard := func(err error) error {
		_ = handle.Close()
		_ = os.Remove(name)
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if _, err := handle.Write(body); err != nil {
		return discard(err)
	}
	if err := handle.Chmod(skillFilePerm); err != nil {
		return discard(err)
	}
	if err := handle.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
