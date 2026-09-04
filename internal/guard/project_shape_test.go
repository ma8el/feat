package guard

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/project"
)

// The project-shape rules are stated twice, and ADR-094 keeps them that way.
// `config.validateProject` and `config.validateRepositories` refuse a file, in
// file vocabulary, collecting every problem in it (ADR-028);
// `domain.Project.Validate` refuses a registration record, returning the first
// violation, as the storage-integrity gate against a hand-edited snapshot.
// Merging them would put one audience's error handling on the other's path, and
// the domain has to stay importable by both.
//
// What nothing enforced is that the two still say the same thing. Five rules are
// shared — a name, at least one repository, no repository named twice, a primary
// repository that is one of them, and a primary a task can edit (FR-PROJ-003) —
// and a change to one copy alone lets a project register that its own snapshot
// then refuses, or refuses one the snapshot would have accepted.
//
// These three tests pin the correspondence and nothing else. They assert accept
// or reject, never the words: the messages differ on purpose, because one names
// a line in a file the user wrote and the other names a record, and a test that
// held them identical would be asking for the merge this decision refused.
//
// They also stay on the five shared rules. Everything else either validator
// checks is one-sided deliberately — the configuration refuses a field the
// selected execution mode would ignore, the domain refuses timestamps no file
// can state — and reaching further would pin rules that are meant to differ.

// projectShapeDocument renders a project configuration from the two blocks the
// shared rules live in. Everything around them is the smallest configuration
// Feat accepts, so what differs between one case and the next is the rule under
// test and nothing else.
func projectShapeDocument(project, repositories string) string {
	return fmt.Sprintf(`version: 1

project:
%s
repositories:
%s
agent:
  execution:
    mode: host
`, project, repositories)
}

// The blocks the cases below are built from. `app` is editable and `docs` is
// not, which is what lets a case point the primary at a repository a task could
// never write to without changing anything else.
const (
	shapeProjectBlock = `  id: shape
  name: Shape
  primary_repository: app`

	shapeRepositoriesBlock = `  app:
    host_path: /repositories/app
    default_access: read_write
  docs:
    host_path: /repositories/docs
    default_access: read_only`
)

// shapeConfigFile is the name the document is decoded under. The file name
// carries the project identifier, so it has to match the one the document
// states.
const shapeConfigFile = "shape.yaml"

// shapeRegisteredAt is when the record under test is registered. It is fixed
// because the domain refuses a project with no creation time, and a clock
// reading is not what any of this is about.
var shapeRegisteredAt = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

// TestTheProjectShapeRulesAgreeAcrossTheConfigAndTheDomain asks both validators
// about the same project and requires one answer.
//
// Three of the five rules are reachable this way. The two that are not have
// their own tests below, because the shape of their correspondence is different
// rather than because they matter less.
func TestTheProjectShapeRulesAgreeAcrossTheConfigAndTheDomain(t *testing.T) {
	cases := []struct {
		rule     string
		document string
		accepted bool
	}{
		{
			rule:     "a named project with an editable primary among its repositories",
			document: projectShapeDocument(shapeProjectBlock, shapeRepositoriesBlock),
			accepted: true,
		},
		{
			// The two halves of this case cannot be separated: a project with no
			// repositories has no valid primary either, so what is pinned is that
			// an empty project is refused rather than which of the two rules each
			// validator refused it with.
			rule:     "a project with no repositories at all",
			document: projectShapeDocument(shapeProjectBlock, ""),
			accepted: false,
		},
		{
			// Both validators check membership for the sake of a better message
			// rather than a different verdict: a primary naming no repository of
			// the project has no access mode either, which the rule below refuses
			// on its own in both packages. So what this case pins is the pair, and
			// what either side loses by dropping its half is an explanation.
			rule: "a primary that is not one of the project's repositories",
			document: projectShapeDocument(`  id: shape
  name: Shape
  primary_repository: web`, shapeRepositoriesBlock),
			accepted: false,
		},
		{
			// FR-PROJ-003. This is the rule ADR-094 was written for: the primary
			// repository is where the agent works, so a project whose primary can
			// never be written to has no editable workspace at all.
			rule: "a primary a task cannot edit",
			document: projectShapeDocument(`  id: shape
  name: Shape
  primary_repository: docs`, shapeRepositoriesBlock),
			accepted: false,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.rule, func(t *testing.T) {
			accepted, refusal := configPathAccepts(t, testCase.document)
			_, err := registers(t, testCase.document)

			switch {
			case accepted && err == nil:
				if !testCase.accepted {
					t.Errorf("both paths accept %s, and neither should\n"+
						"\tThe rule has left both copies. If that was deliberate, this case says which "+
						"one it was and both validators have to lose it together.", testCase.rule)
				}
			case !accepted && err != nil:
				if testCase.accepted {
					t.Errorf("both paths refuse %s, and neither should\n"+
						"\tconfiguration: %v\n\tdomain: %v", testCase.rule, refusal, err)
				}
			case accepted:
				t.Errorf("the configuration accepts %s and the domain refuses the record it registers as\n"+
					"\tdomain: %v\n"+
					"\tThe two copies of this rule have drifted: a project Feat lets a user configure "+
					"cannot be registered. Whichever copy is right, ADR-094 requires both to say it.",
					testCase.rule, err)
			default:
				t.Errorf("the configuration refuses %s and the domain accepts the record it would register as\n"+
					"\tconfiguration: %v\n"+
					"\tThe two copies of this rule have drifted: a snapshot Feat would accept can no "+
					"longer be configured, so a project already registered would survive an edit that "+
					"a user can no longer make. Whichever copy is right, ADR-094 requires both to say it.",
					testCase.rule, refusal)
			}
		})
	}
}

