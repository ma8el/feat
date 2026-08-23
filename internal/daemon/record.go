package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/store"
)

// claimStateDirectory reads the durable daemon record, refuses a state
// directory this build must not write to, and records that a run has begun.
//
// ADR-027 deferred this record to the first code that reads one. It is written
// here because it has three readers rather than because it was scheduled: the
// state schema decides whether this build may write at all,
// the clean-shutdown flag is what lets a recovery report say a daemon crashed
// rather than leaving a user to infer it, and the stop time is how long Feat was
// not looking (ADR-037).
//
// Nothing in it is liveness. A process identifier, a socket, and a lock belong
// to the runtime directory, which does not survive a reboot; a durable copy of
// one would describe a daemon that is not running, which is the bug ADR-027
// evidence 1 exists to prevent.
func (s *service) claimStateDirectory(ctx context.Context) error {
	previous, err := s.store.Daemons().Load(ctx)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		previous = nil
	default:
		return fmt.Errorf("reading the daemon record in %s: %w", s.layout.State, err)
	}

	if previous != nil && previous.Newer() {
		// Refusing is the conservative direction and it is the only one that is
		// safe: an older daemon writing over a newer directory loses whatever
		// the newer schema added, and unlike every other failure here that loss
		// is silent.
		return fmt.Errorf(
			"the state directory %s was written by a newer Feat (state schema %d, this build reads %d). "+
				"Upgrade Feat, or set %s to a different directory, below which this build keeps state of "+
				"its own; this build will not write to this one, because doing so would discard what the "+
				"newer one recorded",
			s.layout.State, previous.StateSchema, domain.StateSchemaVersion, paths.EnvDataHome)
	}

	// What the previous run left is kept in memory for this run's reconciliation
	// and is deliberately not carried into the new record. A record describes one
	// run: its start, and its stop once it has one. Copying the previous run's
	// stop time forward makes a record whose stop precedes its own start, which
	// is a state the domain refuses — so a daemon that shut down cleanly could
	// never start again. Found by stopping and starting the real binary.
	s.previousRun = previous
	if previous != nil && !previous.EndedCleanly {
		s.logger.WarnContext(ctx, "the previous daemon did not record a clean shutdown",
			slog.String("state", s.layout.State),
			slog.String("previous_version", previous.Version))
	}

	// The claim is written with no stop at all, so a crash needs nothing to be
	// written in order to be visible: the absence of the shutdown write is the
	// evidence. Recording a clean end at startup would make every crash look
	// like an orderly stop.
	record := &domain.DaemonRecord{
		StateSchema: domain.StateSchemaVersion,
		StartedAt:   s.now(),
		Version:     s.build.Version,
	}
	if err := s.store.Daemons().Save(ctx, record); err != nil {
		return fmt.Errorf("recording that a daemon has claimed %s: %w", s.layout.State, err)
	}
	s.startedRecord = record
	return nil
}

// releaseStateDirectory records that this run ended cleanly.
//
// It runs on the way out of Serve, after everything that writes has stopped. A
// daemon that is killed never reaches it, which is exactly what makes the flag
// meaningful.
func (s *service) releaseStateDirectory(ctx context.Context) {
	if s.startedRecord == nil {
		return
	}
	record := &domain.DaemonRecord{
		StateSchema:  domain.StateSchemaVersion,
		StartedAt:    s.startedRecord.StartedAt,
		StoppedAt:    s.now(),
		EndedCleanly: true,
		Version:      s.build.Version,
	}
	if err := s.store.Daemons().Save(ctx, record); err != nil {
		s.logger.ErrorContext(ctx, "recording a clean daemon shutdown", slog.Any("error", err))
	}
}
