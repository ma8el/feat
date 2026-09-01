package guard

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The decision log is one file per decision under decisionsDir, with
// decisionIndex naming every one of them (ADR-089). The index keeps its place in
// CLAUDE.md's reading order, so what an agent reads before writing anything is
// the shape of every decision and where each one lives.
//
// These six guards are here rather than beside the documents because the
// failure they are written against is silent. A section dropped or half-copied
// by a future reorganisation turns nothing red; an ADR written on two branches
// at once merges into a document with one number twice; a reference to a
// decision nobody wrote reads exactly like a reference to one somebody did; a
// status written a second way is a status nothing can read; and half a
// supersession leaves the decision that was superseded saying so and the one
// that superseded it never having heard of it.
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

// statusLine matches a line that sets a decision's status, however it is
// written. The guard reads every one of them and then decides, so that a line
// written a second way fails as a malformed status rather than going unseen.
var statusLine = regexp.MustCompile(`(?m)^Status:.*$`)

// statusForm is the one way a status is written: no padding, no trailing
// whitespace, one word.
//
// One word is a decision rather than a convenience. The form a decision log
// usually reaches for, `superseded by ADR-065`, puts a relation between two
// decisions inside a string — where a reader looking for the reasoning does not
// meet it, and where the relation guard below, which reads the body, cannot see
// it. ADR-089 says the same thing from the other end. Widening this to admit a
// multi-word value is therefore a change to where a relation lives, not a
// loosening of a format.
var statusForm = regexp.MustCompile(`^Status: ([a-z_]+)$`)

// decisionStatuses is the closed set of statuses a decision may carry.
//
// It holds `accepted` alone because that is what every decision in this log
// says. It is deliberately not seeded with the statuses a decision log usually
// grows — proposed, rejected, superseded — because a value nothing uses is a
// value nobody has decided the meaning of, and the first decision to need one
// should add it here on purpose rather than find it already waiting.
var decisionStatuses = map[string]bool{
	"accepted": true,
}

// A relation statement is how one decision records what it does to another:
// this log writes it as an emphatic lead — **Superseded for ports by ADR-065.**,
// **Extended by ADR-065**, **supersedes** ADR-034's rule — and that emphasis is
// what tells it from the same verb used in ordinary prose, of which there are
// five occurrences that mean nothing of the kind.
//
// So a relation is a bold span carrying one of the verbs, and the decision it
// relates to is the first one named inside that span or just after it.
var (
	boldSpan     = regexp.MustCompile(`(?s)\*\*(.+?)\*\*`)
	relationVerb = regexp.MustCompile(`(?i)\b(supersede|supersedes|superseded|extended)\b`)
)

// relationTrail is how far past a bold span the decision it names may sit, which
// covers the one site that puts it outside: **supersedes** ADR-034's rule.
const relationTrail = 120

// relation is one decision recording what it does to another.
type relation struct {
	source string // the number of the decision making the statement
	target string // the number of the decision it names
	verb   string // the verb it made the statement with, lower-cased
}

// knownRelations are the relation statements this log held when the guard was
// written: ADR-034 twice, ADR-041, and ADR-065, at lines 91, 203, 152 and 129 of
// their own files. They are pinned because the expensive way for this guard to
// fail is to stop matching — a reciprocity check over an empty set passes, and
// passes silently, which is worse than not having one.
//
// The guard requires these to be among what it matched and does not require the
// set to be exactly these, so that a new decision recording a supersession is
// checked for reciprocity rather than being made to edit this list first.
var knownRelations = []relation{
	{source: "034", target: "065", verb: "superseded"},
	{source: "034", target: "065", verb: "extended"},
	{source: "041", target: "043", verb: "superseded"},
	{source: "065", target: "034", verb: "supersedes"},
}

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
	body    string // everything the file holds
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

