package wizard

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/paths"
)

// fakeHost answers for a machine with two checkouts on it: one with an ordinary
// remote and a Compose file beside it, and one with no remote at all.
//
// It answers by path, because that is what the proposals are derived from: a
// host that answered the same thing everywhere could not tell a repository that
// was inspected from one that was assumed.
type fakeHost struct {
	root string
	// inspected counts the directories Git was asked about, which is what makes
	// "it derives rather than asks" checkable.
	inspected int
}

func (h *fakeHost) Inspect(_ context.Context, path string) (Checkout, error) {
	switch path {
	case filepath.Join(h.root, "api"):
		h.inspected++
		return Checkout{Root: path, Remote: "origin", DefaultBranch: "main"}, nil
	case filepath.Join(h.root, "store"):
		h.inspected++
		return Checkout{Root: path}, nil
	default:
		return Checkout{}, &notARepository{path: path}
	}
}

func (h *fakeHost) ComposeFiles(dir string) []string {
	if dir == filepath.Join(h.root, "api") {
		return []string{filepath.Join(dir, "compose.yaml")}
	}
	return nil
}

func (h *fakeHost) ComposeServices(files ...string) []string {
	if len(files) == 0 {
		return nil
	}
	return []string{"dev", "worker"}
}

func (h *fakeHost) Exists(string) bool { return true }

func (h *fakeHost) Absolute(value string) (string, error) {
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	return filepath.Join(h.root, value), nil
}

func (h *fakeHost) WorkingDirectory() string { return filepath.Join(h.root, "api") }

// notARepository is what a directory Git knows nothing about answers with.
type notARepository struct{ path string }

func (e *notARepository) Error() string { return e.path + " is not a Git repository" }

// start builds a wizard against a temporary configuration directory.
func start(t *testing.T, id string) (*Wizard, *fakeHost) {
	t.Helper()

	root := t.TempDir()
	host := &fakeHost{root: root}
	// Getenv is supplied because resolving a configuration reads $EDITOR through
	// it directly, and a nil one is a nil function call rather than an empty
	// environment.
	process := paths.Environment{Getenv: func(string) string { return "" }, Home: root}
	flow, err := New(Options{
		Host:      host,
		ConfigDir: filepath.Join(root, "config"),
		Resolve:   config.Options{Env: process, StateDir: filepath.Join(root, "state")},
		ID:        id,
	})
	if err != nil {
		t.Fatalf("building a wizard: %v", err)
	}
	return flow, host
}

// answer applies one answer and fails the test when it is refused, naming the
// question that refused it.
func answer(t *testing.T, flow *Wizard, value string) {
	t.Helper()

	question, ok := flow.Step()
	if !ok {
		t.Fatalf("answering %q, but every question has been answered", value)
	}
	if err := flow.Answer(context.Background(), value); err != nil {
		t.Fatalf("%s (%q) refused %q: %v", question.ID, question.Prompt, value, err)
	}
}

// answers applies a script of them.
func answers(t *testing.T, flow *Wizard, values ...string) {
	t.Helper()

	for _, value := range values {
		answer(t, flow, value)
	}
}

// TestTheFlowComposesAConfigurationFromWhatItIsTold is the whole point of the
// package: the answers become a configuration Feat itself accepts.
func TestTheFlowComposesAConfigurationFromWhatItIsTold(t *testing.T) {
	flow, host := start(t, "")

	answers(t, flow,
		"app",     // project identifier
		"Example", // display name
		"",        // the checkout: the working directory, which is a repository
		"",        // repository identifier: api
		"",        // default access: read_write
		"n",       // no second repository
		"",        // execution mode: host
		"",        // provider CLI: none
		"n",       // no application services
		"go test ./...",
		"", // check name: go-test
	)

	if !flow.Complete() {
		question, _ := flow.Step()
		t.Fatalf("the flow is not finished; it is asking %s", question.ID)
	}
	if host.inspected != 1 {
		t.Errorf("Git was asked about %d directories, want the one that was answered", host.inspected)
	}

	review, err := flow.Review()
	if err != nil {
		t.Fatalf("the answers do not compose a configuration: %v", err)
	}
	if filepath.Base(review.Path) != "app.yaml" {
		t.Errorf("the file is %s, want it named after the project", review.Path)
	}

	text := string(review.Text)
	for _, want := range []string{
		"id: app", "name: Example",
		// Both were read from the checkout rather than asked for, which is what
		// the wizard exists to do.
		"default_branch: main", "remote: origin",
		// The check, as the argument vector it was split into rather than as the
		// line it was typed as.
		"id: go-test", "- ./...",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the configuration does not contain %q:\n%s", want, text)
		}
	}
}

