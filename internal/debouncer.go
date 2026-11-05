package internal

import (
	"context"
	"log/slog"
	"time"
)

// debouncerInputBuffer absorbs filesystem event bursts so the watcher can keep
// draining the underlying inotify queue while a burst is being coalesced.
const debouncerInputBuffer = 64

// Debouncer coalesces a burst of file events into a single SyncEvent that is
// forwarded only after no further event arrived for the configured quiet
// period. This keeps a burst of filesystem activity (an editor save, a copy
// into the watched tree, ...) from triggering one command run per event. The
// forwarded event still describes the whole burst: Events lists every
// affected file (deduplicated).
//
// Only the watcher's events pass through the debouncer; timer and init events
// are sent to the sync channel directly.
//
// Run holds all of its mutable state in local variables and is therefore safe
// for a single goroutine without additional locking.
type Debouncer struct {
	in    chan SyncEvent
	out   chan SyncEvent
	quiet time.Duration
}

// NewDebouncer creates a Debouncer that reads raw watcher events from its own
// input channel (exposed by In and typically handed to the Watcher) and
// forwards coalesced events to out once quiet has elapsed without further
// activity. A non-positive quiet period disables coalescing: Run then
// forwards every event unchanged.
func NewDebouncer(out chan SyncEvent, quiet time.Duration) *Debouncer {
	return &Debouncer{
		in:    make(chan SyncEvent, debouncerInputBuffer),
		out:   out,
		quiet: quiet,
	}
}

// In returns the channel raw watcher events must be written to.
func (d *Debouncer) In() chan SyncEvent {
	return d.in
}

// Run coalesces incoming events until the quiet period elapses, then forwards
// the merged event. It returns when ctx is cancelled or the input channel is
// closed; a burst still pending when ctx is cancelled is dropped, while a
// closed input flushes the pending burst first (no further events are coming).
func (d *Debouncer) Run(ctx context.Context) {
	if d.quiet <= 0 {
		d.runPassthrough(ctx)
		return
	}

	timer := time.NewTimer(d.quiet)
	stopTimer(timer) // start idle, no expiry armed until the first event
	defer timer.Stop()

	// timerC is nil while no burst is in progress, which parks that select
	// case. It mirrors the Go idiom for arming a timer inside a select loop.
	var timerC <-chan time.Time
	var b burst

	for {
		select {
		case ev, ok := <-d.in:
			if !ok {
				// Input closed: the watcher is gone, deliver what we have
				// instead of waiting for a quiet period that may never come.
				if b.pending() && !d.send(ctx, b.take()) {
					return
				}
				return
			}

			b.add(ev)
			// Debounce: every new event restarts the quiet clock, so nothing
			// is forwarded while the filesystem keeps changing.
			stopTimer(timer)
			timer.Reset(d.quiet)
			timerC = timer.C

		case <-timerC:
			// The filesystem went quiet, flush the coalesced burst.
			timerC = nil
			ev := b.take()
			slog.Debug("Forwarded debounced file events",
				"filename", ev.Filename,
				"type", ev.Type.String(),
				"files", len(ev.Events))
			if !d.send(ctx, ev) {
				return
			}

		case <-ctx.Done():
			if b.pending() {
				slog.Debug("Dropping debounced file events during shutdown",
					"files", len(b.event.Events))
			}
			return
		}
	}
}

// runPassthrough forwards events unchanged when debouncing is disabled,
// preserving the pre-debouncer behaviour exactly (including event order and
// the nil Events field).
func (d *Debouncer) runPassthrough(ctx context.Context) {
	for {
		select {
		case ev, ok := <-d.in:
			if !ok {
				return
			}
			if !d.send(ctx, ev) {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// send forwards an event to the output channel, giving up if the context is
// cancelled first. This keeps Run responsive during shutdown even when the
// receiver has stopped consuming events. It returns false when the send was
// abandoned due to cancellation.
func (d *Debouncer) send(ctx context.Context, ev SyncEvent) bool {
	select {
	case d.out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// burst accumulates the file events of one debounce window into a single
// SyncEvent: Events deduplicates per file in first-appearance order with the
// latest event type winning.
//
// The burst lives entirely inside Debouncer's Run loop, so it needs no
// locking; the index map keeps deduplication linear in the burst size.
type burst struct {
	event SyncEvent
	index map[string]int // filename → position in event.Events
}

// add folds one watcher event into the burst.
func (b *burst) add(ev SyncEvent) {
	if b.index == nil {
		// First event of the window: start the index and record the origin.
		b.index = make(map[string]int)
		b.event.Origin = ev.Origin
	}

	b.event.Filename = ev.Filename // keep the most recent file for the log
	b.event.Type = ev.Type

	if i, ok := b.index[ev.Filename]; ok {
		// Known file: latest type wins, first-appearance position is kept.
		b.event.Events[i].Type = ev.Type
		return
	}

	b.event.Events = append(b.event.Events, FileEvent{
		Filename: ev.Filename,
		Type:     ev.Type,
	})
	b.index[ev.Filename] = len(b.event.Events) - 1
}

// pending reports whether the burst holds any events.
func (b *burst) pending() bool {
	return b.event.Events != nil
}

// take returns the accumulated event and resets the burst for the next
// debounce window.
func (b *burst) take() SyncEvent {
	ev := b.event
	b.event = SyncEvent{}
	b.index = nil
	return ev
}

// stopTimer stops t and drains a possible stale expiry, so a subsequent Reset
// cannot fire immediately.
func stopTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}