// TestEveryDecisionCarriesOneStatusFromTheClosedSet holds the one field of a
// decision that is read rather than prose. The index prints it beside every
// title, so a status written a second way — padded, capitalised, trailing a
// space, or a word nobody has defined — is a value that reads fine to a person
// and is a different value to anything mechanical.
//
// The trailing-space form is not hypothetical: 43 of these files inherited
// `Status: accepted` with two spaces after it from the document they were split
// out of, where it was a Markdown hard break, and nothing in the toolchain
// noticed until this guard.
func TestEveryDecisionCarriesOneStatusFromTheClosedSet(t *testing.T) {
	for _, decision := range decisionFiles(t, repoRoot(t)) {
		lines := statusLine.FindAllString(decision.body, -1)
		if len(lines) != 1 {
			t.Errorf("%s/%s carries %d status lines, and a decision has exactly one\n"+
				"\tThe status is what the index prints beside the title, so there is one thing to print.",
				decisionsDir, decision.name, len(lines))
			continue
		}

		form := statusForm.FindStringSubmatch(lines[0])
		if form == nil {
			t.Errorf("%s/%s writes its status as %q\n"+
				"\tWrite it as `Status: <value>` — one space, one word, and nothing after it. "+
				"A trailing space is invisible in an editor and is not invisible to anything that reads this. "+
				"A status that wants to name another decision — `superseded by ADR-065` — belongs in the body "+
				"instead, where a reader meets it and where the relation guard below can check that the "+
				"decision it names answers it (ADR-089).",
				decisionsDir, decision.name, lines[0])
			continue
		}

		if !decisionStatuses[form[1]] {
			t.Errorf("%s/%s has status %q, which is not one this log defines (%s)\n"+
				"\tIf the decision genuinely needs a new status, add it to decisionStatuses here "+
				"and say in the log what it means. Do not add one ahead of a decision that uses it.",
				decisionsDir, decision.name, form[1], strings.Join(sortedKeys(decisionStatuses), ", "))
		}
	}
}

// TestARelationBetweenDecisionsIsAnsweredByBothOfThem checks the half of a
// supersession that is easy to leave out. Recording that this decision was
// superseded is the natural half to write, because it is written while reading
// the decision that lost; the decision that won is somewhere else and is
// finished. A log where only one end of the relation knows about it sends a
// reader who arrives from the other end straight past it.
//
// What counts as an answer is a mention, not a matching relation statement: the
// decision that won ordinarily explains the change in its own terms rather than
// restating the relation, which is what ADR-043 does for ADR-041. What is being
// guarded is that the two ends know about each other at all.
//
// Two limits, stated rather than discovered. A bare citation creates no
// obligation, so a decision may cite any other freely. And a relation statement
// that names no decision — "superseded by a later decision" — is read as prose
// and skipped, because a relation this cannot follow is one it cannot check.
func TestARelationBetweenDecisionsIsAnsweredByBothOfThem(t *testing.T) {
	decisions := decisionFiles(t, repoRoot(t))

	bodies := make(map[string]string, len(decisions))
	for _, decision := range decisions {
		bodies[decision.number] = decision.body
	}

	var found []relation
	for _, decision := range decisions {
		for _, span := range boldSpan.FindAllStringSubmatchIndex(decision.body, -1) {
			statement := decision.body[span[2]:span[3]]
			verb := relationVerb.FindString(statement)
			if verb == "" {
				continue
			}

			trail := min(span[1]+relationTrail, len(decision.body))
			named := adrReference.FindStringSubmatch(statement + decision.body[span[1]:trail])
			if named == nil {
				continue
			}

			found = append(found, relation{
				source: decision.number,
				target: named[1],
				verb:   strings.ToLower(verb),
			})
		}
	}

	for _, want := range knownRelations {
		if !slices.Contains(found, want) {
			t.Errorf("the matcher no longer finds ADR-%s's %q relation to ADR-%s\n"+
				"\tEither that statement was reworded, or the matcher stopped recognising the form "+
				"this log writes relations in. Fix whichever it is: a reciprocity check that matches "+
				"nothing passes, and passes silently.",
				want.source, want.verb, want.target)
		}
	}

	for _, found := range found {
		answer, ok := bodies[found.target]
		if !ok {
			t.Errorf("ADR-%s says it %s ADR-%s, and no file under %s holds ADR-%s",
				found.source, found.verb, found.target, decisionsDir, found.target)
			continue
		}
		if !strings.Contains(answer, "ADR-"+found.source) {
			t.Errorf("ADR-%s says it %s ADR-%s, and ADR-%s never mentions ADR-%s\n"+
				"\tA reader who arrives at ADR-%s has no way to learn about the relation. "+
				"Say there what it did to ADR-%s, in its own terms.",
				found.source, found.verb, found.target,
				found.target, found.source, found.target, found.source)
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
		decision := decisionFile{name: entry.Name(), number: match[1], body: string(body)}
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
