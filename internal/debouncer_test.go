package internal

import (
	"context"
	"os"
	"path/filepath"
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

func TestMergeEvent(t *testing.T) {
	first := SyncEvent{
		Filename: "first.txt",
		Type:     SyncEventCreated,
		Origin:   OriginWatcher,
	}

	merged := mergeEvent(nil, first)
	if merged.Count != 1 {
		t.Errorf("first merge Count = %d, want 1", merged.Count)
	}
	if merged.Filename != "first.txt" || merged.Type != SyncEventCreated {
		t.Errorf("first merge = %+v, want first.txt/created", merged)
	}

	second := SyncEvent{
		Filename: "second.txt",
		Type:     SyncEventModified,
		Origin:   OriginWatcher,
	}
	merged = mergeEvent(merged, second)
	if merged.Count != 2 {
		t.Errorf("second merge Count = %d, want 2", merged.Count)
	}
	if merged.Filename != "second.txt" {
		t.Errorf("Filename = %q, want the most recent file %q", merged.Filename, "second.txt")
	}
	if merged.Type != SyncEventModified {
		t.Errorf("Type = %v, want the most recent type %v", merged.Type, SyncEventModified)
	}
	if merged.Origin != OriginWatcher {
		t.Errorf("Origin = %v, want %v", merged.Origin, OriginWatcher)
	}
}

// TestDebouncerCoalescesBurst verifies that a rapid burst of events results in
// exactly one forwarded event carrying the merged count and the most recent
// filename.
func TestDebouncerCoalescesBurst(t *testing.T) {
	quiet := 100 * time.Millisecond
	out := make(chan SyncEvent, 4)
	d := NewDebouncer(out, quiet)

	startDebouncer(t, d, t.Context())

	for i := range 5 {
		d.In() <- SyncEvent{
			Filename: filepath.Join("dir", "file"+string(rune('a'+i))),
			Type:     SyncEventModified,
			Origin:   OriginWatcher,
		}
	}

	event := receiveEvent(t, out)
	if event.Count != 5 {
		t.Errorf("Count = %d, want 5 coalesced events", event.Count)
	}
	if want := filepath.Join("dir", "filee"); event.Filename != want {
		t.Errorf("Filename = %q, want the most recent file %q", event.Filename, want)
	}
	if event.Type != SyncEventModified {
		t.Errorf("Type = %v, want %v", event.Type, SyncEventModified)
	}
	if event.Origin != OriginWatcher {
		t.Errorf("Origin = %v, want %v", event.Origin, OriginWatcher)
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
	if first.Filename != "a.txt" || first.Count != 1 {
		t.Errorf("first event = %+v, want a.txt with Count 1", first)
	}

	d.In() <- SyncEvent{Filename: "b.txt", Type: SyncEventDeleted, Origin: OriginWatcher}
	second := receiveEvent(t, out)
	if second.Filename != "b.txt" || second.Count != 1 {
		t.Errorf("second event = %+v, want b.txt with Count 1", second)
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
// period forwards every event unchanged, preserving order and the zero Count.
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
		if event.Count != 0 {
			t.Errorf("Count = %d, want 0 when debouncing is disabled", event.Count)
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
	if event.Filename != "flushed.txt" || event.Count != 1 {
		t.Errorf("event = %+v, want flushed.txt with Count 1", event)
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
	if event.Count < 1 {
		t.Errorf("Count = %d, want at least 1", event.Count)
	}

	// The whole burst must collapse into that single event.
	assertNoEvent(t, out, 3*quiet)
}
