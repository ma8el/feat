package cli

import (
	"io"
	"strings"

	"github.com/ma8el/feat/internal/config"
)

// table renders aligned columns.
//
// Widths are computed from the content rather than guessed, because the widest
// cell in these tables is a filesystem path and no guess survives a real one: a
// fixed width either wastes half the terminal or lets one long path push every
// following column out of line.
type table struct {
	header []string
	rows   [][]string
}

func (t *table) add(cells ...string) { t.rows = append(t.rows, cells) }

func (t *table) empty() bool { return len(t.rows) == 0 }

// render writes the table, indenting every line by prefix. The last column is
// not padded, so no line carries trailing spaces.
func (t *table) render(out io.Writer, prefix string) {
	if t.empty() {
		return
	}

	widths := t.widths()
	if len(t.header) > 0 {
		printf(out, "%s\n", renderRow(prefix, t.header, widths))
	}
	for _, row := range t.rows {
		printf(out, "%s\n", renderRow(prefix, row, widths))
	}
}

// widths returns the width of each column.
func (t *table) widths() []int {
	columns := len(t.header)
	for _, row := range t.rows {
		if len(row) > columns {
			columns = len(row)
		}
	}

	widths := make([]int, columns)
	for _, row := range append([][]string{t.header}, t.rows...) {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	return widths
}

// renderRow pads every cell but the last.
func renderRow(prefix string, row []string, widths []int) string {
	var line strings.Builder
	line.WriteString(prefix)
	for i, cell := range row {
		if i == len(row)-1 {
			line.WriteString(cell)
			break
		}
		line.WriteString(cell)
		line.WriteString(strings.Repeat(" ", widths[i]-len(cell)+2))
	}
	return strings.TrimRight(line.String(), " ")
}

// mountTable builds the repository-to-container path mapping.
//
// It is a table of its own because it is the mapping a devcontainer task
// depends on and the one most worth checking by eye: a repository mounted at
// the wrong path is a task that compiles nothing.
func mountTable(cfg *config.Config) *table {
	mounts := cfg.Mounts()
	if len(mounts) == 0 {
		return &table{}
	}

	container := cfg.Agent.Execution.Devcontainer()
	built := &table{header: []string{"REPOSITORY", "HOST PATH", "DEFAULT ACCESS"}}
	if container {
		built.header = []string{"REPOSITORY", "HOST PATH", "CONTAINER PATH", "DEFAULT ACCESS"}
	}

	for _, mount := range mounts {
		name := mount.RepositoryID
		if mount.Primary {
			name += " *"
		}
		if container {
			built.add(name, mount.HostPath, orNone(mount.ContainerPath), mount.DefaultAccess)
			continue
		}
		built.add(name, mount.HostPath, mount.DefaultAccess)
	}
	return built
}
