package control

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Rejection is one outbox entry the protocol refused.
//
// It carries the entry rather than only a reason because a refusal has to be
// settled, and settling it needs to know which file it was about. An entry
// refused and left as it was would be read, refused, and reported again on
// every poll for the life of the task, which is the opposite of what this
// package promises: an agent that wrote a bad document is told once.
type Rejection struct {
	// File is the outbox entry that was refused.
	File string
	// ID is the message identifier, when the document parsed far enough to
	// carry a valid one and empty when it did not.
	ID string
	// Err says what was wrong, phrased to be read by a person.
	Err error
	// Final reports that the refusal is about the document rather than about
	// reading it. A document Feat understood and refused is settled for good; a
	// read that failed for a reason of the host's — an I/O error, an entry
	// removed between the listing and the read — is left for the next poll.
	Final bool
}

// Error lets a rejection stand wherever the failure it describes would.
func (r Rejection) Error() string {
	if r.Err == nil {
		return "the control message in " + r.File + " was refused"
	}
	return r.Err.Error()
}

// Unwrap exposes the reason, so a caller can still ask what kind it was.
func (r Rejection) Unwrap() error { return r.Err }

// refuse records why one entry was not applied.
//
// A rejection is final when the document itself was the problem, which is
// exactly when the reason is a *RejectionError: everything else is a failure of
// Feat's own to read what the agent wrote.
func refuse(file, id string, err error) Rejection {
	var rejection *RejectionError
	final := errors.As(err, &rejection)
	if final && rejection.File == "" {
		rejection.File = file
	}
	return Rejection{File: file, ID: id, Err: err, Final: final}
}

// Pending returns the outbox messages that have not been applied yet, oldest
// first, together with the entries that were refused.
//
// Refused entries are returned rather than raised, because a message the agent
// wrote badly is an ordinary event: the daemon records it, the user can see it,
// and the task carries on. Only a failure to read the directory itself is an
// error.
//
// An entry that has already been settled — applied or refused — is skipped
// before it is opened. Messages stay in the outbox until cleanup as the account
// of what the agent sent, so a poll that re-read them would grow without bound
// in the number of messages a task has ever written.
//
// Ordering is by modification time with the file name as a tiebreak. The
// envelope's own timestamp is not used for it: a hook writes that timestamp with
// whatever resolution its shell has, and two events in one second must still be
// applied in the order they happened.
func (w *Workspace) Pending() ([]Message, []Rejection, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.loadProcessed(); err != nil {
		return nil, nil, err
	}
	if err := w.checkDirectory(w.OutboxDir()); err != nil {
		return nil, nil, err
	}

	entries, err := os.ReadDir(w.OutboxDir())
	if errors.Is(err, os.ErrNotExist) {
		// A workspace that has not been created has no messages, which is not a
		// failure to read one.
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("reading the control outbox %s: %w", w.OutboxDir(), err)
	}

	type candidate struct {
		message  Message
		modified time.Time
		name     string
	}
	var (
		found     []candidate
		rejected  []Rejection
		stillHere = make(map[string]bool, len(entries))
	)

	for _, entry := range entries {
		name := entry.Name()
		skip, err := checkEntry(entry)
		if err != nil {
			rejected = append(rejected, refuse(name, "", err))
			continue
		}
		if skip {
			continue
		}
		if w.settled[name] {
			// Dealt with, once. The entry stays where it is as the account of
			// what the agent sent — removing it belongs to cleanup — and nothing
			// opens it again.
			continue
		}
		stillHere[name] = true

		data, err := w.readMessage(name)
		if err != nil {
			rejected = append(rejected, refuse(name, "", err))
			continue
		}

		var message Message
		if err := json.Unmarshal(data, &message); err != nil {
			// A document that does not parse may still be arriving. It is given
			// a grace period before it is called malformed, because a writer
			// that did not stage its file is the ordinary cause and blaming it
			// immediately would report a defect that fixes itself.
			if reason := w.tooYoungToJudge(name); reason {
				continue
			}
			rejected = append(rejected, refuse(name, "", &RejectionError{
				Reason: "is not a valid JSON document: " + err.Error(),
			}))
			continue
		}
		delete(w.firstSeen, name)

		message.file = name
		if err := message.Validate(w.task); err != nil {
			// Without an identifier, or with one this build refused: the file is
			// what settles it.
			rejected = append(rejected, refuse(name, "", err))
			continue
		}
		if w.processed[message.ID] {
			// Already applied, under this name or another. A replayed identifier
			// is the case the identifier exists for, so it is skipped in silence
			// rather than reported — and the entry carrying it is remembered for
			// as long as this process runs, so that a second copy is not opened
			// on every poll from now on. It is not recorded on disk: nothing was
			// applied, and a read is not a reason to write.
			w.settled[name] = true
			continue
		}
		if capability := message.Type.Requires(); capability != CapabilityNone {
			rejected = append(rejected, refuse(name, message.ID, &RejectionError{
				Reason: "requires the " + string(capability) + " capability, which Feat grants to no agent: " +
					"a runtime request is inert until the host validates it and you approve it",
			}))
			continue
		}

		info, err := entry.Info()
		if err != nil {
			rejected = append(rejected, refuse(name, message.ID,
				fmt.Errorf("reading the control message %s: %w", name, err)))
			continue
		}
		found = append(found, candidate{message: message, modified: info.ModTime(), name: name})
	}

	// Forget the files that are gone, so a workspace that runs for weeks does
	// not accumulate a note about every message it ever saw.
	for name := range w.firstSeen {
		if !stillHere[name] {
			delete(w.firstSeen, name)
		}
	}

	sort.SliceStable(found, func(i, j int) bool {
		if !found[i].modified.Equal(found[j].modified) {
			return found[i].modified.Before(found[j].modified)
		}
		return found[i].name < found[j].name
	})

	messages := make([]Message, 0, len(found))
	for _, entry := range found {
		messages = append(messages, entry.message)
	}
	return messages, rejected, nil
}

