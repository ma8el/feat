package wizard

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
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
		return Checkout{
			Root: path, Remote: "origin",
			// A host Feat recognises, which is what the forge question proposes
			// from. The repository beside it has no remote at all, so the two
			// cover both halves of that proposal (ADR-100).
			RemoteURL:     "git@github.com:acme/api.git",
			DefaultBranch: "main",
		}, nil
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
			// The agent's own, in the directory the Dev Containers specification
			// keeps one in. It is what is left over once the application has
			// claimed the two above it.
			filepath.Join(dir, ".devcontainer", "compose.yaml"),
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
// The api checkout's own files declare three services: one mounting it at /srv
// and publishing a port, one built from it, and a database that runs none of its
// code and is therefore not something a task manages. The devcontainer kept in a
// directory of its own mounts api at /opt/api and says nothing Feat can read
// about the store checkout — an interpolated source is a value Feat must not
// resolve, so it is named rather than derived from.
func (h *fakeHost) Compose(projectDir, repository string, files ...string) Composition {
	h.composed = append(h.composed, composeCall{projectDir: projectDir, repository: repository})
	if len(files) == 0 {
		return Composition{}
	}

	// Either devcontainer: the one kept in a directory of its own, and the one
	// kept beside the api checkout in the directory the specification names.
	switch filepath.Base(projectDir) {
	case "devcontainer", ".devcontainer":
		if repository != filepath.Join(h.root, "api") {
			return Composition{Undecided: []string{files[0] + ": service dev: a volume"}}
		}
		return Composition{Services: []string{"dev"}, ContainerPath: "/opt/api"}
	}

	if repository != filepath.Join(h.root, "api") {
		return Composition{}
	}
	return Composition{
		Services:      []string{"db", "dev", "worker"},
		ContainerPath: "/srv",
		Reachable:     []string{"dev"},
		Mounted:       []string{"dev"},
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
		"",        // forge: github, read from the remote rather than asked for
		"n",       // no second repository
		"n",       // no application services
		"",        // execution mode: host
		"",        // no tracker command
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
		// All three were read from the checkout rather than asked for, which is
		// what the wizard exists to do. The forge is the third: the remote's host
		// is one Feat recognises, so the question proposed it and Enter took it
		// (ADR-100).
		"default_branch: main", "remote: origin", "kind: github",
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
		// Display name, the checkout, its identifier, its access, its forge, no
		// second repository, no application services, and then the mode.
		answers(t, flow, "", "", "api", "", "", "n", "n", mode)
		if mode == config.ModeDevcontainer {
			answers(t, flow,
				filepath.Join(host.root, "api", "compose.yaml"), "", "dev", "developer", "/srv/api", "n")
		}
		answers(t, flow, "") // no tracker command, which is where the checks used to be

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
		"",      // its identifier
		"",      // default access
		"",      // forge: none, because there is no remote to read one from
		"n",     // no second repository
	)

	// It says so where it is decided, rather than deciding quietly.
	question, _ := flow.Step()
	if !strings.Contains(strings.Join(question.Notes, " "), "has a remote") {
		t.Errorf("the notes do not say why: %v", question.Notes)
	}

	answers(t, flow, "", "", "")
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
	answers(t, flow, "app", "", "", "api", "read_write", "", "y")

	// A second repository was asked for; stepping back twice returns to the
	// question that asked for it, and then to the first repository's forge.
	if !flow.Back() {
		t.Fatal("nothing to step back to after seven answers")
	}
	question, _ := flow.Step()
	if question.ID != "repository.another" {
		t.Fatalf("stepping back reached %s, want the question that was answered", question.ID)
	}

	// Answering it the other way now composes a project with one repository,
	// with nothing of the second left in it.
	answers(t, flow, "n", "", "", "")
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
	answers(t, flow, "app", "", "", "api", "", "", "n", "n", "devcontainer")

	// What is proposed is what is left. The application was asked about first,
	// so the files it claimed are out of this list, and the one in a
	// `.devcontainer` directory heads what remains — which is where a project
	// following the specification keeps the thing this question is about.
	question, _ := flow.Step()
	if question.ID != "agent.compose" {
		t.Fatalf("the first devcontainer question is %s", question.ID)
	}
	if want := filepath.Join(host.root, "api", devcontainerDir, "compose.yaml"); question.Proposed != want {
		t.Errorf("the devcontainer's Compose question proposes %q, want %q", question.Proposed, want)
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
	answers(t, flow, "developer", "/srv/api", "y", "feat-claude", "")

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
		"",             // forge: github, read from the remote
		"y",            // a second repository
		"store",        // its checkout
		"store",        // its identifier
		"",             // default access: selectable
		"",             // forge: none, because it has no remote
		"n",            // no third repository
		"",             // the repository a task works in: api
		"n",            // no application services
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
		"",  // no tracker command
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

// TestAMountInsideAnotherRepositorysMountIsRefusedWhereItIsGiven is the failure
// the derived proposal made reachable.
//
// Two proposals of "/srv/<id>" are always siblings, so before the mount could be
// read out of the Compose files, the wizard could not compose an overlapping
// pair. A path the files state can be the parent of the next repository's
// default — and configuration refuses two repositories mounted inside one
// another when the composed file is loaded back, which is after the last
// question. Meeting it there ends the conversation and takes every answer with
// it, so it is met here instead.
func TestAMountInsideAnotherRepositorysMountIsRefusedWhereItIsGiven(t *testing.T) {
	flow, host := start(t, "app")
	answers(t, flow,
		"",             // display name
		"",             // the checkout: the working directory, which is the api repository
		"api",          // repository identifier
		"",             // default access: read_write
		"",             // forge: github, read from the remote
		"y",            // a second repository
		"store",        // its checkout
		"store",        // its identifier
		"",             // default access: selectable
		"",             // forge: none, because it has no remote
		"n",            // no third repository
		"",             // the repository a task works in: api
		"n",            // no application services
		"devcontainer", // execution mode
		filepath.Join(host.root, "devcontainer", "compose.yaml"),
		"",          // no more Compose files
		"dev",       // the service the agent runs in
		"developer", // the user it runs as
		"/srv",      // api, mounted where the second repository's default would go
	)

	// The default for the second repository is inside the first one's mount, so
	// the answer the field already holds is the one refused.
	question, _ := flow.Step()
	if question.ID != "agent.mount" || question.Proposed != "/srv/store" {
		t.Fatalf("the question is %s proposing %q, want the default for the second repository",
			question.ID, question.Proposed)
	}
	err := flow.Answer(context.Background(), "")
	if err == nil {
		t.Fatal("a mount inside another repository's mount was accepted")
	}
	for _, want := range []string{"/srv/store", "/srv", "api"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}

	// And the conversation is where it was, with the same question waiting, which
	// is the whole point of asking it here.
	again, ok := flow.Step()
	if !ok || again.ID != "agent.mount" {
		t.Fatalf("a refused mount moved the flow to %s", again.ID)
	}
	answers(t, flow,
		"/opt/store", // somewhere else entirely
		"n",          // no configuration volume for Claude
		"",           // no tracker command
	)

	review, err := flow.Review()
	if err != nil {
		t.Fatalf("the corrected answers do not compose a configuration: %v", err)
	}
	for _, want := range []string{"container_path: /srv\n", "container_path: /opt/store\n"} {
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
// own Compose files, read structurally.
//
// It is asked before the execution mode is known at all, which is the strongest
// form of what ADR-065 evidence 6 asks for: the runtime container path used to
// be skipped for a host-native agent, and now the question cannot even see which
// mode this project will run in.
func TestTheApplicationIsAnsweredOneRepositoryAtATime(t *testing.T) {
	flow, _ := start(t, "app")
	answers(t, flow,
		"",    // display name
		"",    // the checkout: the working directory, which is the api repository
		"api", // repository identifier
		"",    // default access: read_write
		"",    // forge: github, read from the remote
		"n",   // no second repository
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

	// The services that run this repository's code, and not every service its
	// files declare: the database among them runs none of it, so a user
	// accepting the proposal would have been managing more than the project
	// meant (ADR-100).
	services, _ := flow.Step()
	if services.ID != "runtime.services" || services.Proposed != "dev worker" {
		t.Fatalf("the services question proposes %q, want the services that run this "+
			"repository's code", services.Proposed)
	}
	notes := strings.Join(services.Notes, " ")
	if !strings.Contains(notes, "db, dev, worker") {
		t.Errorf("the services the files declare are not all reported: %v", services.Notes)
	}
	if !strings.Contains(notes, "not proposed") || !strings.Contains(notes, "depend on") {
		t.Errorf("the service left out of the proposal is not explained: %v", services.Notes)
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
		"", // execution mode: host, which has no agent container at all
		"", // no tracker command
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
		"",    // forge: github, read from the remote
		"n",   // no second repository
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
	answers(t, flow, "dev worker", "", "", "", "", "")
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
		"app", "Example", "", "", "", "", "n", "n", "", "",
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
		"",    // forge: github, read from the remote
		"n",   // no second repository
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
			answers: []string{"", "", "api", "", "", "n", "n", "devcontainer"},
			id:      "agent.compose",
			repeat:  "Compose override file (blank to finish)",
		},
		{
			name:    "a repository's part of the application",
			answers: []string{"", "", "api", "", "", "n", "y", "y"},
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
	answers(t, flow, "", "", "api", "", "", "n", "n", "", "")

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
	answers(t, flow, "app", "", "", "api", "", "", "n", "n", "", "")

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

// TestEveryMountQuestionSaysWhatMakesAnAnswerCorrect is the guidance both
// container paths were decided without.
//
// One rule decides both: Compose merges a service's volumes on the target, and
// Feat's generated override is merged last, so an answer matching a target the
// project's own files already mount at replaces that mount and an answer
// matching nothing adds a second one beside it. It is now on every mount
// question of both groups rather than on the first of each — the departure
// ADR-082 records — because the failure is silent and the sentence separating
// the agent's container from the application's services is needed most at the
// second repository, which under the convention is asked nothing but "Mount
// point for store".
func TestEveryMountQuestionSaysWhatMakesAnAnswerCorrect(t *testing.T) {
	flow, host := start(t, "app")
	answers(t, flow,
		"",      // display name
		"",      // the checkout: the working directory, which is the api repository
		"api",   // repository identifier
		"",      // default access: read_write
		"",      // forge: github, read from the remote
		"y",     // a second repository
		"store", // its checkout
		"store", // its identifier
		"",      // default access: selectable
		"",      // forge: none, because it has no remote
		"n",     // no third repository
		"",      // the repository a task works in: api
		"y",     // the project runs application services
		"y",     // api brings Compose files
		"",      // the file it proposes
		"",      // no more of them
		"",      // the services it declares
	)

	first, _ := flow.Step()
	if first.ID != "runtime.mount" {
		t.Fatalf("the question after api's services is %s, want where they expect its source", first.ID)
	}
	mustCarryTheMergeRule(t, first, "api")
	answers(t, flow,
		"",  // the mount point the files state
		"",  // reachable: the service that publishes a port
		"y", // store brings Compose files too
		filepath.Join(host.root, "store", "compose.yaml"),
		"",    // no more of them
		"web", // its services, which those files do not declare
	)

	second, _ := flow.Step()
	if second.ID != "runtime.mount" {
		t.Fatalf("the question after store's services is %s, want where they expect its source", second.ID)
	}
	mustCarryTheMergeRule(t, second, "store")

	answers(t, flow,
		"",             // store's mount point: blank, because its files state none
		"",             // reachable: none of them
		"",             // no environment file
		"devcontainer", // execution mode
		filepath.Join(host.root, "devcontainer", "compose.yaml"),
		"",          // no more Compose files
		"dev",       // the service the agent runs in
		"developer", // the user it runs as
	)

	// Both repositories, and not the first alone.
	for _, repository := range []string{"api", "store"} {
		mount, _ := flow.Step()
		if mount.ID != "agent.mount" {
			t.Fatalf("the agent's mount for %s is asked as %s", repository, mount.ID)
		}
		mustCarryTheMergeRule(t, mount, repository)
		answer(t, flow, "")
	}
}

// theMergeRule is what each group has to keep saying: an override that replaces
// a mount only where the answer is the mount point already used, and two live
// mounts where it is not. Both groups say it in the same words, and the runtime
// group says one thing more, because it is the half only that field needs.
var theMergeRule = map[string][]string{
	"agent.mount":   {"override", "exact same mount point", "simultaneously"},
	"runtime.mount": {"override", "exact same mount point", "simultaneously", "no error anywhere"},
}

// mustCarryTheMergeRule fails unless a mount question states the rule an answer
// to it is right or wrong by.
//
// The words rather than the presence of a block, because a detail that had been
// shortened into saying only what the field is would be the gap again with a
// test passing over it: what has to survive is the override, the path it
// replaces a mount at, and what an answer matching nothing produces instead.
func mustCarryTheMergeRule(t *testing.T, question Question, repository string) {
	t.Helper()

	if len(question.Detail) == 0 {
		t.Fatalf("%s for %s carries no detail at all", question.ID, repository)
	}
	// The first of a group opens with what the field is, breaks, and then warns.
	// The break is a line of its own, because a warning running on from the
	// sentence above it is a warning nobody stops at. A repeat opens with the
	// warning and has nothing to break from.
	if !strings.HasPrefix(question.Detail[0], "WARNING") && question.Detail[1] != "" {
		t.Errorf("%s for %s runs its warning on from the sentence above it:\n%v",
			question.ID, repository, question.Detail)
	}
	detail := strings.Join(question.Detail, " ")
	for _, want := range theMergeRule[question.ID] {
		if !strings.Contains(detail, want) {
			t.Errorf("%s for %s does not say %q:\n%s", question.ID, repository, want, detail)
		}
	}
}

// TestAProposalReadFromAComposeFileNamesTheFile is gap 3 of the same finding,
// and the smallest of it.
//
// The flow reported its readings only in the negative: an entry it could not
// read was named, and a path it transcribed out of the user's own file arrived
// as a proposal with nothing beside it. So the two proposals a user must treat
// differently — a transcription, which is always right to accept, and a path
// Feat made up, which is right only where those files mount the repository
// nowhere — looked identical (ADR-082).
func TestAProposalReadFromAComposeFileNamesTheFile(t *testing.T) {
	flow, host := start(t, "app")
	agentFile := filepath.Join(host.root, "devcontainer", "compose.yaml")
	answers(t, flow,
		"", "", "api", "", "", "y", "store", "store", "", "", "n", "",
		"y", // the project runs application services
		"y", // api brings Compose files
		"",  // the file it proposes
		"",  // no more of them
		"",  // the services it declares
	)

	// The runtime's own proposal, read out of the files answered two questions
	// ago, names the file it came out of.
	runtime, _ := flow.Step()
	notes := strings.Join(runtime.Notes, " ")
	if runtime.ID != "runtime.mount" || runtime.Proposed != "/srv" {
		t.Fatalf("the runtime mount is %s proposing %q", runtime.ID, runtime.Proposed)
	}
	if !strings.Contains(notes, "read from") ||
		!strings.Contains(notes, filepath.Join(host.root, "api", "compose.yaml")) {
		t.Errorf("the file api's runtime mount point was read from is not named: %v", runtime.Notes)
	}

	answers(t, flow,
		"",  // the mount point those files state
		"",  // reachable: the service that publishes a port
		"n", // store brings no Compose files
		"",  // no environment file
		"devcontainer", agentFile,
		"",          // no more Compose files
		"dev",       // the service the agent runs in
		"developer", // the user it runs as
	)

	// The agent's derivation names the file it came out of too.
	mount, _ := flow.Step()
	notes = strings.Join(mount.Notes, " ")
	if mount.Proposed != "/opt/api" {
		t.Fatalf("the mount for api proposes %q, want the path those files mount it at", mount.Proposed)
	}
	if !strings.Contains(notes, "read from") || !strings.Contains(notes, agentFile) {
		t.Errorf("the file api's mount point was read from is not named: %v", mount.Notes)
	}
	answer(t, flow, "")

	// And Feat's own default does not claim to have been read from anywhere.
	second, _ := flow.Step()
	notes = strings.Join(second.Notes, " ")
	if second.Proposed != "/srv/store" {
		t.Fatalf("the mount for store proposes %q, want Feat's own default", second.Proposed)
	}
	if strings.Contains(notes, "read from") {
		t.Errorf("a path Feat made up is reported as read out of a file: %v", second.Notes)
	}
	if !strings.Contains(notes, "interpolate") {
		t.Errorf("the entry left unread is not named beside the default it caused: %v", second.Notes)
	}
}

// TestTheForgeIsProposedFromTheRemoteAndAskedOtherwise is the inference ADR-071
// allows, made in the one place it can be made.
//
// Configuration is loaded without Git, so nothing downstream can look at a
// remote; the wizard can, and what it does with it is propose. A host Feat
// recognises proposes its forge and says where that came from; a host it does
// not — a self-hosted instance, which is most of why this field is declared —
// proposes none and says that it read the remote and could not tell.
func TestTheForgeIsProposedFromTheRemoteAndAskedOtherwise(t *testing.T) {
	flow, _ := start(t, "app")
	answers(t, flow, "", "", "api", "")

	question, _ := flow.Step()
	if question.ID != "repository.forge" {
		t.Fatalf("the question after a repository's access is %s, want its forge", question.ID)
	}
	if question.Proposed != "github" {
		t.Errorf("the forge for a github.com remote is proposed as %q", question.Proposed)
	}
	if notes := strings.Join(question.Notes, " "); !strings.Contains(notes, "read from origin") {
		t.Errorf("the proposal does not say where it came from: %v", question.Notes)
	}
	// Offered rather than assumed: a repository on a recognised host that Feat
	// should never publish is still one answer away.
	if !containsString(question.Options, noForge) {
		t.Errorf("the question offers %v, and a repository may publish nowhere", question.Options)
	}

	// The second repository has no remote at all, so there is nothing to read
	// and nothing is claimed to have been read.
	answers(t, flow, "", "y", "store", "store", "")
	second, _ := flow.Step()
	if second.ID != "repository.forge" || second.Proposed != noForge {
		t.Fatalf("the forge for a repository with no remote is %s proposing %q",
			second.ID, second.Proposed)
	}
	if notes := strings.Join(second.Notes, " "); strings.Contains(notes, "read from") {
		t.Errorf("a repository with no remote reports a forge read from one: %v", second.Notes)
	}

	answers(t, flow, "", "n", "", "n", "", "")
	review, err := flow.Review()
	if err != nil {
		t.Fatalf("the answers do not compose a configuration: %v", err)
	}
	// One forge section, on the repository that answered one. "none" is the
	// absence of the section rather than a value in it.
	text := string(review.Text)
	if strings.Count(text, "forge:") != 1 || !strings.Contains(text, "kind: github") {
		t.Errorf("the forge sections are not the one that was answered:\n%s", text)
	}
}

// TestTheForgeQuestionOffersExactlyWhatConfigurationAccepts pins the two lists
// together.
//
// A question offering a forge the configuration refuses would be a conversation
// that composes a file Feat will not load; one missing a forge it accepts would
// be a field the wizard cannot reach. Both are read off domain.ForgeKinds, and
// this is what says so out loud (ADR-094's form, ADR-100's case).
func TestTheForgeQuestionOffersExactlyWhatConfigurationAccepts(t *testing.T) {
	options := forgeOptions()
	if got := options[len(options)-1]; got != noForge {
		t.Errorf("the last option is %q, want the answer for a repository Feat never publishes", got)
	}

	offered := options[:len(options)-1]
	for _, kind := range offered {
		if !domain.ForgeKind(kind).Valid() {
			t.Errorf("the question offers %q, which configuration refuses", kind)
		}
	}
	for _, kind := range domain.ForgeKinds() {
		if !containsString(offered, string(kind)) {
			t.Errorf("configuration accepts %q and the question does not offer it: %v", kind, offered)
		}
	}
	if containsString(offered, noForge) {
		t.Errorf("%q is offered as a forge kind, and it is the absence of one", noForge)
	}
}

// TestTheHostOfARemoteIsReadInEitherFormOrNotAtAll covers the parsing the
// proposal rests on, including the forms that name no host.
func TestTheHostOfARemoteIsReadInEitherFormOrNotAtAll(t *testing.T) {
	for _, tc := range []struct {
		remote string
		forge  string
	}{
		{"https://github.com/acme/api.git", "github"},
		{"git@github.com:acme/api.git", "github"},
		{"ssh://git@github.com/acme/api.git", "github"},
		{"https://GitHub.com/acme/api", "github"},
		{"git@gitlab.com:acme/api.git", "gitlab"},
		{"https://gitlab.com:8443/acme/api.git", "gitlab"},
		// A self-hosted instance, which is not guessable and is the case the
		// declaration exists for (ADR-071).
		{"git@gitlab.example.com:acme/api.git", ""},
		{"https://github.acme.example/acme/api.git", ""},
		// Neither a forge nor a mistake: a remote may be a directory.
		{"/srv/mirrors/api.git", ""},
		{"../api.git", ""},
		{"", ""},
	} {
		if got := forgeFor(tc.remote); got != tc.forge {
			t.Errorf("%q proposes the forge %q, want %q", tc.remote, got, tc.forge)
		}
	}
}

// TestTheTrackerIsAskedLastAndIsOptional is the question that may have no answer
// yet.
//
// A tracker is a command the user writes, so demanding one would put a thing to
// go away and build in the middle of configuring a project. It is asked once,
// last, and an empty answer is an answer: the section can be added to the file
// afterwards, which is the case ADR-093 gives the skill (ADR-100).
func TestTheTrackerIsAskedLastAndIsOptional(t *testing.T) {
	blank, _ := start(t, "app")
	answers(t, blank, "", "", "api", "", "", "n", "n", "")

	question, ok := blank.Step()
	if !ok || question.ID != "tracker.command" {
		t.Fatalf("the last question is %s, want the tracker command", question.ID)
	}
	if !question.Optional || question.Proposed != "" {
		t.Errorf("the tracker question is optional=%v proposing %q, and a project may have none",
			question.Optional, question.Proposed)
	}
	if question.Section != SectionTracker || question.Heading == "" {
		t.Errorf("the tracker question is in section %q under heading %q",
			question.Section, question.Heading)
	}
	answer(t, blank, "")
	if text := string(mustReview(t, blank).Text); strings.Contains(text, "tracker:") {
		t.Errorf("a blank answer wrote a tracker section:\n%s", text)
	}

	// And an answer is the argument vector the configuration holds, split the
	// way the user typed it: a quoted argument is one word, because the command
	// is run directly and a shell is never involved.
	given, _ := start(t, "app")
	answers(t, given, "", "", "api", "", "", "n", "n", "",
		`feat-tickets --query 'assigned to me' --json`)

	text := string(mustReview(t, given).Text)
	for _, want := range []string{
		"tracker:", "- feat-tickets", "- --query", "- assigned to me", "- --json",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the tracker command does not hold %q:\n%s", want, text)
		}
	}
	// The kind is resolution's, so a generated file does not state it.
	if strings.Contains(text, "kind: command") {
		t.Errorf("the wizard wrote the tracker kind, which is a default:\n%s", text)
	}
}

// TestATrackerCommandThatCannotBeReadIsRefusedWhereItWasTyped is the rejection
// that has to happen at the question rather than at the end of the run.
//
// A quote that never closes and a program that is not named both compose a
// configuration Feat refuses, and meeting that refusal after the last question
// ends the conversation and takes every answer with it — which is the failure
// the mount questions already learned from.
func TestATrackerCommandThatCannotBeReadIsRefusedWhereItWasTyped(t *testing.T) {
	flow, _ := start(t, "app")
	answers(t, flow, "", "", "api", "", "", "n", "n", "")

	for _, tc := range []struct{ answer, says string }{
		{`feat-tickets --query 'assigned to me`, "never closed"},
		{`""`, "name the program"},
	} {
		err := flow.Answer(context.Background(), tc.answer)
		if err == nil {
			t.Fatalf("%q was accepted as a tracker command", tc.answer)
		}
		if !strings.Contains(err.Error(), tc.says) {
			t.Errorf("the refusal of %q does not say why: %v", tc.answer, err)
		}
		again, ok := flow.Step()
		if !ok || again.ID != "tracker.command" {
			t.Fatalf("a refused tracker command moved the flow to %s", again.ID)
		}
	}
}

// TestTheAgentsComposeQuestionProposesWhatTheApplicationLeft is finding 4 of the
// second pass, which is the one that reorders the rest.
//
// The agent's environment used to be answered before the application's, so this
// question could not tell a devcontainer's Compose file from an application's
// and honestly proposed neither. The application is asked first now, so what is
// left is a list — and the file in a `.devcontainer` directory heads it
// (ADR-100).
func TestTheAgentsComposeQuestionProposesWhatTheApplicationLeft(t *testing.T) {
	flow, host := start(t, "app")
	claimed := filepath.Join(host.root, "api", "compose.yaml")
	answers(t, flow,
		"",      // display name
		"",      // the checkout: the api repository
		"api",   // its identifier
		"",      // default access: read_write
		"",      // forge: github
		"n",     // no second repository
		"y",     // the project runs application services
		"y",     // api brings Compose files
		claimed, // the base file, which the application now owns
		"",      // no more of them
		"dev",   // the service it manages
		"/srv",  // where that service expects its source
		"",      // reachable: none
		"",      // no environment file
		"devcontainer",
	)

	question, _ := flow.Step()
	if question.ID != "agent.compose" {
		t.Fatalf("the first devcontainer question is %s", question.ID)
	}
	want := filepath.Join(host.root, "api", devcontainerDir, "compose.yaml")
	if question.Proposed != want {
		t.Errorf("the agent's Compose question proposes %q, want %q", question.Proposed, want)
	}
	// And the file the application took is neither proposed nor offered, with
	// the reason said out loud: a file Feat withheld and a file Feat never found
	// are otherwise the same absence.
	if containsString(question.Candidates, claimed) {
		t.Errorf("the file the application claimed is offered for the agent: %v", question.Candidates)
	}
	notes := strings.Join(question.Notes, " ")
	if !strings.Contains(notes, "application already claims them") || !strings.Contains(notes, claimed) {
		t.Errorf("the question does not say which files were left out: %v", question.Notes)
	}
}

// TestTheSectionsAreAskedInTheOrderTheyAreNamed checks the two halves of the
// reorder together: the path an asker draws, and the questions behind it.
//
// The application before the agent is what lets the question above propose
// anything at all, and the tracker last is what keeps a command the user may not
// have written out of the middle of the run (ADR-100).
func TestTheSectionsAreAskedInTheOrderTheyAreNamed(t *testing.T) {
	want := []Section{
		SectionProject, SectionRepositories, SectionServices, SectionAgent, SectionTracker,
	}
	if !slices.Equal(Sections(), want) {
		t.Errorf("the sections are %v, want %v", Sections(), want)
	}

	flow, _ := start(t, "")
	var asked []string
	var sections []Section
	for _, value := range []string{"app", "", "", "api", "", "", "n", "n", "", ""} {
		question, ok := flow.Step()
		if !ok {
			t.Fatalf("answering %q, but every question has been answered", value)
		}
		asked = append(asked, question.ID)
		if len(sections) == 0 || sections[len(sections)-1] != question.Section {
			sections = append(sections, question.Section)
		}
		answer(t, flow, value)
	}

	wantAsked := []string{
		"project.id", "project.name",
		"repository.path", "repository.id", "repository.access", "repository.forge",
		"repository.another", "runtime.wanted", "agent.mode", "tracker.command",
	}
	if !slices.Equal(asked, wantAsked) {
		t.Errorf("the questions asked were\n%v\nwant\n%v", asked, wantAsked)
	}
	// Each section is entered once and in the order it is named, so the trail an
	// asker draws never goes backwards.
	if !slices.Equal(sections, want) {
		t.Errorf("the sections were entered as %v, want %v", sections, want)
	}
}

// TestARepositoryNoTaskCanWriteToIsNotAskedWhereItPublishes is the answer that
// would have been unreachable configuration.
//
// Four of the five access modes can become read-write in some task — omitted,
// selectable, and stable_read_only all permit it once the repository is
// explicitly selected — so a forge may yet be used and the question is worth
// asking. read_only is the one that cannot: a repository a project declared
// read-only must not become writable because one task asked, and publication
// refuses a binding that is not read-write everywhere it looks at one.
//
// It would not have been inert, which is what makes it worth refusing rather
// than tolerating. `feat doctor` collects the forges every repository declares
// without looking at access, so accepting the proposal on a read-only
// repository whose remote is on github.com buys a standing warning demanding a
// command line for a repository that can never use it.
func TestARepositoryNoTaskCanWriteToIsNotAskedWhereItPublishes(t *testing.T) {
	flow, _ := start(t, "app")
	answers(t, flow,
		"",    // display name
		"",    // the checkout: the api repository, whose remote is on github.com
		"api", // its identifier
		string(domain.DefaultAccessReadOnly),
	)

	question, _ := flow.Step()
	if question.ID != "repository.another" {
		t.Fatalf("a read-only repository was asked %s, and no task can publish it", question.ID)
	}

	// Every other mode is asked, including the three that take a decision per
	// task: a repository selected read-write once has a merge request to open.
	for _, access := range []domain.DefaultAccess{
		domain.DefaultAccessReadWrite,
		domain.DefaultAccessSelectable,
		domain.DefaultAccessStableReadOnly,
		domain.DefaultAccessOmitted,
	} {
		asked, _ := start(t, "app")
		answers(t, asked, "", "", "api", string(access))

		if question, _ := asked.Step(); question.ID != "repository.forge" {
			t.Errorf("a %s repository is asked %s, and a task may yet write to it",
				access, question.ID)
		}
	}
}

// TestTheForgeExplanationLandsOnTheFirstRepositoryAskedForOne is the detail
// block following the group rather than the repositories.
//
// A project whose first repository is read-only never sees that question, so
// counting repositories would have put the explanation on nothing and left the
// repository that was asked with a bare prompt.
func TestTheForgeExplanationLandsOnTheFirstRepositoryAskedForOne(t *testing.T) {
	flow, _ := start(t, "app")
	answers(t, flow,
		"",                                    // display name
		"",                                    // the api checkout
		"api",                                 // its identifier
		string(domain.DefaultAccessReadOnly),  // never published, so never asked
		"y",                                   // a second repository
		"store",                               // its checkout
		"store",                               // its identifier
		string(domain.DefaultAccessReadWrite), // this one a task may write to
	)

	question, _ := flow.Step()
	if question.ID != "repository.forge" {
		t.Fatalf("the second repository is asked %s, want where it publishes", question.ID)
	}
	if len(question.Detail) == 0 {
		t.Errorf("the first repository asked where it publishes gets no explanation: %+v", question)
	}
}

// TestTheOnlyEditableRepositoryIsPromotedWithoutItsForgeBeingAsked is the gap
// ADR-100 states rather than closes, checked so that it stays the one it says.
//
// A project whose repositories are all read-only has no editable workspace, so
// the flow asks which one a task may edit and promotes it. That repository was
// never asked where it publishes, because when it was answered it was one no
// task could write to. What comes out is a configuration Feat accepts with an
// optional section missing, and not a broken one.
func TestTheOnlyEditableRepositoryIsPromotedWithoutItsForgeBeingAsked(t *testing.T) {
	flow, _ := start(t, "app")
	answers(t, flow,
		"",    // display name
		"",    // the api checkout
		"api", // its identifier
		string(domain.DefaultAccessReadOnly),
		"n", // no second repository
	)

	question, _ := flow.Step()
	if question.ID != "project.editable" {
		t.Fatalf("a project with no editable repository is asked %s", question.ID)
	}
	answers(t, flow, "api", "n", "", "")

	review, err := flow.Review()
	if err != nil {
		t.Fatalf("the promoted repository does not compose a configuration: %v", err)
	}
	text := string(review.Text)
	if !strings.Contains(text, "default_access: read_write") {
		t.Errorf("the repository a task may edit was not promoted:\n%s", text)
	}
	if strings.Contains(text, "forge:") {
		t.Errorf("a forge was written for a repository that was never asked for one:\n%s", text)
	}
}
