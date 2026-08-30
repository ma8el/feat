package ui

import (
	"strings"
	"testing"
)

// TestAScrollingOverlayNamesEveryKeyThatMovesIt is the reported gap.
//
// The publication draft and the wizard's review answer j and k as well as the
// arrows and the page keys, and their hints named only two of the four. A key
// that works and is not offered is as good as one that does not exist — and
// these are the screens where it matters, because a user who has learned the
// dashboard's own j and k has no way to find out that the same keys carry into
// an overlay.
//
// Each case checks the two halves together: the key moves the document, and the
// hints say so. A hint that names a key nothing answers is the same defect from
// the other side, which is why the footer is not asserted on its own.
func TestAScrollingOverlayNamesEveryKeyThatMovesIt(t *testing.T) {
	t.Run("the publication draft", func(t *testing.T) {
		backend := newFakeBackend()
		model := sized(publishable(t, backend), 120, 32)
		model.publication.status.Drafts[0].Body = longDescription()
		if !model.publicationScrollable() {
			t.Fatal("the draft fits its window, so it offers no reading keys at all")
		}

		moved := press(t, model, "j")
		if moved.publication.scroll != 1 {
			t.Errorf("j moved the draft to line %d, want the second", moved.publication.scroll)
		}
		if back := press(t, moved, "k"); back.publication.scroll != 0 {
			t.Errorf("k moved the draft to line %d, want the first", back.publication.scroll)
		}
		requireNamesTheLetters(t, model.publicationHints())
	})

	t.Run("the wizard's review", func(t *testing.T) {
		model := reviewingWizard(t, longConfiguration())

		model.wizard, _ = model.wizard.Update(key("j"))
		if model.wizard.scroll != 1 {
			t.Errorf("j moved the configuration to line %d, want the second", model.wizard.scroll)
		}
		model.wizard, _ = model.wizard.Update(key("k"))
		if model.wizard.scroll != 0 {
			t.Errorf("k moved the configuration to line %d, want the first", model.wizard.scroll)
		}
		requireNamesTheLetters(t, model.wizard.hints())
	})

	// The same wording is on three more screens that answer the same four keys,
	// which is why it has one home rather than five.
	t.Run("the wizard's checks", func(t *testing.T) {
		model := reviewingWizard(t, longConfiguration())
		model.wizard.step = wizardChecking
		requireNamesTheLetters(t, model.wizard.hints())
	})

	t.Run("the diagnosis report", func(t *testing.T) {
		backend := newFakeBackend()
		backend.diagnosis = longDiagnosis()
		model := press(t, sized(dashboard(backend, liveTask()), 120, 32), "D")

		before := model.diagnosis.scroll
		if moved := press(t, model, "j"); moved.diagnosis.scroll != before+1 {
			t.Errorf("j moved the report to line %d, want %d", moved.diagnosis.scroll, before+1)
		}
		requireNamesTheLetters(t, model.diagnosisHints())
	})

	t.Run("a finished publication", func(t *testing.T) {
		backend := newFakeBackend()
		model := sized(publishable(t, backend), 120, 32)
		model.publication.done = true
		requireNamesTheLetters(t, model.publicationHints())
	})
}

// requireNamesTheLetters checks that a hint line offers the letter keys beside
// the arrows and the page keys it already offered.
func requireNamesTheLetters(t *testing.T, hints string) {
	t.Helper()

	flat := flowed(hints)
	if !strings.Contains(flat, "j k") {
		t.Errorf("the hints do not offer j and k:\n%s", flat)
	}
	for _, kept := range []string{"↑↓", "pgup/pgdn"} {
		if !strings.Contains(flat, kept) {
			t.Errorf("the hints lost %q:\n%s", kept, flat)
		}
	}
}
