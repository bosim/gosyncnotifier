package internal

import (
	"context"
	"testing"
	"time"
)

const timerTestTimeout = 2 * time.Second

// TestNewTimerInitialization verifies the constructor correctly sets the nextTime.
func TestNewTimerInitialization(t *testing.T) {
	interval := 10 * time.Second
	syncChan := make(chan SyncEvent, 1)

	before := time.Now().Unix()
	timer := NewTimer(syncChan, interval)
	after := time.Now().Unix()

	if timer.interval != interval {
		t.Errorf("Interval mismatch. Expected %v, got %v", interval, timer.interval)
	}

	// nextTime uses Unix seconds, so allow for the clock crossing a second
	// boundary while the constructor runs.
	minNextTime := before + int64(interval.Seconds())
	maxNextTime := after + int64(interval.Seconds())
	if timer.nextTime < minNextTime || timer.nextTime > maxNextTime {
		t.Errorf("nextTime = %d, want between %d and %d", timer.nextTime, minNextTime, maxNextTime)
	}
}

// TestTimerReset verifies Reset schedules the next event one interval away.
func TestTimerReset(t *testing.T) {
	interval := 5 * time.Second
	timer := NewTimer(make(chan SyncEvent, 1), interval)

	// Move nextTime away from its initial value so we can tell Reset changed it.
	timer.nextTime = 0

	before := time.Now().Unix()
	timer.Reset()
	after := time.Now().Unix()

	minNextTime := before + int64(interval.Seconds())
	maxNextTime := after + int64(interval.Seconds())
	if timer.nextTime < minNextTime || timer.nextTime > maxNextTime {
		t.Errorf("nextTime = %d, want between %d and %d", timer.nextTime, minNextTime, maxNextTime)
	}
}

// TestTimerRunWithZeroIntervalIsDisabled verifies that a non-positive interval
// makes Run return immediately without emitting events.
func TestTimerRunWithZeroIntervalIsDisabled(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
	}{
		{name: "zero interval", interval: 0},
		{name: "negative interval", interval: -time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncChan := make(chan SyncEvent, 1)
			timer := NewTimer(syncChan, tt.interval)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan struct{})
			go func() {
				defer close(done)
				timer.Run(ctx)
			}()

			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("Run did not return for a non-positive interval")
			}

			select {
			case event := <-syncChan:
				t.Errorf("unexpected event for a disabled timer: %+v", event)
			default:
			}
		})
	}
}

// TestTimerRunScheduleAndReset verifies that Run waits for the interval before
// firing, and that it schedules subsequent events rather than firing in a loop.
func TestTimerRunScheduleAndReset(t *testing.T) {
	testInterval := time.Second
	syncChan := make(chan SyncEvent, 10)
	timer := NewTimer(syncChan, testInterval)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(timerTestTimeout):
			t.Error("Run did not stop during cleanup")
		}
	})

	// The first event must not arrive before the interval. The generous lower
	// bound keeps this reliable on a loaded machine.
	select {
	case event := <-syncChan:
		t.Fatalf("received event before the interval elapsed: %+v", event)
	case <-time.After(250 * time.Millisecond):
	}

	first := receiveTimerEvent(t, syncChan)
	if first.Origin != OriginTimer {
		t.Errorf("Origin = %v, want %v", first.Origin, OriginTimer)
	}
	if first.Type != SyncEventNone {
		t.Errorf("Type = %v, want %v", first.Type, SyncEventNone)
	}

	// A second event should be scheduled, not emitted immediately.
	select {
	case event := <-syncChan:
		t.Fatalf("received a second event too soon after the first: %+v", event)
	case <-time.After(100 * time.Millisecond):
	}

	second := receiveTimerEvent(t, syncChan)
	if second.Origin != OriginTimer {
		t.Errorf("Origin = %v, want %v", second.Origin, OriginTimer)
	}
	if second.Type != SyncEventNone {
		t.Errorf("Type = %v, want %v", second.Type, SyncEventNone)
	}
}

// TestTimerRunStopsOnContextCancellation verifies that Run exits cleanly when
// its context is canceled.
func TestTimerRunStopsOnContextCancellation(t *testing.T) {
	syncChan := make(chan SyncEvent, 1)
	timer := NewTimer(syncChan, 10*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer.Run(ctx)
	}()

	// Give Run a moment to start its ticker before cancelling.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(timerTestTimeout):
		t.Fatal("Run did not return after context cancellation")
	}
}

func receiveTimerEvent(t *testing.T, syncChan chan SyncEvent) SyncEvent {
	t.Helper()

	select {
	case event := <-syncChan:
		return event
	case <-time.After(timerTestTimeout):
		t.Fatal("timed out waiting for timer event")
		return SyncEvent{}
	}
}
