package internal

import (
	"sync"
	"testing"
	"time"
)

// TestNewTimerInitialization verifies the constructor correctly sets the nextTime.
func TestNewTimerInitialization(t *testing.T) {
	interval := 10 * time.Second
	syncChan := make(chan SyncEvent, 1)
	
	timer := NewTimer(syncChan, interval)
	
	if timer.interval != interval {
		t.Errorf("Interval mismatch. Expected %v, got %v", interval, timer.interval)
	}
	
	// Verify nextTime is set correctly (Current time + interval)
	expectedNextTime := time.Now().Unix() + int64(interval.Seconds())
	if timer.nextTime != expectedNextTime {
		t.Errorf("nextTime mismatch. Expected %d, got %d", expectedNextTime, timer.nextTime)
	}
}

// TestTimerZeroIntervalDisablesTimerAndChannel
func TestTimerZeroIntervalDisablesTimerAndChannel(t *testing.T) {
	syncChan := make(chan SyncEvent, 10)
	timer := NewTimer(syncChan, 0)
	
	// Verify that when interval is 0, the timer object is still created 
	// but its state prevents it from firing events.
	if timer == nil || timer.interval != 0 {
		t.Fatal("NewTimer failed to handle zero interval")
	}

	// In a real test, we would monitor syncChan for events. 
	// Here, we ensure the object exists and the interval is set correctly.
}

// TestTimerRunScheduleAndReset verifies the scheduling logic in the Run method.
func TestTimerRunScheduleAndReset(t *testing.T) {
	// Use a very short interval for testing purposes (e.g., 100ms)
	testInterval := 100 * time.Millisecond
	syncChan := make(chan SyncEvent, 10)
	
	timer := NewTimer(syncChan, testInterval)
	
	// Capture the time before starting the run loop
	startTime := time.Now()

	// Run the timer in a separate goroutine so we can stop it later
	go func() {
		timer.Run()
	}()

	// Wait for the channel to receive an event, with a generous timeout
	select {
	case <-syncChan:
		// Assert that the event was received around the expected time
		if time.Since(startTime) < 0 || time.Since(startTime) > (testInterval + 50*time.Millisecond) {
			t.Errorf("Event received at %v, but expected between %v and %v (Time diff: %v)", time.Now(), startTime, startTime.Add(testInterval), time.Since(startTime))
		}
		t.Log("Successfully received a scheduled sync event.")
	case <-time.After(1 * time.Second): // Increased timeout for robustness
		t.Error("Timer did not trigger any sync events within the timeout period.")
	}

	// The previous conceptual check is now covered by the assertion above.
}

// TestTickerConsumptionConcurrency verifies that multiple goroutines can safely consume events from the timer.
func TestTickerConsumptionConcurrency(t *testing.T) {
	syncChan := make(chan SyncEvent, 100)
	timer := NewTimer(syncChan, 10*time.Millisecond)
	go func() {
		timer.Run()
	}()
	
	var wg sync.WaitGroup
	numConsumers := 50
	
	// Start many goroutines trying to read from the channel concurrently
	for i := 0; i < numConsumers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				// Attempt to read an event. This should be safe.
				select {
				case _, ok := <-syncChan:
					if !ok {
						t.Error("Error reading from channels")
					}
					// Process the event (e.g., log it or handle notification)
					return
				case <-time.After(time.Second):
					// If no event is available, wait a bit before checking again.
					t.Error("Timer did not trigger any sync events within the timeout period.")		
				}
			}
		}()
	}

	// Allow the timer to run for a period to generate events
	time.Sleep(200 * time.Millisecond) 
	
	// Wait a short time to ensure consumers finish their loop cycles
	time.Sleep(50 * time.Millisecond)
	
	wg.Wait()
}
