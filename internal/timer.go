package internal

import (
	"context"
	"log/slog"
	"time"
)

type Timer struct {
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
			if time.Now().Unix() >= timer.nextTime {
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
	timer.nextTime = time.Now().Unix() + int64(timer.interval.Seconds())
}

func NewTimer(syncChan chan SyncEvent, interval time.Duration) *Timer {
	return &Timer{
		nextTime: time.Now().Unix() + int64(interval.Seconds()),
		syncChan: syncChan,
		interval: interval,
	}
}
