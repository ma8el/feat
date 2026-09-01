package guard

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The decision log is one file per decision under decisionsDir, with
// decisionIndex naming every one of them (ADR-089). The index keeps its place in
// CLAUDE.md's reading order, so what an agent reads before writing anything is
// the shape of every decision and where each one lives.
//
// These four guards are here rather than beside the documents because the
// failure they are written against is silent. A section dropped or half-copied
// by a future reorganisation turns nothing red; an ADR written on two branches
// at once merges into a document with one number twice; and a reference to a
// decision nobody wrote reads exactly like a reference to one somebody did.
const (
	decisionsDir  = "docs/decisions"
	decisionIndex = "docs/10-decisions-and-open-questions.md"
)

// adrReference matches a decision named anywhere in the repository. Every
// reference in this tree is a bare name of this shape — there is not one
// anchor-style link — which is what made the split free.
var adrReference = regexp.MustCompile(`ADR-([0-9]{3})`)

// decisionFileName matches the name of a decision file: the zero-padded number
// that addresses it, and the kebab-cased slug that makes a directory listing
// readable.
var decisionFileName = regexp.MustCompile(`^ADR-([0-9]{3})-[a-z0-9]+(?:-[a-z0-9]+)*\.md$`)

// decisionHeading matches the heading a decision file leads with.
var decisionHeading = regexp.MustCompile(`^# ADR-([0-9]{3}) \x{2014} \S`)

// indexEntry matches the link an index row carries to the file it describes.
var indexEntry = regexp.MustCompile(`\(decisions/(ADR-[0-9]{3}-[a-z0-9-]+\.md)\)`)

// decisionScanSkips are the directories a repository-wide scan for references
// does not descend into. testdata is deliberately not among them: a fixture that
// cites a decision cites one, and a citation that does not resolve is as broken
// there as anywhere else.
var decisionScanSkips = map[string]bool{
	".git":         true,
	"bin":          true,
	"dist":         true,
	"node_modules": true,
	"vendor":       true,
}

// decisionFile is one file under docs/decisions, as it is named and as its
// heading claims to be. The two are separate fields because the point of
// reading both is that they can disagree.
type decisionFile struct {
	name    string // ADR-NNN-slug.md
	number  string // the three digits in the name
	heading string // the three digits in the file's own heading, empty if it has none
}

// TestEveryDecisionFileIsNamedForItsOwnHeading holds the two halves of a
// decision's identity together: the number in its filename, which is what every
// reference resolves through, and the number in the heading a reader sees.
func TestEveryDecisionFileIsNamedForItsOwnHeading(t *testing.T) {
	for _, decision := range decisionFiles(t, repoRoot(t)) {
		if decision.heading == "" {
			t.Errorf("%s/%s does not lead with a heading of the form `# ADR-NNN — Title`\n"+
				"\tA decision file is read on its own, so its first line has to name the decision it holds.",
				decisionsDir, decision.name)
			continue
		}
		if decision.heading != decision.number {
			t.Errorf("%s/%s is named for ADR-%s and its heading says ADR-%s\n"+
				"\tOne of the two is wrong, and every reference in the repository resolves through the name.",
				decisionsDir, decision.name, decision.number, decision.heading)
		}
	}
}

// TestNoTwoDecisionsClaimOneNumber is the guard against the collision that made
// the split worth making: two branches each appending a decision take the next
// free number, and in one document Git merges both cleanly into one heading
// twice. As files it is a name that has to be resolved by a person — unless a
// slug differs, which is the case this test is for.
func TestNoTwoDecisionsClaimOneNumber(t *testing.T) {
	claimed := map[string][]string{}
	for _, decision := range decisionFiles(t, repoRoot(t)) {
		claimed[decision.number] = append(claimed[decision.number], decision.name)
	}

	for _, number := range sortedKeys(claimed) {
		files := claimed[number]
		if len(files) > 1 {
			t.Errorf("ADR-%s is claimed by %s\n"+
				"\tA number addresses one decision. Renumber all but one of them, in its file, "+
				"its filename, and its index row.",
				number, strings.Join(files, " and "))
		}
	}
}

