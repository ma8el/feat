package api

// The documents `--json` prints.
//
// Three of the four reading commands print what the daemon already answered
// them with — a task list, a review, a runtime status — so their shapes are the
// ones above and there is one model rather than a second to keep in step. What
// is here is the two that have no shape already: the envelope a list needs to
// be an object, and the resolved project configuration, which `feat project
// show` reads from the configuration directory rather than over the socket.
//
// The mapping from a loaded configuration to ProjectConfiguration is the CLI's,
// because the CLI is what loads one. This package describes the shape and does
// not acquire a dependency on internal/config to fill it in.

// TaskList is the document `feat task list --json` prints.
//
// It is an envelope around the array rather than the array itself, so that
// every document this CLI prints is an object and a field can be added to one
// without changing what a parser is looking at.
//
// It carries archived tasks, which the table counts and does not show. Room on
// a screen is why a table hides them; a document has no such limit, and
// selecting on `workflow` is what a script does anyway.
type TaskList struct {
	Tasks []Task `json:"tasks"`
}

// NewTaskList wraps a task list as the document that prints.
func NewTaskList(tasks []Task) TaskList {
	if tasks == nil {
		return TaskList{Tasks: []Task{}}
	}
	return TaskList{Tasks: tasks}
}

// ProjectConfiguration is the document `feat project show --json` prints: the
// configuration Feat will act on, after "~" expansion and after defaults are
// filled.
//
// It is a resolved view rather than the configuration file's own shape. The
// file is what `schema/feat-project.schema.json` describes, and reproducing it
// here would be a second model of the same document to keep in step with the
// first.
//
// Files that may hold secrets appear as paths and never as contents, which is a
// property of what the configuration package holds rather than a filter applied
// here: nothing ever read them.
type ProjectConfiguration struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// PrimaryRepository is where a task works by default.
	PrimaryRepository string `json:"primary_repository"`
	// Repositories is each repository's place on the host, in the agent's
	// container, and in its own services' containers.
	Repositories []ConfiguredRepository `json:"repositories"`
	// Sections are the rest of the resolved configuration, in the order and the
	// words the table prints: a dotted configuration path, the value Feat will
	// use, and a note where one is needed.
	//
	// They are names and values rather than a typed object for the reason the
	// type comment gives. What a caller reads them for is what Feat resolved,
	// and a caller who wants the configuration's own shape has the file.
	Sections []ConfigurationSection `json:"sections"`
}

// ConfiguredRepository is one repository's resolved place.
//
// Every field is present whether or not it has a value, so that a caller can
// read one without asking whether the key is there. An empty agent path is a
// host-native project; an empty runtime path is a repository whose code no
// service runs.
type ConfiguredRepository struct {
	ID string `json:"id"`
	// HostPath is the ordinary checkout, after expansion.
	HostPath string `json:"host_path"`
	// AgentPath is where task worktrees are mounted in the agent's own
	// container, empty for host-native execution.
	AgentPath string `json:"agent_path"`
	// RuntimePath is where this repository's own services expect their source,
	// empty for a repository whose code no service runs.
	RuntimePath string `json:"runtime_path"`
	// RuntimeServices are the services this repository asks Feat to manage.
	RuntimeServices []string `json:"runtime_services"`
	// DefaultAccess is the repository's default participation in a task.
	DefaultAccess string `json:"default_access"`
	// Primary reports whether this is the project's primary repository.
	Primary bool `json:"primary"`
}

// ConfigurationSection is one titled block of resolved configuration.
type ConfigurationSection struct {
	// Title names the block.
	Title string `json:"title"`
	// Fields are the block's values, in the order the table prints them.
	Fields []ConfigurationField `json:"fields"`
}

// ConfigurationField is one resolved value.
type ConfigurationField struct {
	// Name is the field's dotted configuration path.
	Name string `json:"name"`
	// Value is the resolved value as it will be used.
	Value string `json:"value"`
	// Note explains a value that would otherwise need explaining, such as a
	// default Feat filled in or a file it does not read. It is empty when there
	// is nothing to explain.
	Note string `json:"note"`
}
