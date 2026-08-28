package wizard

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/paths"
)

// fakeHost answers for a machine with two checkouts on it: one with an ordinary
// remote and two Compose files beside it, and one with no remote at all.
//
// Two files rather than one, because a repository that brings a base and an
// override is the ordinary case and is the one where the flow derives more than
// it can propose.
//
// It answers by path, because that is what the proposals are derived from: a
// host that answered the same thing everywhere could not tell a repository that
// was inspected from one that was assumed.
type fakeHost struct {
	root string
	// inspected counts the directories Git was asked about, which is what makes
	// "it derives rather than asks" checkable.
	inspected int
	// composed records what the Compose reader was asked, because the agent's
	// own files and a repository's application files are read by the same call
	// and what separates them is which two directories it is given.
	composed []composeCall
}

// composeCall is one reading: the directory the paths in those files resolve
// against, and the repository being asked about.
type composeCall struct {
	projectDir string
	repository string
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
		return []string{
			filepath.Join(dir, "compose.yaml"),
			filepath.Join(dir, "compose.override.yaml"),
		}
	}
	return nil
}

func (h *fakeHost) ComposeServices(files ...string) []string {
	if len(files) == 0 {
		return nil
	}
	return []string{"dev", "worker"}
}

// Compose answers what one set of Compose files says about one repository.
//
// There are two sets on this machine and they answer differently, which is the
// arrangement rather than an awkward fixture: each repository answers two mount
// questions, and where the agent's own container holds a checkout is a
// different question from where an application's services expect its source.
//
// The api checkout's own files declare two services, one mounting it at /srv
// and publishing a port and one built from it. The devcontainer kept in a
// directory of its own mounts api at /opt/api and says nothing Feat can read
// about the store checkout — an interpolated source is a value Feat must not
// resolve, so it is named rather than derived from.
func (h *fakeHost) Compose(projectDir, repository string, files ...string) Composition {
	h.composed = append(h.composed, composeCall{projectDir: projectDir, repository: repository})
	if len(files) == 0 {
		return Composition{}
	}

	if projectDir == filepath.Join(h.root, "devcontainer") {
		if repository != filepath.Join(h.root, "api") {
			return Composition{Undecided: []string{files[0] + ": service dev: a volume"}}
		}
		return Composition{Services: []string{"dev"}, ContainerPath: "/opt/api"}
	}

	if repository != filepath.Join(h.root, "api") {
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
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the configuration does not contain %q:\n%s", want, text)
		}
	}
	// And nothing about verification. The questions do not ask, so the file does
	// not state one, and a project acquires a gate by being opened in an editor
	// (ADR-078).
	if strings.Contains(text, "checks:") {
		t.Errorf("the wizard wrote a checks block nobody was asked about:\n%s", text)
	}
}

