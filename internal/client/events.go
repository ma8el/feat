package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ma8el/feat/internal/api"
)

// maxEventBytes bounds one event frame.
const maxEventBytes = 1 << 20

// ErrStreamLost reports that the daemon ended the stream because this client
// fell too far behind.
//
// It is a distinct error because the recovery is specific: the client's view of
// state has gaps, so it has to read current state again rather than reconnect and
// carry on (ADR-027).
var ErrStreamLost = errors.New("the daemon ended this event stream because it fell behind")

// Events consumes the daemon's event stream, calling handle for each item in
// order.
//
// It returns when the context ends, when handle returns an error, or when the
// daemon ends the stream. A stream the daemon ended because this client fell
// behind returns ErrStreamLost, after handle has seen the final item, so a
// caller cannot mistake lost events for quiet ones.
func (c *Client) Events(ctx context.Context, handle func(api.Event) error) error {
	// No request timeout: an event stream is idle most of the time, and its
	// lifetime is the caller's context.
	response, err := c.get(ctx, "/events", nil)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	if err := failed(response, "/events"); err != nil {
		return err
	}

	reader := bufio.NewReaderSize(response.Body, 8<<10)
	var data strings.Builder

	for {
		line, err := readLine(reader)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || ctx.Err() != nil {
				// The daemon closed the stream, or the caller stopped caring.
				return ctx.Err()
			}
			return fmt.Errorf("reading the event stream: %w", err)
		}

		switch {
		case line == "":
			// A blank line ends one event. An event with no data is a comment
			// block, such as the daemon's heartbeat.
			if data.Len() == 0 {
				continue
			}
			payload := data.String()
			data.Reset()

			var event api.Event
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				return fmt.Errorf("the daemon sent an event this build cannot read: %w", err)
			}
			if err := handle(event); err != nil {
				return err
			}
			if event.Kind == api.KindStreamLost {
				return fmt.Errorf("%w: %s", ErrStreamLost, event.Detail)
			}

		case strings.HasPrefix(line, ":"):
			// A comment. Heartbeats arrive this way and mean the connection is
			// alive, which is all a reader needs from them.

		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))

		default:
			// id and event fields are carried inside the payload as well, so
			// there is nothing to parse out of them here.
		}
	}
}

// readLine reads one line without its terminator, rejecting a frame that would
// grow without bound.
func readLine(reader *bufio.Reader) (string, error) {
	var line strings.Builder

	for {
		chunk, isPrefix, err := reader.ReadLine()
		if err != nil {
			return "", err
		}
		line.Write(chunk)
		if line.Len() > maxEventBytes {
			return "", fmt.Errorf("an event line exceeded %d bytes", maxEventBytes)
		}
		if !isPrefix {
			return line.String(), nil
		}
	}
}