// readMessage reads one outbox entry, refusing at the moment of use everything
// checkEntry could only establish about a listing.
//
// checkEntry works from the snapshot os.ReadDir returned, and the agent owns
// this directory: between that listing and this read an entry can become a
// symbolic link to a file elsewhere on the machine, a named pipe with no
// writer, or a document that has grown past the limit. Opening once, asking the
// descriptor what it is, and reading through a bound is what makes the file
// that was checked the file that is read.
func (w *Workspace) readMessage(name string) ([]byte, error) {
	path := filepath.Join(w.OutboxDir(), name)

	file, err := openLeaf(path, os.O_RDONLY, 0)
	if errors.Is(err, errNotRegular) {
		return nil, &RejectionError{
			File:   name,
			Reason: "is not a regular file, and only regular files are read as messages",
		}
	}
	if err != nil {
		return nil, fmt.Errorf("reading the control message %s: %w", name, err)
	}
	defer func() { _ = file.Close() }()

	// One byte past the limit, so that a document exactly on it is still read
	// and one over it is refused rather than quietly truncated into something
	// that might still parse.
	data, err := io.ReadAll(io.LimitReader(file, MaxMessageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading the control message %s: %w", name, err)
	}
	if len(data) > MaxMessageBytes {
		return nil, &RejectionError{
			File:   name,
			Reason: fmt.Sprintf("is larger than the limit of %d bytes", MaxMessageBytes),
		}
	}
	return data, nil
}

// tooYoungToJudge reports whether an unparseable entry is still within the
// grace period that treats it as a write in progress.
func (w *Workspace) tooYoungToJudge(name string) bool {
	now := w.now()
	first, seen := w.firstSeen[name]
	if !seen {
		w.firstSeen[name] = now
		return true
	}
	return now.Sub(first) < w.parseGrace
}

// processedRecord is one line of the host-only record of applied messages.
type processedRecord struct {
	ID          string    `json:"id"`
	Type        string    `json:"type,omitempty"`
	File        string    `json:"file,omitempty"`
	ProcessedAt time.Time `json:"processed_at"`
	// Outcome says whether the message changed anything, so that a rejected
	// message is recorded as having been seen without being recorded as applied.
	Outcome string `json:"outcome"`
	// Reason explains a rejection.
	Reason string `json:"reason,omitempty"`
}

// Outcomes recorded for a control message.
const (
	// OutcomeApplied means the message changed task state.
	OutcomeApplied = "applied"
	// OutcomeRejected means the message was refused and never applied.
	OutcomeRejected = "rejected"
	// OutcomeInert means the message was understood and deliberately did
	// nothing, which is what a runtime request does in v0.
	OutcomeInert = "inert"
)

// MarkProcessed records that a message has been dealt with.
//
// The record is host-only, so marking a message never requires writing into the
// directory the agent owns. The message itself is left in the outbox as the
// account of what the agent sent; removing it belongs to cleanup.
func (w *Workspace) MarkProcessed(message Message, outcome, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.loadProcessed(); err != nil {
		return err
	}
	if w.processed[message.ID] {
		return nil
	}
	return w.settle(processedRecord{
		ID:      message.ID,
		Type:    string(message.Type),
		File:    message.file,
		Outcome: outcome,
		Reason:  reason,
	})
}

// MarkRefused records that an entry was refused, so that it is never read,
// judged, or reported again.
//
// It is separate from MarkProcessed because a refused document may have no
// identifier at all — an unparseable one has nothing but its name — and because
// only a refusal Feat reached a conclusion about may be settled. A read that
// failed for a reason of the host's is left for the next poll instead, which is
// what Final distinguishes.
func (w *Workspace) MarkRefused(rejection Rejection) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if rejection.File == "" {
		return fmt.Errorf("refusing to settle a control message of task %s that names no file: "+
			"a refusal is settled by the entry it was about", w.task)
	}
	if !rejection.Final {
		return fmt.Errorf("refusing to settle %s of task %s: reading it failed for a reason of Feat's own, "+
			"so the next poll tries again rather than recording a judgement nobody made",
			rejection.File, w.task)
	}

	if err := w.loadProcessed(); err != nil {
		return err
	}
	if w.settled[rejection.File] {
		return nil
	}
	return w.settle(processedRecord{
		ID:      rejection.ID,
		File:    rejection.File,
		Outcome: OutcomeRejected,
		Reason:  rejection.Error(),
	})
}