// TestTheIndexAndTheDecisionFilesNameEachOther checks the property the index
// exists for. A file the index does not list is a decision an agent reading the
// specification never learns about; a row pointing at nothing is a link that
// breaks in whatever renders it.
func TestTheIndexAndTheDecisionFilesNameEachOther(t *testing.T) {
	root := repoRoot(t)

	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(decisionIndex)))
	if err != nil {
		t.Fatalf("reading the index: %v", err)
	}

	listed := map[string]int{}
	for _, match := range indexEntry.FindAllStringSubmatch(string(body), -1) {
		listed[match[1]]++
	}

	held := map[string]bool{}
	for _, decision := range decisionFiles(t, root) {
		held[decision.name] = true
	}

	for _, name := range sortedKeys(held) {
		switch listed[name] {
		case 1:
		case 0:
			t.Errorf("%s does not list %s/%s\n"+
				"\tAdd its index row, so that a reader of the specification meets the decision.",
				decisionIndex, decisionsDir, name)
		default:
			t.Errorf("%s lists %s %d times, and an index row is one per decision",
				decisionIndex, name, listed[name])
		}
	}

	for _, name := range sortedKeys(listed) {
		if !held[name] {
			t.Errorf("%s links to %s/%s, which is not there\n"+
				"\tEither the file was renamed and the row was not, or the row describes a decision nobody wrote.",
				decisionIndex, decisionsDir, name)
		}
	}
}

// TestEveryADRReferenceResolvesToOneDecision walks the whole repository, because
// the references are spread over it: the specification documents, the index, the
// decisions themselves, and more than a thousand Go comments. Each of them names
// a decision by bare number, so each of them is a link that a reorganisation, a
// renumbering, or a reference to a decision reserved on another branch can break
// without anything else noticing.
func TestEveryADRReferenceResolvesToOneDecision(t *testing.T) {
	root := repoRoot(t)

	known := map[string]bool{}
	for _, decision := range decisionFiles(t, root) {
		known[decision.number] = true
	}

	for _, file := range referencingFiles(t, root) {
		reported := map[string]bool{}
		for _, match := range adrReference.FindAllStringSubmatch(file.body, -1) {
			number := match[1]
			if known[number] || reported[number] {
				continue
			}
			reported[number] = true
			t.Errorf("%s names ADR-%s, and no file under %s holds it\n"+
				"\tA decision is addressed by its number, so a number nothing wrote is a reference "+
				"that goes nowhere. If the decision is being written on another branch, do not name "+
				"it here until it lands.",
				file.rel, number, decisionsDir)
		}
	}
}

// decisionFiles reads the decision directory and reports what it holds. It
// judges nothing beyond the shape of a name, so that each guard above can fail
// for its own reason with its own remedy.
func decisionFiles(t *testing.T, root string) []decisionFile {
	t.Helper()

	dir := filepath.Join(root, filepath.FromSlash(decisionsDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", decisionsDir, err)
	}

	found := make([]decisionFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			t.Errorf("%s/%s is a directory, and %s holds one decision per file",
				decisionsDir, entry.Name(), decisionsDir)
			continue
		}

		match := decisionFileName.FindStringSubmatch(entry.Name())
		if match == nil {
			t.Errorf("%s/%s is not named ADR-NNN-slug.md\n"+
				"\tThe number is what every reference resolves through, so it has to be in the name, "+
				"zero-padded to three digits.",
				decisionsDir, entry.Name())
			continue
		}

		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s/%s: %v", decisionsDir, entry.Name(), err)
		}
		decision := decisionFile{name: entry.Name(), number: match[1]}
		first, _, _ := strings.Cut(string(body), "\n")
		if heading := decisionHeading.FindStringSubmatch(first); heading != nil {
			decision.heading = heading[1]
		}
		found = append(found, decision)
	}

	if len(found) == 0 {
		t.Fatalf("no decision files under %s", decisionsDir)
	}
	return found
}

// textFile is one file of the repository and what it holds, read once so that
// the scan for references neither opens anything twice nor has to decide what a
// text file is from its name.
type textFile struct {
	rel  string
	body string
}

// referencingFiles returns every text file in the repository, as paths relative
// to the module root together with their contents. A file is text when it holds
// no NUL byte, which is what keeps this from maintaining a list of extensions
// that would be out of date the first time somebody adds a file type.
func referencingFiles(t *testing.T, root string) []textFile {
	t.Helper()

	var files []textFile
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if decisionScanSkips[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(body, 0) >= 0 {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, textFile{rel: filepath.ToSlash(rel), body: string(body)})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(files) == 0 {
		t.Fatalf("no files found under %s", root)
	}
	return files
}

// sortedKeys returns a map's keys in order, so that a failing run reports the
// same list whichever way the map iterated.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
