package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	iofs "io/fs"
	"path/filepath"
	"time"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/store"
)

// eventDocument is the stored form of one recorded event. Each is written as
// one line of JSON, so that appending never rewrites the history before it.
type eventDocument struct {
	SchemaVersion int       `json:"schema_version"`
	Sequence      uint64    `json:"sequence"`
	OccurredAt    time.Time `json:"occurred_at"`
	ProjectID     string    `json:"project_id"`
	TaskID        string    `json:"task_id"`
	RepositoryID  string    `json:"repository_id,omitempty"`
	Type          string    `json:"type"`
	From          string    `json:"from,omitempty"`
	To            string    `json:"to,omitempty"`
	Detail        string    `json:"detail,omitempty"`
}

type eventStore struct{ store *Store }

// Append records one event and returns it with its assigned sequence number.
//
// The log assigns the sequence rather than the caller. Ordering is a property
// of the log, and a caller that supplied its own numbers would have to know
// what every other caller had already written.
func (e eventStore) Append(ctx context.Context, ref store.TaskRef, event domain.Event) (domain.Event, error) {
	if err := ctx.Err(); err != nil {
		return domain.Event{}, err
	}
	if err := ref.Validate(); err != nil {
		return domain.Event{}, err
	}
	if event.ProjectID != ref.Project || event.TaskID != ref.Task {
		return domain.Event{}, fmt.Errorf(
			"event describes task %s/%s and cannot be appended to the log of %s",
			event.ProjectID, event.TaskID, ref)
	}
	if err := event.Validate(); err != nil {
		return domain.Event{}, err
	}
	dir, err := e.store.taskDir(ref)
	if err != nil {
		return domain.Event{}, err
	}
	path := filepath.Join(dir, eventsFile)

	key := "events:" + ref.String()
	defer e.store.lock(key)()

	last, err := e.store.lastSequence(key, ref, path)
	if err != nil {
		return domain.Event{}, err
	}

	event.Sequence = last + 1
	event.OccurredAt = event.OccurredAt.UTC()
	line, err := json.Marshal(encodeEvent(event))
	if err != nil {
		return domain.Event{}, fmt.Errorf("encoding event: %w", err)
	}
	if err := e.store.appendLine(path, append(line, '\n')); err != nil {
		return domain.Event{}, err
	}

	e.store.recordSequence(key, event.Sequence)
	return event, nil
}

// Replay returns the task's recorded history in order.
//
// It takes the same lock an append takes, so a replay that runs while an event
// is being written sees the log either before or after that event, rather than
// mistaking a record still being written for a crash.
func (e eventStore) Replay(ctx context.Context, ref store.TaskRef) (store.EventLog, error) {
	if err := ctx.Err(); err != nil {
		return store.EventLog{}, err
	}
	dir, err := e.store.taskDir(ref)
	if err != nil {
		return store.EventLog{}, err
	}

	defer e.store.lock("events:" + ref.String())()
	return readLog(ref, filepath.Join(dir, eventsFile))
}

// lastSequence returns the highest sequence in a log, reading the file only
// when this process has not appended to it yet.
func (s *Store) lastSequence(key string, ref store.TaskRef, path string) (uint64, error) {
	if sequence, ok := s.cachedSequence(key); ok {
		return sequence, nil
	}
	log, err := readLog(ref, path)
	if err != nil {
		return 0, err
	}
	last, _ := log.Last()
	s.recordSequence(key, last.Sequence)
	return last.Sequence, nil
}

// readLog reads and decodes an event log.
//
// A record is complete when it is terminated by a newline. An unterminated
// final record is what a crash during an append leaves behind, so it is ignored
// and reported. Anything else that fails to decode is corruption: dropping a
// malformed record in the middle of a history would quietly rewrite it.
func readLog(ref store.TaskRef, path string) (store.EventLog, error) {
	raw, err := readFile(path)
	if errors.Is(err, iofs.ErrNotExist) {
		return store.EventLog{}, nil
	}
	if err != nil {
		return store.EventLog{}, fmt.Errorf("reading %s: %w", path, err)
	}

	var log store.EventLog
	var previous uint64
	for number, offset := 1, 0; offset < len(raw); number++ {
		width := bytes.IndexByte(raw[offset:], '\n')
		if width < 0 {
			log.IncompleteFinalRecord = true
			break
		}
		record := raw[offset : offset+width]
		offset += width + 1

		var document eventDocument
		if err := eventCodec.unmarshal(ref.String(), path, record, &document); err != nil {
			return store.EventLog{}, withRecord(err, number)
		}
		event := decodeEvent(document)
		if err := event.Validate(); err != nil {
			return store.EventLog{}, &store.CorruptError{
				Kind: "event", ID: ref.String(), Path: path, Record: number, Err: err,
			}
		}
		if event.Sequence <= previous {
			return store.EventLog{}, &store.CorruptError{
				Kind: "event", ID: ref.String(), Path: path, Record: number,
				Err: fmt.Errorf("sequence %d does not follow %d, so the history is not in order",
					event.Sequence, previous),
			}
		}
		previous = event.Sequence
		log.Events = append(log.Events, event)
	}
	return log, nil
}

// withRecord adds the record number to a decoding failure, so the message names
// the line the user has to look at.
func withRecord(err error, number int) error {
	var corruptErr *store.CorruptError
	if errors.As(err, &corruptErr) {
		corruptErr.Record = number
	}
	return err
}

func encodeEvent(event domain.Event) eventDocument {
	return eventDocument{
		SchemaVersion: eventSchemaVersion,
		Sequence:      event.Sequence,
		OccurredAt:    event.OccurredAt.UTC(),
		ProjectID:     event.ProjectID.String(),
		TaskID:        event.TaskID.String(),
		RepositoryID:  event.RepositoryID.String(),
		Type:          string(event.Type),
		From:          event.From,
		To:            event.To,
		Detail:        event.Detail,
	}
}

func decodeEvent(document eventDocument) domain.Event {
	return domain.Event{
		Sequence:     document.Sequence,
		ProjectID:    domain.ProjectID(document.ProjectID),
		TaskID:       domain.TaskID(document.TaskID),
		RepositoryID: domain.RepositoryID(document.RepositoryID),
		Type:         domain.EventType(document.Type),
		From:         document.From,
		To:           document.To,
		Detail:       document.Detail,
		OccurredAt:   document.OccurredAt.UTC(),
	}
}
