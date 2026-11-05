package internal

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// assertNoEvent fails the test if an event arrives on ch within d.
func assertNoEvent(t *testing.T, ch chan SyncEvent, d time.Duration) {
	t.Helper()

	select {
	case ev := <-ch:
		t.Errorf("unexpected event: %+v", ev)
	case <-time.After(d):
	}
}

// waitForRunExit waits for a Run goroutine to return after cancellation.
func waitForRunExit(t *testing.T, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(watcherTestTimeout):
		t.Fatal("Run did not return")
	}
}

// startDebouncer runs d in the background and registers a cleanup that
// verifies it stops once the test context is cancelled.
func startDebouncer(t *testing.T, d *Debouncer, ctx context.Context) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Run(ctx)
	}()

	t.Cleanup(func() {
		waitForRunExit(t, done)
	})
}

func TestNewDebouncer(t *testing.T) {
	out := make(chan SyncEvent, 1)
	d := NewDebouncer(out, time.Second)

	if d.out != out {
		t.Error("out channel was not stored on the Debouncer")
	}
	if d.quiet != time.Second {
		t.Errorf("quiet = %v, want %v", d.quiet, time.Second)
	}
	if d.In() == nil {
		t.Fatal("In() returned a nil channel")
	}
	if cap(d.In()) != debouncerInputBuffer {
		t.Errorf("input channel capacity = %d, want %d", cap(d.In()), debouncerInputBuffer)
	}
}

// TestBurstDeduplicatesPerFile verifies the accumulator deduplicates per file
// in first-appearance order with the latest type winning, and that take()
// resets it for the next window.
func TestBurstDeduplicatesPerFile(t *testing.T) {
	var b burst

	if b.pending() {
		t.Fatal("fresh burst reports pending events")
	}

	// a(created) a(modified) b(deleted) a(modified)
	b.add(SyncEvent{Filename: "a.txt", Type: SyncEventCreated, Origin: OriginWatcher})
	b.add(SyncEvent{Filename: "a.txt", Type: SyncEventModified, Origin: OriginWatcher})
	b.add(SyncEvent{Filename: "b.txt", Type: SyncEventDeleted, Origin: OriginWatcher})
	b.add(SyncEvent{Filename: "a.txt", Type: SyncEventModified, Origin: OriginWatcher})

	if !b.pending() {
		t.Fatal("burst with events reports no pending events")
	}
	wantEvents := []FileEvent{
		{Filename: "a.txt", Type: SyncEventModified},
		{Filename: "b.txt", Type: SyncEventDeleted},
	}
	if !reflect.DeepEqual(b.event.Events, wantEvents) {
		t.Errorf("Events = %+v, want %+v", b.event.Events, wantEvents)
	}
	if b.event.Filename != "a.txt" || b.event.Type != SyncEventModified {
		t.Errorf("most recent = %q/%v, want a.txt/modified", b.event.Filename, b.event.Type)
	}
	if b.event.Origin != OriginWatcher {
		t.Errorf("Origin = %v, want %v", b.event.Origin, OriginWatcher)
	}

	ev := b.take()
	if !reflect.DeepEqual(ev.Events, wantEvents) {
		t.Errorf("take() = %+v, want %+v", ev.Events, wantEvents)
	}
	if b.pending() {
		t.Error("burst still pending after take()")
	}
	if b.event.Events != nil {
		t.Errorf("burst not reset after take(): %+v", b.event)
	}
}

