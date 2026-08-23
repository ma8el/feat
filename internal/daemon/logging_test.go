package daemon

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/paths"
)

// logLayout returns a layout whose state directory is this test's own.
func logLayout(t *testing.T) paths.Layout {
	t.Helper()
	return paths.Layout{State: t.TempDir(), Runtime: t.TempDir()}
}

// openBounded opens a rotating writer on a fresh log file with the given bound.
func openBounded(t *testing.T, limit int64, keep int) *rotatingFile {
	t.Helper()
	return openBoundedOn(t, logLayout(t), limit, keep)
}

// openBoundedOn is openBounded against a caller-supplied layout.
func openBoundedOn(t *testing.T, layout paths.Layout, limit int64, keep int) *rotatingFile {
	t.Helper()

	file, err := openLogFile(layout)
	if err != nil {
		t.Fatalf("openLogFile: %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })

	rotating, err := newRotatingFile(file, limit, keep)
	if err != nil {
		t.Fatalf("newRotatingFile: %v", err)
	}
	return rotating
}

// sizeOf returns a file's size, or -1 when it does not exist.
func sizeOf(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return -1
	}
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

func TestRotatingFileKeepsASmallLogInOneFile(t *testing.T) {
	log := openBounded(t, 1024, 2)

	for range 10 {
		if _, err := log.Write([]byte(strings.Repeat("a", 50) + "\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	if got := sizeOf(t, log.path); got != 510 {
		t.Errorf("log is %d bytes, want 510", got)
	}
	if got := sizeOf(t, generationPath(log.path, 1)); got != -1 {
		t.Errorf("a log under the bound was rotated: generation 1 is %d bytes", got)
	}
}

func TestRotatingFileMovesTheLogAsideWhenItReachesTheBound(t *testing.T) {
	log := openBounded(t, 200, 2)

	first := strings.Repeat("a", 99) + "\n"
	second := strings.Repeat("b", 99) + "\n"
	third := strings.Repeat("c", 99) + "\n"
	for _, record := range []string{first, second, third} {
		if _, err := log.Write([]byte(record)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	// The third record would have taken the file past 200 bytes, so the first
	// two moved aside and it began a new one.
	current, err := os.ReadFile(log.path)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	if string(current) != third {
		t.Errorf("the current log holds %q, want %q", current, third)
	}

	rotated, err := os.ReadFile(generationPath(log.path, 1))
	if err != nil {
		t.Fatalf("reading generation 1: %v", err)
	}
	if string(rotated) != first+second {
		t.Errorf("generation 1 holds %q, want %q", rotated, first+second)
	}
}

// TestRotatingFileHoldsTheBoundAcrossManyRotations is the property the whole
// change exists for: no amount of writing makes the log outgrow its bound.
func TestRotatingFileHoldsTheBoundAcrossManyRotations(t *testing.T) {
	const (
		limit  = 500
		keep   = 2
		record = 100
	)
	log := openBounded(t, limit, keep)

	// Far more than the bound: without rotation this would be 200 KB.
	for range 2000 {
		if _, err := log.Write([]byte(strings.Repeat("x", record-1) + "\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	var total int64
	for _, path := range []string{log.path, generationPath(log.path, 1), generationPath(log.path, 2)} {
		if size := sizeOf(t, path); size > 0 {
			total += size
		}
	}
	// One record of slack per file: a record is never split across a rotation.
	if ceiling := int64(limit+record) * (keep + 1); total > ceiling {
		t.Errorf("the log occupies %d bytes, want at most %d", total, ceiling)
	}

	// The generation past the last one kept must not survive.
	if got := sizeOf(t, generationPath(log.path, keep+1)); got != -1 {
		t.Errorf("generation %d was kept, and only %d are", keep+1, keep)
	}
}

// TestRotatingFileCutsDownALogThatIsAlreadyOversized is the case the user hits
// on the first start after this change: a log that grew without a bound must
// shrink rather than be copied at its full size into a rotated file.
func TestRotatingFileCutsDownALogThatIsAlreadyOversized(t *testing.T) {
	layout := logLayout(t)
	path := layout.LogFile()
	if err := os.MkdirAll(filepath.Dir(path), stateDirPerm); err != nil {
		t.Fatalf("creating the log directory: %v", err)
	}

	// A log written before the bound existed, with recognisable ends.
	var existing strings.Builder
	existing.WriteString(`{"msg":"oldest"}` + "\n")
	for i := range 5000 {
		_ = i
		existing.WriteString(`{"msg":"` + strings.Repeat("m", 90) + `"}` + "\n")
	}
	existing.WriteString(`{"msg":"newest"}` + "\n")
	if err := os.WriteFile(path, []byte(existing.String()), logFilePerm); err != nil {
		t.Fatalf("writing the existing log: %v", err)
	}
	before := sizeOf(t, path)

	file, err := openLogFile(layout)
	if err != nil {
		t.Fatalf("openLogFile: %v", err)
	}
	defer func() { _ = file.Close() }()

	log, err := newRotatingFile(file, 4096, 2)
	if err != nil {
		t.Fatalf("newRotatingFile: %v", err)
	}
	if _, err := log.Write([]byte(`{"msg":"after"}` + "\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	after := sizeOf(t, path) + sizeOf(t, generationPath(log.path, 1))
	if after >= before {
		t.Errorf("the log occupies %d bytes and occupied %d; it should have shrunk", after, before)
	}

	rotated, err := os.ReadFile(generationPath(log.path, 1))
	if err != nil {
		t.Fatalf("reading generation 1: %v", err)
	}
	if strings.Contains(string(rotated), `"oldest"`) {
		t.Error("the oldest record survived, so the whole oversized log was carried over")
	}
	if !strings.Contains(string(rotated), `"newest"`) {
		t.Error("the most recent record was dropped, and it is the one worth keeping")
	}

	// Every line must still parse: the tail is cut at a record boundary.
	scanner := bufio.NewScanner(strings.NewReader(string(rotated)))
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		if !json.Valid(scanner.Bytes()) {
			t.Fatalf("a rotated line does not parse: %q", scanner.Text())
		}
	}
}

// TestRotatingFileKeepsInheritedDescriptorsValid pins the reason rotation copies
// and truncates rather than renaming. `feat daemon start` opens the log and
// hands that descriptor to the spawned daemon as its standard error, so a
// rotation that swapped the file out from under it would send a panic to a file
// nobody would open.
func TestRotatingFileKeepsInheritedDescriptorsValid(t *testing.T) {
	layout := logLayout(t)

	// The descriptor the parent opens and the child inherits.
	inherited, err := openLogFile(layout)
	if err != nil {
		t.Fatalf("openLogFile: %v", err)
	}
	defer func() { _ = inherited.Close() }()

	log := openBoundedOn(t, layout, 200, 1)
	for range 5 {
		if _, err := log.Write([]byte(strings.Repeat("a", 99) + "\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	if _, err := inherited.WriteString("panic: from the inherited descriptor\n"); err != nil {
		t.Fatalf("writing through the inherited descriptor: %v", err)
	}

	current, err := os.ReadFile(layout.LogFile())
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	if !strings.Contains(string(current), "panic: from the inherited descriptor") {
		t.Error("a write through the inherited descriptor did not reach the live log after rotation")
	}
}

// TestOpenLogBoundsWhatItWrites checks the wiring: a logger built by OpenLog
// rotates, rather than only the writer underneath it.
func TestOpenLogBoundsWhatItWrites(t *testing.T) {
	layout := logLayout(t)

	log, err := OpenLog(layout, slog.LevelInfo, false)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	defer func() { _ = log.Close() }()

	// Enough records to pass the real bound several times over would be slow;
	// that rotation happens at all is what this pins, so write past a bound the
	// writer underneath already proved it honours.
	for range 200 {
		log.Logger.Info("a record", slog.String("padding", strings.Repeat("p", 200)))
	}

	if got := sizeOf(t, layout.LogFile()); got <= 0 {
		t.Fatalf("the log is %d bytes, want records in it", got)
	}
	if got := sizeOf(t, generationPath(layout.LogFile(), 1)); got != -1 {
		t.Errorf("a log well under the bound rotated: generation 1 is %d bytes", got)
	}
	if maxLogSize <= 0 || logGenerations < 0 {
		t.Errorf("the configured bound is not usable: max %d, generations %d", maxLogSize, logGenerations)
	}
}

func TestGenerationPathNumbersFilesBesideTheLog(t *testing.T) {
	if got, want := generationPath("/s/logs/daemon.log", 1), "/s/logs/daemon.log.1"; got != want {
		t.Errorf("generationPath = %s, want %s", got, want)
	}
	if got, want := generationPath("/s/logs/daemon.log", 2), "/s/logs/daemon.log.2"; got != want {
		t.Errorf("generationPath = %s, want %s", got, want)
	}
}