// settle appends one record and remembers it.
//
// The caller holds the mutex and has already loaded the record.
func (w *Workspace) settle(record processedRecord) error {
	record.ProcessedAt = w.now().UTC()

	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("recording the control message %s of task %s: %w", record.File, w.task, err)
	}
	if err := w.appendProcessed(append(line, '\n')); err != nil {
		return err
	}
	if record.ID != "" {
		w.processed[record.ID] = true
	}
	if record.File != "" {
		w.settled[record.File] = true
	}
	return nil
}

// Processed reports whether a message identifier has already been applied.
func (w *Workspace) Processed(id string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.loadProcessed(); err != nil {
		return false, err
	}
	return w.processed[id], nil
}

// loadProcessed reads the record of settled messages once.
//
// It builds two indexes of the same record: the identifiers that have been
// applied, which is what recognises a message delivered twice under two names,
// and the entries that have been settled, which is what stops a poll from
// opening the same file for the rest of the task's life.
//
// An incomplete final line is ignored, for the reason the event log ignores one:
// a crash during an append costs the record it was writing and nothing else. A
// forgotten identifier can only cause a message to be applied twice, which is
// what the type-level idempotency in the daemon is there for.
//
// The caller holds the mutex.
func (w *Workspace) loadProcessed() error {
	if w.loaded {
		return nil
	}

	if err := w.checkDirectory(w.AgentDir()); err != nil {
		return err
	}
	file, err := openLeaf(w.processedPath(), os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		w.loaded = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the applied control messages of task %s: %w", w.task, err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), MaxMessageBytes)
	for scanner.Scan() {
		var record processedRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			// A damaged line is not a reason to refuse to run. The consequence
			// of skipping it is a message applied twice at worst, and the
			// consequence of failing here is a task that cannot make progress.
			continue
		}
		if record.ID != "" {
			w.processed[record.ID] = true
		}
		if record.File != "" {
			w.settled[record.File] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading %s: %w", w.processedPath(), err)
	}
	w.loaded = true
	return nil
}

// appendProcessed adds one record, repairing an interrupted previous append
// first so that two records can never be joined into one unreadable line.
//
// This is the one write in the package that truncates, so it is the one that
// most needs the file it opens to be the file it named: the record lives in the
// host-only area, and a symbolic link left where it belongs would make the
// repair below truncate whatever the daemon's own user can write. openLeaf and
// the directory check above it are what refuse that.
//
// The caller holds the mutex.
func (w *Workspace) appendProcessed(line []byte) error {
	if err := w.prepareDirectory(w.AgentDir()); err != nil {
		return err
	}

	path := w.processedPath()
	file, err := openLeaf(path, os.O_CREATE|os.O_RDWR, filePerm)
	if err != nil {
		return fmt.Errorf("recording the applied control messages of task %s: %w", w.task, err)
	}
	defer func() { _ = file.Close() }()

	end, err := completeEnd(file)
	if err != nil {
		return fmt.Errorf("repairing %s: %w", path, err)
	}
	if _, err := file.WriteAt(line, end); err != nil {
		return fmt.Errorf("appending to %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("flushing %s: %w", path, err)
	}
	return nil
}

// completeEnd truncates a trailing record with no newline and returns the
// offset the next record starts at.
func completeEnd(file *os.File) (int64, error) {
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	if size == 0 {
		return 0, nil
	}

	last := make([]byte, 1)
	if _, err := file.ReadAt(last, size-1); err != nil {
		return 0, err
	}
	if last[0] == '\n' {
		return size, nil
	}

	data := make([]byte, size)
	if _, err := file.ReadAt(data, 0); err != nil {
		return 0, err
	}
	for i := len(data) - 1; i >= 0; i-- {
		if data[i] == '\n' {
			end := int64(i) + 1
			return end, file.Truncate(end)
		}
	}
	return 0, file.Truncate(0)
}