// TestDebouncerCoalescesBurst verifies that a rapid burst of events results in
// exactly one forwarded event whose Events deduplicates per file in
// first-appearance order with the latest type.
func TestDebouncerCoalescesBurst(t *testing.T) {
	quiet := 100 * time.Millisecond
	out := make(chan SyncEvent, 4)
	d := NewDebouncer(out, quiet)

	startDebouncer(t, d, t.Context())

	for _, ev := range []SyncEvent{
		{Filename: "a.txt", Type: SyncEventCreated, Origin: OriginWatcher},
		{Filename: "b.txt", Type: SyncEventModified, Origin: OriginWatcher},
		{Filename: "a.txt", Type: SyncEventModified, Origin: OriginWatcher},
		{Filename: "c.txt", Type: SyncEventCreated, Origin: OriginWatcher},
		{Filename: "b.txt", Type: SyncEventModified, Origin: OriginWatcher},
	} {
		d.In() <- ev
	}

	event := receiveEvent(t, out)
	if event.Filename != "b.txt" {
		t.Errorf("Filename = %q, want the most recent file %q", event.Filename, "b.txt")
	}
	if event.Type != SyncEventModified {
		t.Errorf("Type = %v, want %v", event.Type, SyncEventModified)
	}
	if event.Origin != OriginWatcher {
		t.Errorf("Origin = %v, want %v", event.Origin, OriginWatcher)
	}
	wantEvents := []FileEvent{
		{Filename: "a.txt", Type: SyncEventModified},
		{Filename: "b.txt", Type: SyncEventModified},
		{Filename: "c.txt", Type: SyncEventCreated},
	}
	if !reflect.DeepEqual(event.Events, wantEvents) {
		t.Errorf("Events = %+v, want %+v", event.Events, wantEvents)
	}

	// The burst must not produce any follow-up events.
	assertNoEvent(t, out, 3*quiet)
}

// TestDebouncerSeparatesBursts verifies that activity resuming after the quiet
// period starts a new burst and therefore a new forwarded event.
func TestDebouncerSeparatesBursts(t *testing.T) {
	quiet := 50 * time.Millisecond
	out := make(chan SyncEvent, 4)
	d := NewDebouncer(out, quiet)

	startDebouncer(t, d, t.Context())

	d.In() <- SyncEvent{Filename: "a.txt", Type: SyncEventCreated, Origin: OriginWatcher}
	first := receiveEvent(t, out)
	if first.Filename != "a.txt" {
		t.Errorf("first event = %+v, want a.txt", first)
	}
	if len(first.Events) != 1 || first.Events[0].Filename != "a.txt" {
		t.Errorf("first event Events = %+v, want a single a.txt entry", first.Events)
	}

	d.In() <- SyncEvent{Filename: "b.txt", Type: SyncEventDeleted, Origin: OriginWatcher}
	second := receiveEvent(t, out)
	if second.Filename != "b.txt" {
		t.Errorf("second event = %+v, want b.txt", second)
	}
	if len(second.Events) != 1 || second.Events[0].Filename != "b.txt" {
		t.Errorf("second event Events = %+v, want a single b.txt entry", second.Events)
	}
}

// TestDebouncerCancelDropsPending verifies that a burst pending during
// shutdown is dropped instead of being forwarded, and that Run returns
// promptly.
func TestDebouncerCancelDropsPending(t *testing.T) {
	quiet := 5 * time.Second // long enough that the burst cannot flush first
	out := make(chan SyncEvent, 4)
	d := NewDebouncer(out, quiet)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Run(ctx)
	}()

	d.In() <- SyncEvent{Filename: "pending.txt", Type: SyncEventModified, Origin: OriginWatcher}
	// Give Run time to absorb the event into the pending burst.
	time.Sleep(100 * time.Millisecond)

	cancel()
	waitForRunExit(t, done)
	assertNoEvent(t, out, 100*time.Millisecond)
}

// TestDebouncerCancelWhileBlockedOnSend verifies that Run returns on
// cancellation even while blocked delivering the flushed burst to a receiver
// that stopped consuming, mirroring the watcher's shutdown guarantee.
func TestDebouncerCancelWhileBlockedOnSend(t *testing.T) {
	quiet := 50 * time.Millisecond
	out := make(chan SyncEvent) // unbuffered, no consumer
	d := NewDebouncer(out, quiet)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Run(ctx)
	}()

	d.In() <- SyncEvent{Filename: "blocking.txt", Type: SyncEventModified, Origin: OriginWatcher}

	// Let the quiet period elapse so Run reaches the blocked send.
	time.Sleep(200 * time.Millisecond)
	cancel()
	waitForRunExit(t, done)
}

