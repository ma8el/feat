package api

// The documents `--json` prints.
//
// Three of the four reading commands print what the daemon already answered them
// with — a task list, a review, a runtime status — so their shapes are the wire
// types above and there is one model rather than two to keep in step. The two
// shapes here had none: the envelope a list needs to be an object, and the
// resolved project configuration, which `feat project show` reads from the
// configuration directory rather than over the socket.
//
// The CLI maps a loaded configuration onto ProjectConfiguration, because the CLI
// is what loads one. This package describes the shape and takes no dependency on
// internal/config to fill it in.

// TaskList is the document `feat task list --json` prints.
//
// It is an envelope around the array rather than the array itself, so every
// document this CLI prints is an object and a field can be added without changing
// what a parser looks at.
type TaskList struct {
	// Tasks are the tasks the list shows, newest first.
	Tasks []Task `json:"tasks"`
	// Archived is how many archived tasks there are, whether or not this list
	// shows them.
	//
	// The archive is terminal and nothing prunes it, so the list leaves archived
	// tasks out unless --all was given, and this count is how a caller tells a
	// partial list from a whole one. It counts the same tasks either way, because
	// it is a fact about the tasks rather than about the list.
	Archived int `json:"archived"`
}

// ProjectConfiguration is the document `feat project show --json` prints: the
// configuration Feat will act on, after "~" expansion and after defaults are
// filled.
//
// It is a resolved view rather than the configuration file's own shape, which
// `schema/feat-project.schema.json` already describes. Files that may hold
// secrets appear as paths and never as contents, because nothing ever read
// them.
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
	// They are names and values rather than a typed object, because a caller who
	// wants the configuration's own shape has the file.
	Sections []ConfigurationSection `json:"sections"`
}

// ConfiguredRepository is one repository's resolved place.
//
// Every field is present whether or not it has a value, so a caller can read one
// without asking whether the key is there. An empty agent path is a host-native
// project; an empty runtime path is a repository whose code no service runs.
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
	// Note explains a value that needs it, such as a default Feat filled in or a
	// file it does not read. It is empty otherwise.
	Note string `json:"note"`
}
