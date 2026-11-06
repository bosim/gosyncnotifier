package internal

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type Timer struct {
	// mu guards nextTime: Run reads it from the timer goroutine while Reset
	// is called from the event loop and NextRun from the control server.
	mu       sync.Mutex
	nextTime int64
	syncChan chan SyncEvent
	interval time.Duration
}

func (timer *Timer) Run(ctx context.Context) {
	if timer.interval <= 0 {
		slog.Info("Timer Run disabled by non-positive interval", "interval", timer.interval)
		return
	}

	// Use a ticker (instead of sleep) to compensate for laptop being suspended
	// We would like to trigger as it wakes up which sleep won't do
	ticker := time.NewTicker(min(timer.interval, 10*time.Second))
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Now().Unix() >= timer.nextRunUnix() {
				select {
				case timer.syncChan <- SyncEvent{Origin: OriginTimer}:
					timer.Reset()
				case <-ctx.Done():
					slog.Info("Timer Run loop stopped by context", "reason", ctx.Err())
					return
				}
			}
		case <-ctx.Done():
			slog.Info("Timer Run loop stopped by context", "reason", ctx.Err())
			return
		}
	}
}

func (timer *Timer) Reset() {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	timer.nextTime = time.Now().Unix() + int64(timer.interval.Seconds())
}

// nextRunUnix returns the next scheduled firing time in Unix seconds.
func (timer *Timer) nextRunUnix() int64 {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	return timer.nextTime
}

// NextRun returns the wall clock time of the next scheduled trigger, which is
// already in the past if the timer is due. It is used for status reporting.
func (timer *Timer) NextRun() time.Time {
	return time.Unix(timer.nextRunUnix(), 0)
}

func NewTimer(syncChan chan SyncEvent, interval time.Duration) *Timer {
	return &Timer{
		nextTime: time.Now().Unix() + int64(interval.Seconds()),
		syncChan: syncChan,
		interval: interval,
	}
}
