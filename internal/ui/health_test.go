package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func testHealth() Health {
	return Health{
		Version:   "v0.0.1",
		Commit:    "abc1234",
		GoVersion: "go1.26.0",
		Platform:  "darwin/arm64",
		Daemon:    "ok (pid 4242, up 3m0s)",
		Socket:    "/run/feat/feat.sock",
	}
}

func TestRunHealthWritesPlainTextWhenNotInteractive(t *testing.T) {
	var out bytes.Buffer

	if err := RunHealth(context.Background(), testHealth(), &out, false); err != nil {
		t.Fatalf("RunHealth: %v", err)
	}

	got := out.String()
	for _, want := range []string{"v0.0.1", "abc1234", "go1.26.0", "darwin/arm64", "ok (pid 4242, up 3m0s)", "/run/feat/feat.sock"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("plain output contains escape sequences:\n%q", got)
	}
}

func TestHealthModelQuitKeys(t *testing.T) {
	quitting := []struct {
		name string
		msg  tea.KeyMsg
	}{
		{"q", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}},
		{"ctrl+c", tea.KeyMsg{Type: tea.KeyCtrlC}},
	}

	for _, test := range quitting {
		t.Run(test.name, func(t *testing.T) {
			_, cmd := healthModel{health: testHealth()}.Update(test.msg)
			if cmd == nil {
				t.Fatalf("%q returned no command; the screen cannot be closed", test.name)
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Errorf("%q did not quit", test.name)
			}
		})
	}

	_, cmd := healthModel{health: testHealth()}.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if cmd != nil {
		t.Error("an unrelated key produced a command")
	}
}

func TestRunHealthDoesNotStartTheProgramWhenNotInteractive(t *testing.T) {
	// A cancelled context must not matter when there is no terminal to drive:
	// the plain rendering is a pure write.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out bytes.Buffer
	if err := RunHealth(ctx, testHealth(), &out, false); err != nil {
		t.Fatalf("RunHealth: %v", err)
	}
	if out.Len() == 0 {
		t.Error("nothing was written")
	}
}
