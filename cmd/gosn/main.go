package main

import (
	"flag"
	"github.com/bosim/gosyncnotifier/internal"
	"log"
	"log/slog"
	"time"
)

func setupLogger(level string) {
	// remove timestamps
	log.SetFlags(0)

	// handle log level
	var l slog.Level
	l.UnmarshalText([]byte(level))
	slog.SetLogLoggerLevel(l)
}

func main() {
	timerInterval := flag.Int("timer-interval", 10, "Specifies the interval (in minutes) on how often the timer part should trigger")
	watcherPath := flag.String("watcher-path", "", "The path that the notifier should monitor")
	loglevel := flag.String("log-level", "info", "The minimum loglevel")
	flag.Parse()

	setupLogger(*loglevel)

	syncChannel := make(chan internal.SyncEvent, 1)

	cmd := flag.Args()
	runner := internal.NewRunner(cmd)

	if len(*watcherPath) > 0 {
		watcher := internal.NewWatcher(syncChannel, *watcherPath)
		go watcher.Run()
	} else {
		slog.Warn("watcher-path not specified, disabling watcher")
	}

	timer := internal.NewTimer(syncChannel, time.Duration(*timerInterval)*time.Minute)
	go timer.Run()

	syncChannel <- internal.SyncEvent{
		Origin: internal.OriginInit,
	}
	for {
		e := <-syncChannel

		/* Event was generated from file modification */
		if e.Type != internal.SyncEventNone {
			timer.Reset()
		}

		if e.Type != internal.SyncEventNone {
			slog.Info("Got event for file will run command",
				"filename", e.Filename,
				"type", e.Type.String(),
				"origin", e.Origin.String())
		} else {
			slog.Info("Got event will run command",
				"origin", e.Origin.String())
		}

		if err := runner.Run(); err != nil {
			slog.Error("Got error", "error", err)
		}

	}
}