// TestARepositoryWithNoRemoteDecidesTheBasePolicy checks the one value the
// wizard decides on the user's behalf, and that it decides it from what it found.
func TestARepositoryWithNoRemoteDecidesTheBasePolicy(t *testing.T) {
	flow, _ := start(t, "")

	answers(t, flow,
		"app", "",
		"store", // a checkout with no remote
		"", "", "n",
	)

	// It says so where it is decided, rather than deciding quietly.
	question, _ := flow.Step()
	if !strings.Contains(strings.Join(question.Notes, " "), "has a remote") {
		t.Errorf("the notes do not say why: %v", question.Notes)
	}

	answers(t, flow, "", "", "n", "")
	review, err := flow.Review()
	if err != nil {
		t.Fatalf("the answers do not compose a configuration: %v", err)
	}
	if !strings.Contains(string(review.Text), config.PolicyLocal) {
		t.Errorf("bases are not resolved locally for a repository with no remote:\n%s", review.Text)
	}
}

// TestAnAssumedValueIsReportedAsAssumed is the difference that stops being
// visible once a value is in the file.
func TestAnAssumedValueIsReportedAsAssumed(t *testing.T) {
	flow, _ := start(t, "")
	answers(t, flow, "app", "", "store")

	question, _ := flow.Step()
	notes := strings.Join(question.Notes, "\n")
	if !strings.Contains(notes, "no remote and no branch checked out") {
		t.Errorf("the notes do not separate what Git said from what Feat assumed:\n%s", notes)
	}
	if !strings.Contains(notes, "origin") || !strings.Contains(notes, "main") {
		t.Errorf("the notes do not name the values being assumed:\n%s", notes)
	}
}

// TestARefusedAnswerChangesNothing checks that a rejection leaves the flow where
// it was, which is what lets the same question be asked again.
func TestARefusedAnswerChangesNothing(t *testing.T) {
	flow, _ := start(t, "")
	answers(t, flow, "app", "")

	before, _ := flow.Step()
	if err := flow.Answer(context.Background(), "/nowhere"); err == nil {
		t.Fatal("a directory that is not a repository was accepted")
	}

	after, ok := flow.Step()
	if !ok || after.ID != before.ID {
		t.Errorf("a refused answer moved the flow from %s to %s", before.ID, after.ID)
	}
	if len(after.Notes) != 0 {
		t.Errorf("a refused answer left notes behind: %v", after.Notes)
	}
}

// TestSteppingBackUndoesTheAnswer is what the dialog's esc is, and the reason
// the flow snapshots rather than replaying: an answer changes more than the
// field it names.
func TestSteppingBackUndoesTheAnswer(t *testing.T) {
	flow, _ := start(t, "")
	answers(t, flow, "app", "", "", "api", "read_write", "y")

	// A second repository was asked for; stepping back twice returns to the
	// question that asked for it, and then to the first repository's access.
	if !flow.Back() {
		t.Fatal("nothing to step back to after six answers")
	}
	question, _ := flow.Step()
	if question.ID != "repository.another" {
		t.Fatalf("stepping back reached %s, want the question that was answered", question.ID)
	}

	// Answering it the other way now composes a project with one repository,
	// with nothing of the second left in it.
	answers(t, flow, "n", "", "", "n", "")
	review, err := flow.Review()
	if err != nil {
		t.Fatalf("the answers do not compose a configuration: %v", err)
	}
	if strings.Count(string(review.Text), "host_path:") != 1 {
		t.Errorf("the configuration holds more than the one repository answered:\n%s", review.Text)
	}
}

// TestSteppingBackToTheStartIsPossible checks the whole way out, because the
// dialog closes when there is nothing left to step back to.
func TestSteppingBackToTheStartIsPossible(t *testing.T) {
	flow, _ := start(t, "")
	answers(t, flow, "app", "Example")

	for flow.Back() {
	}
	question, ok := flow.Step()
	if !ok || question.ID != "project.id" {
		t.Fatalf("stepping back reached %s, want the first question", question.ID)
	}
	if flow.Back() {
		t.Error("there was something behind the first question")
	}
}

