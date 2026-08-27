package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/tracker"
)

// Tickets runs a project's configured tracker command and returns what it
// printed.
//
// The command runs on the trusted host, using the authentication the user
// already has there: the agent environment receives no provider token and no
// tracker access, and a ticket the agent fetched would never pass the
// confirmation step that makes a brief something the user read (ADR-070).
//
// It runs the command every time it is asked, because that is the only way to
// learn what the user's tickets are now. Feat passes no filter, so there is
// nothing for a cached list to be re-filtered against, and a ticket that changed
// is found by running the command again and comparing (ADR-071).
func (s *service) Tickets(ctx context.Context, id domain.ProjectID) (api.TicketList, error) {
	if err := id.Validate(); err != nil {
		return api.TicketList{}, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}
	if _, err := s.store.Projects().Load(ctx, id); err != nil {
		return api.TicketList{}, translate(err, "no project "+id.String()+" is registered")
	}

	cfg, err := config.Load(s.layout.ProjectConfigDir(), id.String(), s.configOptions())
	if err != nil {
		return api.TicketList{}, translateConfig(err)
	}
	command, err := s.trackerCommand(cfg)
	if err != nil {
		return api.TicketList{}, err
	}

	// The daemon holds the bound, because it is half of a contract: the client
	// waits for this plus a margin, so that a tracker which will not answer is
	// reported by the process that knows what it was waiting for
	// (api.TicketTimeout).
	bounded, cancel := context.WithTimeout(ctx, s.ticketTimeout())
	defer cancel()

	// The time is taken before the command runs, so that a snapshot composed
	// from one of these tickets says the ticket was read no later than it was.
	readAt := s.now()
	tickets, err := tracker.List(bounded, s.tracker, command)
	if err != nil {
		s.logger.WarnContext(ctx, "reading a project's tickets",
			slog.String("project", id.String()), slog.Any("error", err))

		var rejected *tracker.RejectionError
		if errors.As(err, &rejected) {
			// The request was well formed and the project's own command was
			// not. Saying so as an invalid request is the closest the transport
			// has, and the message names what was wrong so that the answer is
			// actionable wherever it is read.
			return api.TicketList{}, fmt.Errorf("%w: %w", api.ErrInvalid, err)
		}
		return api.TicketList{}, err
	}

	list := api.TicketList{ReadAt: readAt, Tickets: make([]api.Ticket, 0, len(tickets))}
	for _, ticket := range tickets {
		list.Tickets = append(list.Tickets, api.Ticket{
			Reference: ticket.Reference,
			Title:     ticket.Title,
			Body:      ticket.Body,
			URL:       ticket.URL,
			State:     ticket.State,
			Source:    ticket.Source,
		})
	}
	return list, nil
}

// ticketTimeout is how long the daemon waits for a tracker command.
func (s *service) ticketTimeout() time.Duration {
	if s.ticketOverride > 0 {
		return s.ticketOverride
	}
	return api.TicketTimeout
}

// trackerCommand resolves a project's tracker section into a command to run.
//
// The directory is the user's home rather than a repository or whatever
// directory the daemon was started in: there is no task yet, so there is no
// worktree, and a project whose tickets are filed somewhere no repository
// knows about is the ordinary case (ADR-071). `feat doctor` resolves the same
// directory, so a command that answers one answers the other.
func (s *service) trackerCommand(cfg *config.Config) (tracker.Command, error) {
	if cfg.Tracker == nil || len(cfg.Tracker.Command) == 0 {
		return tracker.Command{}, fmt.Errorf(
			"%w: project %s configures no tracker, so Feat has nowhere to read tickets from; "+
				"add a tracker section naming a command that prints them",
			api.ErrNotFound, cfg.Project.ID)
	}
	return tracker.Command{
		Program:   cfg.Tracker.Command[0],
		Arguments: cfg.Tracker.Command[1:],
		Directory: s.env.Home,
	}, nil
}
