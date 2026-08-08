package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/store/storetest"
)

// terminalPath is the endpoint for one task's rendered pane.
func terminalPath(suffix string) string {
	return "/v1/tasks/" + storetest.TaskID.String() + "/terminal" + suffix
}

// TestATerminalFrameCarriesWhatTmuxDrew checks the endpoint returns the rendered
// pane, colour and all, rather than anything derived from it.
func TestATerminalFrameCarriesWhatTmuxDrew(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	response := requestBody(t, handler, http.MethodPost, terminalPath(""),
		`{"width":100,"height":30}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
	}

	var frame TerminalFrame
	if err := json.Unmarshal(response.Body.Bytes(), &frame); err != nil {
		t.Fatalf("decoding the frame: %v", err)
	}
	if len(frame.Content) == 0 || !strings.Contains(frame.Content[0], "\x1b[32m") {
		t.Errorf("the frame lost the colour tmux rendered: %q", frame.Content)
	}
	if len(service.views) != 1 || service.views[0].Width != 100 || service.views[0].Height != 30 {
		t.Errorf("the daemon was asked for %+v, want one 100x30 view", service.views)
	}
}

// TestATerminalViewMustNameASizeItWillDraw is why the request is validated
// rather than trusted: asking for a frame resizes the agent's own pane.
func TestATerminalViewMustNameASizeItWillDraw(t *testing.T) {
	for name, view := range map[string]TerminalView{
		"no size":       {},
		"negative":      {Width: -1, Height: 10},
		"zero height":   {Width: 80, Height: 0},
		"absurdly wide": {Width: MaxTerminalSize + 1, Height: 24},
	} {
		if err := view.Validate(); err == nil {
			t.Errorf("a %s view was accepted: %+v", name, view)
		}
	}
	if err := (TerminalView{Width: 80, Height: 24}).Validate(); err != nil {
		t.Errorf("an ordinary view was refused: %v", err)
	}
}

// TestTerminalInputIsBoundedAndNamed is the security rule at this surface.
//
// Sending keys is a write to a running agent, so every field is checked rather
// than passed through. The adapter also sends keys after a terminator; this is
// the check that does not depend on that one holding.
func TestTerminalInputIsBoundedAndNamed(t *testing.T) {
	tooManyKeys := make([]string, MaxTerminalKeys+1)
	for i := range tooManyKeys {
		tooManyKeys[i] = "Enter"
	}

	for name, input := range map[string]TerminalInput{
		"empty":            {},
		"a flag as a key":  {Keys: []string{"-X"}},
		"a shell fragment": {Keys: []string{"Enter; rm -rf /"}},
		"a spaced key":     {Keys: []string{"C-c Enter"}},
		"an endless name":  {Keys: []string{strings.Repeat("A", MaxTerminalKeyName+1)}},
		"too many keys":    {Keys: tooManyKeys},
		"too much text":    {Text: strings.Repeat("x", MaxTerminalText+1)},
	} {
		if err := input.Validate(); err == nil {
			t.Errorf("%s was accepted as input", name)
		}
	}

	for name, input := range map[string]TerminalInput{
		"a key":          {Keys: []string{"Enter"}},
		"a modified key": {Keys: []string{"C-c"}},
		"a shifted key":  {Keys: []string{"S-Up"}},
		"a function key": {Keys: []string{"F12"}},
		"typed text":     {Text: "run the tests"},
		"both":           {Text: "run the tests", Keys: []string{"Enter"}},
	} {
		if err := input.Validate(); err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
}

// TestRefusedInputNeverReachesTheDaemon checks the transport rejects rather than
// passing a bad request through for the daemon to interpret.
func TestRefusedInputNeverReachesTheDaemon(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	response := requestBody(t, handler, http.MethodPost, terminalPath("/input"),
		`{"keys":["Enter; rm -rf /"]}`)

	if response.Code != http.StatusBadRequest {
		t.Errorf("a refused key returned %d, want %d", response.Code, http.StatusBadRequest)
	}
	if len(service.inputs) != 0 {
		t.Errorf("a refused request reached the daemon: %+v", service.inputs)
	}
}

// TestAcceptedInputReachesTheDaemonWhole checks nothing is dropped between the
// transport and the service, and that text and keys arrive together: a user who
// typed a line and pressed Enter sends both, and the Enter submits the text.
func TestAcceptedInputReachesTheDaemonWhole(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	response := requestBody(t, handler, http.MethodPost, terminalPath("/input"),
		`{"text":"run the tests","keys":["Enter"]}`)

	if response.Code != http.StatusNoContent {
		t.Fatalf("sending input returned %d, body: %s", response.Code, response.Body.String())
	}
	if len(service.inputs) != 1 {
		t.Fatalf("the daemon received %d inputs, want 1", len(service.inputs))
	}
	if got := service.inputs[0]; got.Text != "run the tests" || len(got.Keys) != 1 {
		t.Errorf("the daemon received %+v", got)
	}
}

// TestATerminalFrameIsAPublishedSurface pins the payload, as every other
// response in this package is pinned (ADR-027).
func TestATerminalFrameIsAPublishedSurface(t *testing.T) {
	handler := NewHandler(Options{Service: newFakeService()})

	response := requestBody(t, handler, http.MethodPost, terminalPath(""),
		`{"width":100,"height":30}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
	}
	compare(t, "terminal-frame.golden", response.Body.String())
}
