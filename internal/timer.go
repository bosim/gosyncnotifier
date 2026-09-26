package internal

import (
	"time"
)

type Timer struct {
	nextTime int64
	syncChan chan SyncEvent
	interval time.Duration
}

func (timer *Timer) Run() {
	// Use a ticker (instead of sleep) to compensate for laptop being suspended
	// We would like to trigger as it wakes up which sleep won't do
	ticker := time.NewTicker(time.Duration(10) * time.Second)

	for range ticker.C {
		if time.Now().Unix() >= timer.nextTime {
			timer.syncChan <- SyncEvent{
				Origin: OriginTimer,
			}
			timer.Reset()
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