// TestANamedProjectStartsAtTheSecondQuestion checks `feat project init app`,
// where the identifier is supplied rather than asked for.
func TestANamedProjectStartsAtTheSecondQuestion(t *testing.T) {
	flow, _ := start(t, "app")

	question, ok := flow.Step()
	if !ok || question.ID != "project.name" {
		t.Fatalf("the first question is %s, want the display name", question.ID)
	}
	if flow.Back() {
		t.Error("a supplied identifier can be stepped back into")
	}
	if flow.ID() != "app" {
		t.Errorf("the project is %q, want the one that was named", flow.ID())
	}
}

// TestTheDevcontainerQuestionsFollowTheMode checks that answering one question
// decides which questions exist, which is why this is a flow and not a form.
func TestTheDevcontainerQuestionsFollowTheMode(t *testing.T) {
	flow, _ := start(t, "")
	answers(t, flow, "app", "", "", "api", "", "n", "devcontainer")

	// The Compose file found beside the repository is proposed rather than asked
	// for, and the services it declares are reported once it is accepted.
	question, _ := flow.Step()
	if question.ID != "agent.compose" || filepath.Base(question.Proposed) != "compose.yaml" {
		t.Fatalf("the Compose question proposes %q", question.Proposed)
	}
	answers(t, flow, "", "")

	service, _ := flow.Step()
	if service.ID != "agent.service" || service.Proposed != "dev" {
		t.Fatalf("the service question proposes %q, want the one the file declares", service.Proposed)
	}
	if !strings.Contains(strings.Join(service.Notes, " "), "dev, worker") {
		t.Errorf("the services the file declares are not reported: %v", service.Notes)
	}

	if err := flow.Answer(context.Background(), "dev"); err != nil {
		t.Fatalf("naming the service: %v", err)
	}
	if err := flow.Answer(context.Background(), "root"); err == nil {
		t.Error("the agent was allowed to run as root in the devcontainer")
	}
	answers(t, flow, "developer", "/srv/api", "y", "feat-claude", "gh", "n", "")

	if !flow.Complete() {
		question, _ := flow.Step()
		t.Fatalf("the flow is still asking %s", question.ID)
	}
	review, err := flow.Review()
	if err != nil {
		t.Fatalf("the answers do not compose a configuration: %v", err)
	}
	for _, want := range []string{"mode: devcontainer", "service: dev", "user: developer", "/srv/api"} {
		if !strings.Contains(string(review.Text), want) {
			t.Errorf("the configuration does not contain %q:\n%s", want, review.Text)
		}
	}
}

// TestAProjectThatIsAlreadyConfiguredIsRefused is the file a user must not lose
// to a command they ran twice.
func TestAProjectThatIsAlreadyConfiguredIsRefused(t *testing.T) {
	flow, host := start(t, "")
	answer(t, flow, "app")
	answers(t, flow, "", "", "api", "", "n", "", "", "n", "")

	if _, err := flow.Write(); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}

	// A second wizard for the same project is refused at the question that names
	// the file, before anything else is asked.
	second, err := New(Options{Host: host, ConfigDir: filepath.Dir(mustReview(t, flow).Path)})
	if err != nil {
		t.Fatalf("building a second wizard: %v", err)
	}
	if err := second.Answer(context.Background(), "app"); err == nil {
		t.Fatal("a project that is already configured was accepted")
	} else if !strings.Contains(err.Error(), "already configured at") {
		t.Errorf("the refusal does not name the file: %v", err)
	}
}

// TestWritingNeverReplacesAFile checks the exclusive create, which is the whole
// of the protection an existing configuration has.
func TestWritingNeverReplacesAFile(t *testing.T) {
	flow, _ := start(t, "")
	answers(t, flow, "app", "", "", "api", "", "n", "", "", "n", "")

	file, err := flow.Write()
	if err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	if _, err := flow.Write(); err == nil {
		t.Fatalf("%s was written twice", file)
	} else if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("the second write does not say why it refused: %v", err)
	}
}

func mustReview(t *testing.T, flow *Wizard) Review {
	t.Helper()

	review, err := flow.Review()
	if err != nil {
		t.Fatalf("composing the configuration: %v", err)
	}
	return review
}
