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

// claimStateDirectory reads the durable daemon record, refuses a state directory
// this build must not write to, and records that a run has begun.
//
// The record has three readers (ADR-027, ADR-037): the state schema decides
// whether this build may write at all, the clean-shutdown flag lets a recovery
// report say a daemon crashed, and the stop time is how long Feat was not
// looking.
//
// Nothing in it is liveness. A process identifier, a socket, and a lock belong to
// the runtime directory, which does not survive a reboot, so a durable copy would
// describe a daemon that is not running (ADR-027 evidence 1).
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
		// An older daemon writing over a newer directory loses whatever the newer
		// schema added, and unlike every other failure here that loss is silent.
		return fmt.Errorf(
			"the state directory %s was written by a newer Feat (state schema %d, this build reads %d). "+
				"Upgrade Feat, or set %s to a different directory, below which this build keeps state of "+
				"its own; this build will not write to this one, because doing so would discard what the "+
				"newer one recorded",
			s.layout.State, previous.StateSchema, domain.StateSchemaVersion, paths.EnvDataHome)
	}

	// The previous run is kept in memory for this run's reconciliation and not
	// carried into the new record. Copying its stop time forward makes a record
	// whose stop precedes its own start, which the domain refuses, so a daemon that
	// shut down cleanly could never start again.
	s.previousRun = previous
	if previous != nil && !previous.EndedCleanly {
		s.logger.WarnContext(ctx, "the previous daemon did not record a clean shutdown",
			slog.String("state", s.layout.State),
			slog.String("previous_version", previous.Version))
	}

	// The claim is written with no stop, so the absence of the shutdown write is
	// the evidence of a crash. Recording a clean end at startup would make every
	// crash look like an orderly stop.
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

// releaseStateDirectory records that this run ended cleanly. It runs on the way
// out of Serve, after everything that writes has stopped, and a daemon that is
// killed never reaches it, which is what makes the flag meaningful.
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
