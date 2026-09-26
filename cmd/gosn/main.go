package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/bosim/gosyncnotifier/internal"
)

const programName = "gosn"

func setupLogger(level string) {
	// remove timestamps
	log.SetFlags(0)

	// handle log level
	var l slog.Level
	l.UnmarshalText([]byte(level))
	slog.SetLogLoggerLevel(l)
}

func main() {
	timerInterval := flag.Int("timer-interval", 10, "Specifies the interval (in minutes) on how often the timer part should trigger, 0 is disabled")
	watcherPath := flag.String("watcher-path", "", "The path that the notifier should monitor")
	loglevel := flag.String("log-level", "info", "The minimum loglevel")

	flag.Usage = func() {
		fmt.Printf("%s - run commands based on timer or file activity\n\n", programName)
		fmt.Printf("Basic syntax:\n")
		fmt.Printf("  %s <flags> -- <command>\n\n", programName)
		fmt.Printf("Command will be run if either timer is triggered or new file activity occur\n\n")
		fmt.Printf("The following flags are available:\n\n")

		flag.PrintDefaults()

		fmt.Printf("\nExample:\n")
		fmt.Printf("  %s -timer-interval 0 -watcher-path /home/user/test -- rsync -avz -e ssh /home/user/test user@server:/dir\n\n", programName)
	}

	flag.Parse()

	setupLogger(*loglevel)

	syncChannel := make(chan internal.SyncEvent, 1)

	cmd := flag.Args()
	if len(cmd) == 0 {
		slog.Error("You need to specify a command to run, gosn ... -- command")
		flag.Usage()
		os.Exit(1)
	}

	runner := internal.NewRunner(cmd)

	var watcher *internal.Watcher
	var timer *internal.Timer

	if len(*watcherPath) > 0 {
		watcher = internal.NewWatcher(syncChannel, *watcherPath)
		go watcher.Run()
	} else {
		slog.Warn("watcher-path not specified, disabling watcher")
	}

	if *timerInterval > 0 {
		timer = internal.NewTimer(syncChannel, time.Duration(*timerInterval)*time.Minute)
		go timer.Run()
	} else {
		slog.Warn("timer-interval is 0, disabling timer")
	}

	if timer == nil && watcher == nil {
		slog.Error("either timer or watcher needs to be active")
		os.Exit(1)
	}

	notifier, err := internal.NewNotifier()
	if err != nil {
		slog.Warn("notify-send is not found")
	}

	syncChannel <- internal.SyncEvent{
		Origin: internal.OriginInit,
	}
	for {
		e := <-syncChannel

		/* Event was generated from file modification */
		if e.Type != internal.SyncEventNone && timer != nil {
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
			slog.Error("Error running command", "error", err)
			notifier.Critical(fmt.Sprintf("Error running command: %v", err))
		}

	}
}
