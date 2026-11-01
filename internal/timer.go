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
	// Use a ticker (instead of sleep) to compensate for laptop being suspended
	// We would like to trigger as it wakes up which sleep won't do
	ticker := time.NewTicker(min(timer.interval,
		time.Duration(10) * time.Second))

	for {
		select {
		case <-ticker.C:
			if time.Now().Unix() >= timer.nextTime {
				timer.syncChan <- SyncEvent{
					Origin: OriginTimer,
				}
				timer.Reset()
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
