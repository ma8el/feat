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

// Compose answers what the api checkout's own Compose file says about itself:
// two services, one of which mounts the repository at /srv and publishes a
// port, and one built from the repository itself.
func (h *fakeHost) Compose(root string, files ...string) Composition {
	if len(files) == 0 || root != filepath.Join(h.root, "api") {
		return Composition{}
	}
	return Composition{
		Services:      []string{"dev", "worker"},
		ContainerPath: "/srv",
		Reachable:     []string{"dev"},
		Baked:         []string{"worker"},
	}
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
	flow, host := start(t, "")
	answers(t, flow, "app", "", "", "api", "", "n", "devcontainer")

	// Nothing is proposed. The files beside a repository are overwhelmingly its
	// application's rather than the agent's container's, and this section is
	// asked before the one that would know which: proposing one of them offered
	// an application file for the devcontainer, and the empty answer that was
	// meant to finish the loop accepted it.
	question, _ := flow.Step()
	if question.ID != "agent.compose" {
		t.Fatalf("the first devcontainer question is %s", question.ID)
	}
	if question.Proposed != "" {
		t.Errorf("the devcontainer's Compose question proposes %q, and it cannot know", question.Proposed)
	}
	if err := flow.Answer(context.Background(), ""); err == nil {
		t.Error("an empty answer was accepted for a question with nothing to propose")
	}
	answers(t, flow, filepath.Join(host.root, "api", "compose.yaml"), "")

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
	answers(t, flow, "developer", "/srv/api", "y", "feat-claude", "n", "")

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

// TestTheApplicationIsAnsweredOneRepositoryAtATime is the shape ADR-065 gives
// the runtime, as a conversation.
//
// A runtime is composed of its repositories, so it is answered where the code
// is: each repository is asked what it brings, what its services are, and where
// those services expect its source. The proposals come from that repository's
// own Compose files, read structurally, and the question is asked with the
// agent running on this host — which is the mode the runtime container path
// used to be skipped for (ADR-065 evidence 6).
func TestTheApplicationIsAnsweredOneRepositoryAtATime(t *testing.T) {
	flow, _ := start(t, "app")
	answers(t, flow,
		"",    // display name
		"",    // the checkout: the working directory, which is the api repository
		"api", // repository identifier
		"",    // default access: read_write
		"n",   // no second repository
		"",    // execution mode: host, which has no agent container at all
		"y",   // the project runs application services
	)

	repository, _ := flow.Step()
	if repository.ID != "runtime.repository" || repository.Proposed != "y" {
		t.Fatalf("the first application question is %s proposing %q, want the repository beside a "+
			"Compose file", repository.ID, repository.Proposed)
	}
	answer(t, flow, "y")

	compose, _ := flow.Step()
	if compose.ID != "runtime.compose" || filepath.Base(compose.Proposed) != "compose.yaml" {
		t.Fatalf("the Compose question of %s proposes %q", "api", compose.Proposed)
	}
	answers(t, flow, "", "")

	services, _ := flow.Step()
	if services.ID != "runtime.services" || services.Proposed != "dev worker" {
		t.Fatalf("the services question proposes %q, want what the files declare", services.Proposed)
	}
	answer(t, flow, "dev worker")

	// The mount point is read from the files rather than asked blind, and the
	// service whose image bakes its code is named while the user can still act
	// on it.
	mount, _ := flow.Step()
	if mount.ID != "runtime.mount" || mount.Proposed != "/srv" {
		t.Fatalf("the runtime mount question proposes %q, want what the files mount", mount.Proposed)
	}
	if !strings.Contains(strings.Join(mount.Notes, " "), "worker") {
		t.Errorf("the service built from this repository is not reported: %v", mount.Notes)
	}
	answer(t, flow, "")

	reachable, _ := flow.Step()
	if reachable.ID != "runtime.reachable" || reachable.Proposed != "dev" {
		t.Fatalf("the reachable question proposes %q, want the service that publishes a port",
			reachable.Proposed)
	}
	if err := flow.Answer(context.Background(), "postgres"); err == nil {
		t.Error("a service the repository does not manage was accepted as reachable")
	}
	answers(t, flow,
		"", // reachable: the proposal
		"", // no environment file
		"", // no check
	)

	if !flow.Complete() {
		question, _ := flow.Step()
		t.Fatalf("the flow is still asking %s", question.ID)
	}
	review, err := flow.Review()
	if err != nil {
		t.Fatalf("the answers do not compose a configuration: %v", err)
	}
	text := string(review.Text)
	for _, want := range []string{
		// The contribution is on the repository, and its Compose file is named
		// the way that repository would name it: relative to its own checkout.
		"    runtime:\n", "      - compose.yaml", "      container_path: /srv",
		"        - dev\n", "      reachable:", "runtime:\n  provider: compose",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the configuration does not contain %q:\n%s", want, text)
		}
	}
	// Nothing about the agent's own container: there is none.
	if strings.Contains(text, "agent:\n      container_path") {
		t.Errorf("a host-execution project was given an agent container path:\n%s", text)
	}
}

// TestBlankFinishesAFileLoop is the defect a real run found.
//
// A loop cannot both propose the next file and finish on an empty answer: they
// are two meanings for one key, and finishing lost. A user pressing Enter at
// "Compose file (blank to finish) [/some/path]" accepted the bracketed path
// instead — twice — and ended up with an application's Compose files defining
// the container their agent runs in, reported back as a service list they did
// not recognise.
//
// So a loop proposes only its first, and Enter after that means what the prompt
// says it means.
func TestBlankFinishesAFileLoop(t *testing.T) {
	flow, _ := start(t, "app")
	answers(t, flow,
		"",    // display name
		"",    // the checkout: the working directory, which is the api repository
		"api", // repository identifier
		"",    // default access: read_write
		"n",   // no second repository
		"",    // execution mode: host
		"y",   // the project runs application services
		"y",   // api brings Compose files
	)

	proposed, _ := flow.Step()
	if proposed.Proposed == "" {
		t.Fatal("the first file of a loop is not proposed, and the repository has one beside it")
	}
	answer(t, flow, "")

	next, _ := flow.Step()
	if next.ID != "runtime.compose" {
		t.Fatalf("the loop moved to %s after one file, and it takes more than one", next.ID)
	}
	if next.Proposed != "" {
		t.Errorf("the loop still proposes %q, so an empty answer cannot mean 'no more'", next.Proposed)
	}
	if !next.Optional {
		t.Error("the loop's continuation is not optional, so it cannot be finished")
	}

	answer(t, flow, "")
	services, _ := flow.Step()
	if services.ID != "runtime.services" {
		t.Fatalf("an empty answer did not finish the loop; the flow is asking %s", services.ID)
	}

	// The one file answered is the one recorded — not it plus whatever was in
	// the brackets when Enter was pressed.
	answers(t, flow, "dev worker", "", "", "", "")
	review, err := flow.Review()
	if err != nil {
		t.Fatalf("the answers do not compose a configuration: %v", err)
	}
	if got := strings.Count(string(review.Text), "compose.yaml"); got != 1 {
		t.Errorf("%d Compose files were recorded, want the one that was answered:\n%s", got, review.Text)
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