// TestDebouncerPassthroughWhenDisabled verifies that a non-positive quiet
// period forwards every event unchanged, preserving order and nil Events.
func TestDebouncerPassthroughWhenDisabled(t *testing.T) {
	out := make(chan SyncEvent, 4)
	d := NewDebouncer(out, 0)

	startDebouncer(t, d, t.Context())

	want := []string{"a.txt", "b.txt", "c.txt"}
	for _, name := range want {
		d.In() <- SyncEvent{Filename: name, Type: SyncEventModified, Origin: OriginWatcher}
	}

	for _, name := range want {
		event := receiveEvent(t, out)
		if event.Filename != name {
			t.Errorf("Filename = %q, want %q", event.Filename, name)
		}
		if event.Events != nil {
			t.Errorf("Events = %+v, want nil when debouncing is disabled", event.Events)
		}
	}
}

// TestDebouncerInputClosedFlushesPending verifies that closing the input
// channel delivers the pending burst rather than waiting for a quiet period
// that can never be interrupted by further events.
func TestDebouncerInputClosedFlushesPending(t *testing.T) {
	quiet := 5 * time.Second // long enough that the quiet period cannot fire
	out := make(chan SyncEvent, 4)
	d := NewDebouncer(out, quiet)

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Run(t.Context())
	}()

	d.In() <- SyncEvent{Filename: "flushed.txt", Type: SyncEventCreated, Origin: OriginWatcher}
	close(d.In())

	event := receiveEvent(t, out)
	if event.Filename != "flushed.txt" {
		t.Errorf("event = %+v, want flushed.txt", event)
	}
	if len(event.Events) != 1 || event.Events[0].Filename != "flushed.txt" {
		t.Errorf("event Events = %+v, want a single flushed.txt entry", event.Events)
	}
	waitForRunExit(t, done)
}

// TestWatcherWithDebouncerCoalescesBurst exercises the full pipeline with
// real filesystem notifications: a burst of writes to one file results in a
// single sync event.
func TestWatcherWithDebouncerCoalescesBurst(t *testing.T) {
	quiet := 250 * time.Millisecond
	dir := t.TempDir()
	out := make(chan SyncEvent, 16)
	d := NewDebouncer(out, quiet)

	watcher, err := NewWatcher(d.In(), dir)
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}
	t.Cleanup(func() {
		_ = watcher.watcher.Close()
	})

	debouncerDone := make(chan struct{})
	go func() {
		defer close(debouncerDone)
		d.Run(t.Context())
	}()
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		watcher.Run(t.Context())
	}()
	t.Cleanup(func() {
		for _, done := range []chan struct{}{debouncerDone, watcherDone} {
			select {
			case <-done:
			case <-time.After(watcherTestTimeout):
				t.Error("Run did not stop during cleanup")
			}
		}
	})

	waitForWatch(t, watcher, dir)

	// Burst: create plus repeated rewrites of the same file.
	file := filepath.Join(dir, "burst.txt")
	for range 5 {
		if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}

	event := receiveEvent(t, out)
	if event.Filename != file {
		t.Errorf("Filename = %q, want %q", event.Filename, file)
	}
	if event.Origin != OriginWatcher {
		t.Errorf("Origin = %v, want %v", event.Origin, OriginWatcher)
	}
	// Every event of the burst was for the same file, so the deduplicated
	// list must contain exactly that one entry.
	if len(event.Events) != 1 || event.Events[0].Filename != file {
		t.Errorf("Events = %+v, want a single entry for %q", event.Events, file)
	}

	// The whole burst must collapse into that single event.
	assertNoEvent(t, out, 3*quiet)
}
