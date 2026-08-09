package ui

// overviewBody renders the wide task table, for comparing tasks against each
// other.
//
// It is the one view where resource usage or check counts of several tasks can
// be read side by side, which is FR-UI-005's case for a secondary view. ADR-041
// keeps it provisionally: it is the part of the layout with the least evidence
// behind it, and the three-task runs decide whether it stays.
//
// Columns are dropped from the right when they do not fit rather than wrapped.
// A table that wraps is the defect this layout was built to fix, and a column a
// user cannot see is one the task panel still has.
func (m Model) overviewBody(width int) string {
	if len(m.tasks) == 0 {
		return mutedStyle.Render("no tasks yet") + "\n\n" +
			mutedStyle.Render("press n to prepare one, or run `feat implement`")
	}

	columns := fitColumns(taskColumns, width)
	rows := make([][]string, 0, len(m.tasks))
	for i, task := range m.tasks {
		row := m.taskRow(task, m.now())
		marker := "  "
		if i == m.cursor {
			marker = selectedStyle.Render("▸ ")
		}
		row[0] = marker + taskKey(task)
		rows = append(rows, row[:min(len(row), len(columns))])
	}

	table := renderTable(columns, rows)
	if dropped := len(taskColumns) - len(columns); dropped > 0 {
		note := count(dropped, "column does", "columns do") +
			" not fit this terminal; the task panel has all of them"
		table += "\n\n" + mutedStyle.Render(truncate(note, width))
	}
	return table
}

// fitColumns is the widest prefix of columns that fits, with the marker's two
// cells added to the first.
func fitColumns(columns []column, width int) []column {
	fitted := append([]column(nil), columns...)
	fitted[0].width += 2

	total := 0
	for i, candidate := range fitted {
		next := total + candidate.width
		if i > 0 {
			next += 2
		}
		if next > width {
			if i == 0 {
				return fitted[:1]
			}
			return fitted[:i]
		}
		total = next
	}
	return fitted
}
