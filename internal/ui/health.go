package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Health is the static information rendered by the health screen.
//
// It is a plain struct so that the UI does not depend on the version, config,
// or daemon packages.
type Health struct {
	Version   string
	Commit    string
	GoVersion string
	Platform  string
	Daemon    string
}

// RunHealth renders the health screen.
//
// When interactive is false the summary is written to out as plain text, so
// that `feat` remains usable in a pipe or in CI where no terminal is attached.
func RunHealth(ctx context.Context, h Health, out io.Writer, interactive bool) error {
	if !interactive {
		_, err := fmt.Fprintln(out, renderHealth(h, false))
		return err
	}

	program := tea.NewProgram(
		healthModel{health: h},
		tea.WithContext(ctx),
		tea.WithAltScreen(),
	)

	if _, err := program.Run(); err != nil {
		// A cancelled context is an ordinary shutdown, not a failure.
		if ctx.Err() != nil || errors.Is(err, tea.ErrProgramKilled) {
			return nil
		}
		return fmt.Errorf("health screen: %w", err)
	}
	return nil
}

type healthModel struct {
	health Health
}

func (m healthModel) Init() tea.Cmd { return nil }

func (m healthModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m healthModel) View() string { return renderHealth(m.health, true) }

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#fafafa"})

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#6c6c6c", Dark: "#8a8a8a"})

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#6c6c6c", Dark: "#8a8a8a"}).
			Width(10)

	valueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#fafafa"})

	frameStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.AdaptiveColor{Light: "#c4c4c4", Dark: "#4a4a4a"}).
			Padding(1, 3)
)

// renderHealth renders the health summary. When styled is false the output is
// plain text with no escape sequences, which keeps piped output and golden
// test files readable.
func renderHealth(h Health, styled bool) string {
	fields := [][2]string{
		{"version", h.Version},
		{"commit", h.Commit},
		{"go", h.GoVersion},
		{"platform", h.Platform},
		{"daemon", h.Daemon},
	}

	if !styled {
		var b strings.Builder
		b.WriteString("feat — pre-alpha skeleton\n")
		for _, field := range fields {
			fmt.Fprintf(&b, "%-10s %s\n", field[0], field[1])
		}
		return strings.TrimRight(b.String(), "\n")
	}

	lines := []string{
		titleStyle.Render("feat"),
		subtitleStyle.Render("pre-alpha skeleton"),
		"",
	}
	for _, field := range fields {
		lines = append(lines, labelStyle.Render(field[0])+valueStyle.Render(field[1]))
	}
	lines = append(lines, "", subtitleStyle.Render("q  quit"))

	return frameStyle.Render(strings.Join(lines, "\n"))
}