// TestAConfiguredProjectAlwaysRegistersUnderAName holds the name rule, whose two
// copies correspond in a way the table above cannot express.
//
// The domain refuses a project with no name. A configuration cannot present one:
// resolution fills the name from the identifier before validation sees it, so
// `config.validateProject`'s own name rule is reached only by a document that
// states no identifier either — which both paths refuse for the identifier.
//
// So the correspondence here is that the configuration path guarantees the name
// the domain requires, and this test fails if it stops: without the default, the
// configuration would refuse a document that names nothing, and a document Feat
// accepts today would stop being one.
func TestAConfiguredProjectAlwaysRegistersUnderAName(t *testing.T) {
	document := projectShapeDocument(`  id: shape
  primary_repository: app`, shapeRepositoriesBlock)

	accepted, refusal := configPathAccepts(t, document)
	if !accepted {
		t.Fatalf("the configuration refuses a project that states no name of its own: %v\n"+
			"\tA name is filled from the identifier, so a document may omit it. If that changed, "+
			"the domain's own name rule now decides what a nameless project is, and ADR-094 "+
			"requires both copies to move together.", refusal)
	}

	registered, err := registers(t, document)
	if err != nil {
		t.Fatalf("the domain refuses the record a project that states no name registers as: %v", err)
	}
	if registered.Name == "" {
		t.Errorf("a project that states no name registers under none\n" +
			"\tThe domain requires a name and the configuration is what supplies it, so a record " +
			"reaching the store without one is the gate in domain.Project.Validate firing on " +
			"Feat's own output rather than on a hand-edited snapshot.")
	}
}

// TestNeitherPathAcceptsAProjectThatNamesOneRepositoryTwice holds the last of
// the five, whose copies also correspond asymmetrically.
//
// The configuration's answer is structural rather than a rule: repositories are
// a mapping keyed by identifier and the decoder is strict, so a document naming
// one twice is refused before a Config exists. The domain's is a check, because
// a snapshot on disk is a document nobody decoded strictly. Both refuse, which
// is what this pins; only the domain's copy can be reached with a record.
//
// The record is the one the valid document registers as, with one of its own
// repositories repeated, so that nothing here re-states the mapping from
// configuration to domain that project.FromConfig owns.
func TestNeitherPathAcceptsAProjectThatNamesOneRepositoryTwice(t *testing.T) {
	twice := projectShapeDocument(shapeProjectBlock, `  app:
    host_path: /repositories/app
    default_access: read_write
  app:
    host_path: /repositories/app
    default_access: read_write`)

	if accepted, _ := configPathAccepts(t, twice); accepted {
		t.Errorf("the configuration accepts a document naming one repository twice\n" +
			"\tStrict decoding of a mapping is what refuses it. If a repository list ever stops " +
			"being a mapping, this rule needs a check of its own, because the domain's copy " +
			"cannot see a file (ADR-094).")
	}

	registered, err := registers(t, projectShapeDocument(shapeProjectBlock, shapeRepositoriesBlock))
	if err != nil {
		t.Fatalf("the valid document does not register: %v", err)
	}
	registered.Repositories = append(registered.Repositories, registered.Repositories[0])
	if err := registered.Validate(); err == nil {
		t.Errorf("the domain accepts a record naming repository %s twice\n"+
			"\tNothing decodes a stored snapshot strictly, so this check is the only thing between "+
			"a hand-edited record and a project whose repositories shadow each other.",
			registered.Repositories[0].ID)
	}
}

// configPathAccepts reports whether the configuration path accepts a document,
// and what it said if it did not.
//
// Decoding, resolution, and validation are one path here because a refusal is a
// refusal wherever the document is stopped: the user is told no, and which stage
// said it is this package's business rather than the rule's.
func configPathAccepts(t *testing.T, document string) (bool, error) {
	t.Helper()

	cfg, err := resolvedConfig(t, document)
	if err != nil {
		return false, err
	}
	if err := cfg.Validate(); err != nil {
		return false, err
	}
	return true, nil
}

// registers maps a document onto the record it is registered as, and reports
// the domain's verdict on it.
//
// It goes through project.FromConfig rather than a mapping written here, so that
// what the domain judges is what a registration actually produces. Its own
// comment says why it validates a configuration that has already been validated:
// the two rule sets are maintained separately, and the day they disagree the
// disagreement should stop a registration rather than be recorded as state.
// This test is the day arriving in CI instead.
func registers(t *testing.T, document string) (*domain.Project, error) {
	t.Helper()

	cfg, err := resolvedConfig(t, document)
	if err != nil {
		// Every case states a project the file format can express, so a document
		// that never becomes a configuration is a broken case rather than a
		// verdict about the domain.
		t.Fatalf("the document does not resolve, so no record can be registered from it: %v", err)
	}
	return project.FromConfig(cfg, shapeRegisteredAt)
}

// resolvedConfig decodes and resolves a document without judging it.
func resolvedConfig(t *testing.T, document string) (*config.Config, error) {
	t.Helper()

	cfg, err := config.Parse(filepath.Join(t.TempDir(), shapeConfigFile), []byte(document))
	if err != nil {
		return nil, err
	}
	home := t.TempDir()
	opts := config.Options{
		Env: paths.Environment{
			Getenv: func(string) string { return "" },
			Home:   home,
		},
		StateDir: filepath.Join(home, "state"),
	}
	if err := cfg.Resolve(opts); err != nil {
		return nil, err
	}
	return cfg, nil
}