// TestVerificationIsNotAsked is the removal, at the flow that used to ask.
//
// Three questions became none, and the section they belonged to is gone from the
// path an asker draws. What did not change is anything else about `checks:`: the
// configuration model, the schema, the example file, and `feat doctor` all still
// have it, so a hand-written gate works exactly as it did (ADR-078).
func TestVerificationIsNotAsked(t *testing.T) {
	for _, section := range Sections() {
		if section == "checks" {
			t.Errorf("the sections still name verification: %v", Sections())
		}
	}

	// Every question of a whole run, in both execution modes, and none of them is
	// about a command.
	for _, mode := range []string{config.ModeHost, config.ModeDevcontainer} {
		flow, host := start(t, "app")
		answers(t, flow, "", "", "api", "", "n", mode)
		if mode == config.ModeDevcontainer {
			answers(t, flow,
				filepath.Join(host.root, "api", "compose.yaml"), "", "dev", "developer", "/srv/api", "n")
		}
		answers(t, flow, "n") // no application services, which used to be followed by the checks

		if !flow.Complete() {
			question, _ := flow.Step()
			t.Fatalf("%s mode is still asking %s (%q)", mode, question.ID, question.Prompt)
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

	answers(t, flow, "", "")
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
	answers(t, flow, "n", "", "")
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
	answers(t, flow, "developer", "/srv/api", "y", "feat-claude", "n")

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

// TestTheAgentsMountIsProposedFromItsOwnComposeFiles points the agent section at
// the reader the runtime section already uses on the same kind of file.
//
// The agent's files are read against their own first file's directory, which is
// the project directory the daemon passes Compose when it starts the agent, and
// they are asked about each repository in turn: one container holds every
// repository a task takes, and a file can name a different path for each. Where
// they name one, that is the mount point; where they name none, Feat keeps a
// path of its own, because Feat generates this mount itself and is entitled to
// choose where it goes.
func TestTheAgentsMountIsProposedFromItsOwnComposeFiles(t *testing.T) {
	flow, host := start(t, "app")

	// The agent's Compose files are beside neither repository, which is the
	// arrangement the two directories exist for: a devcontainer kept in a
	// repository of its own resolves its own relative paths against itself while
	// answering about repositories somewhere else.
	agentFile := filepath.Join(host.root, "devcontainer", "compose.yaml")
	answers(t, flow,
		"",             // display name
		"",             // the checkout: the working directory, which is the api repository
		"api",          // repository identifier
		"",             // default access: read_write
		"y",            // a second repository
		"store",        // its checkout
		"store",        // its identifier
		"",             // default access: selectable
		"n",            // no third repository
		"",             // the repository a task works in: api
		"devcontainer", // execution mode
		agentFile,
		"",          // no more Compose files
		"dev",       // the service the agent runs in
		"developer", // the user it runs as
	)

	mount, _ := flow.Step()
	if mount.ID != "agent.mount" {
		t.Fatalf("the question after the container user is %s", mount.ID)
	}
	if mount.Proposed != "/opt/api" {
		t.Errorf("the mount for api is proposed as %q, want the path the agent's own files mount "+
			"it at rather than the one its application's files do", mount.Proposed)
	}
	// Read against the files' own directory and asked about the repository. The
	// daemon gives Compose exactly that project directory, so reading them any
	// other way would answer a different question from the one Compose is asked.
	want := composeCall{
		projectDir: filepath.Dir(agentFile),
		repository: filepath.Join(host.root, "api"),
	}
	if got := host.composed[len(host.composed)-1]; got != want {
		t.Errorf("the agent's files were read as %+v, want %+v", got, want)
	}
	answer(t, flow, "")

	// The repository those files say nothing about keeps Feat's own default, and
	// the entry that could not be read is named: a default a user cannot tell
	// from a derivation is a value that appeared out of nowhere.
	second, _ := flow.Step()
	if second.ID != "agent.mount" || second.Proposed != "/srv/store" {
		t.Fatalf("the mount for store is %s proposing %q, want the default /srv/store",
			second.ID, second.Proposed)
	}
	if !strings.Contains(strings.Join(second.Notes, " "), "interpolate") {
		t.Errorf("the entry left unread is not named beside the default it caused: %v", second.Notes)
	}
	answers(t, flow,
		"",  // the default
		"n", // no configuration volume for Claude
		"n", // no application services
	)

	review, err := flow.Review()
	if err != nil {
		t.Fatalf("the answers do not compose a configuration: %v", err)
	}
	for _, want := range []string{"container_path: /opt/api\n", "container_path: /srv/store\n"} {
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
	flow, host := start(t, "app")
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
	// The rest of the files are still offered here, which is where the second one
	// is added from. They are offered and not proposed: that separation is what
	// lets one key mean "no more" and another mean "the next file" (ADR-077).
	remaining := host.ComposeFiles(filepath.Join(host.root, "api"))[1:]
	if !slices.Equal(next.Candidates, remaining) {
		t.Errorf("the repeat offers %v, want the files not answered yet: %v", next.Candidates, remaining)
	}
	// And it says so, because the field is empty here and an empty field on a
	// question about finishing reads as a loop with nothing left in it.
	if !strings.Contains(strings.Join(next.Notes, " "), remaining[0]) {
		t.Errorf("the repeat does not say what is left to add: %v", next.Notes)
	}

	answer(t, flow, "")
	services, _ := flow.Step()
	if services.ID != "runtime.services" {
		t.Fatalf("an empty answer did not finish the loop; the flow is asking %s", services.ID)
	}

	// The one file answered is the one recorded — not it plus whatever was in
	// the brackets when Enter was pressed.
	answers(t, flow, "dev worker", "", "", "")
	review, err := flow.Review()
	if err != nil {
		t.Fatalf("the answers do not compose a configuration: %v", err)
	}
	if got := strings.Count(string(review.Text), "compose.yaml"); got != 1 {
		t.Errorf("%d Compose files were recorded, want the one that was answered:\n%s", got, review.Text)
	}
}

// TestTheProposalIsTheHeadOfTheCandidates is what keeps the two askers the same
// conversation once one of them can complete an answer (ADR-077).
//
// A list whose first entry was not the value an empty answer takes would be two
// answers to one question: the dashboard's Tab would offer one value and the
// conversation's brackets another, and the same question would mean different
// things depending on where it was asked.
func TestTheProposalIsTheHeadOfTheCandidates(t *testing.T) {
	flow, _ := start(t, "")

	for _, value := range []string{
		"app", "Example", "", "", "", "n", "", "n",
	} {
		question, ok := flow.Step()
		if !ok {
			t.Fatalf("answering %q, but every question has been answered", value)
		}
		switch {
		case question.Kind != KindText:
			// A closed question is a list with a cursor on it, and nothing is
			// typed into one.
			if len(question.Candidates) > 0 {
				t.Errorf("%s is answered from %v and offers completions %v",
					question.ID, question.Options, question.Candidates)
			}
		case question.Proposed == "":
			// A question may offer without proposing — that is the loop, where an
			// empty answer means "no more" — so there is nothing to check here.
		case len(question.Candidates) == 0 || question.Candidates[0] != question.Proposed:
			t.Errorf("%s proposes %q and offers %v", question.ID, question.Proposed, question.Candidates)
		}
		answer(t, flow, value)
	}
}

// TestTheFilesBesideARepositoryAreOfferedAndNotOnlyNamed is the derivation that
// had nowhere to go.
//
// The flow finds every Compose file beside a repository, proposes the first, and
// has until now reported the rest as a sentence — so a user who wanted the
// second one read its path off the screen and typed it back in. It is still a
// sentence, for the asker that can only print one, and it is now also a list the
// dashboard can complete from.
func TestTheFilesBesideARepositoryAreOfferedAndNotOnlyNamed(t *testing.T) {
	flow, host := start(t, "app")
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

	compose, _ := flow.Step()
	if compose.ID != "runtime.compose" {
		t.Fatalf("the question is %s, want the repository's Compose files", compose.ID)
	}
	found := host.ComposeFiles(filepath.Join(host.root, "api"))
	if !slices.Equal(compose.Candidates, found) {
		t.Errorf("the question offers %v, want every file found beside the repository: %v",
			compose.Candidates, found)
	}
	// And the sentence is still there. An asker that cannot complete has to be
	// told what was found, and it is the same asker the user reads back later.
	if !strings.Contains(strings.Join(compose.Notes, " "), filepath.Base(found[1])) {
		t.Errorf("the other file is no longer named: %v", compose.Notes)
	}
}

// TestARepeatedFileQuestionAsksForAnOverride is the question that arrived with
// nothing to go on.
//
// Both loops asked for the next file with the same words as the first and
// "(blank to finish)" appended, so the repeat said what to do with it and never
// what it was. A user who has given the one Compose file they know about has no
// reason to think another exists; the prompt names it now, in Compose's own noun.
func TestARepeatedFileQuestionAsksForAnOverride(t *testing.T) {
	for _, loop := range []struct {
		name    string
		answers []string
		id      string
		repeat  string
	}{
		{
			name:    "the agent's own container",
			answers: []string{"", "", "api", "", "n", "devcontainer"},
			id:      "agent.compose",
			repeat:  "Compose override file (blank to finish)",
		},
		{
			name:    "a repository's part of the application",
			answers: []string{"", "", "api", "", "n", "", "y", "y"},
			id:      "runtime.compose",
			// Named, because this loop runs once per repository and the file
			// belongs to whichever it is on.
			repeat: "Compose override file for api (blank to finish)",
		},
	} {
		t.Run(loop.name, func(t *testing.T) {
			flow, host := start(t, "app")
			answers(t, flow, loop.answers...)

			opening, _ := flow.Step()
			if opening.ID != loop.id {
				t.Fatalf("the question is %s, want %s", opening.ID, loop.id)
			}
			answer(t, flow, filepath.Join(host.root, "api", "compose.yaml"))

			repeat, _ := flow.Step()
			if repeat.ID != loop.id {
				t.Fatalf("one file moved the loop to %s", repeat.ID)
			}
			if repeat.Prompt != loop.repeat {
				t.Errorf("the repeat asks %q, want %q", repeat.Prompt, loop.repeat)
			}
			if opening.Prompt == repeat.Prompt {
				t.Error("the repeat asks for the same thing as the question before it")
			}

			// And it keeps asking for one. Compose takes as many as a project
			// keeps, so the third question is the second one again rather than a
			// question about a third kind of file.
			answer(t, flow, filepath.Join(host.root, "api", "compose.override.yaml"))
			again, _ := flow.Step()
			if again.ID != loop.id || again.Prompt != loop.repeat {
				t.Errorf("the third question is %s asking %q", again.ID, again.Prompt)
			}
		})
	}
}

// TestAProjectThatIsAlreadyConfiguredIsRefused is the file a user must not lose
// to a command they ran twice.
func TestAProjectThatIsAlreadyConfiguredIsRefused(t *testing.T) {
	flow, host := start(t, "")
	answer(t, flow, "app")
	answers(t, flow, "", "", "api", "", "n", "", "")

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
	answers(t, flow, "app", "", "", "api", "", "n", "", "")

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
