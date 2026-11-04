package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
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
	debounceInterval := flag.Duration("debounce-interval", time.Second, "Quiet period for coalescing file events before running the command, 0 is disabled")
	watcherPath := flag.String("watcher-path", "", "Directory to watch for file changes, empty disables the watcher")
	loglevel := flag.String("log-level", "info", "The minimum log level (debug, info, warn, error)")
	notifierEnabled := flag.Bool("notifier-enabled", true, "Whether desktop notifications are sent or not")
	notifierBackend := flag.String("notifier-backend", string(internal.NotifierBackendDBus), `Notification backend: "dbus" or "command"`)
	notifierExecutable := flag.String("notifier-exec", "notify-send", "Name or location of a notify-send compatible executable (command backend only)")
	notifierPostCommand := flag.Bool("notifier-post-command", false, "Send a low priority desktop notification after the command has completed")

	flag.Usage = func() {
		fmt.Printf("%s - run commands based on timer or file activity\n\n", programName)
		fmt.Printf("Basic syntax:\n")
		fmt.Printf("  %s <flags> -- <command>\n\n", programName)
		fmt.Printf("The command is run when the timer triggers or file activity occurs\n\n")
		fmt.Printf("The following flags are available:\n\n")

		flag.PrintDefaults()

		fmt.Printf("\nExample:\n")
		fmt.Printf("  %s -timer-interval 0 -watcher-path /home/user/test -- rsync -avz -e ssh /home/user/test user@server:/dir\n\n", programName)
	}

	flag.Parse()

	setupLogger(*loglevel)

	backend, err := internal.ParseNotifierBackend(*notifierBackend)
	if err != nil {
		slog.Error("Invalid notifier backend", "error", err)
		os.Exit(1)
	}

	// Cancel the shared context on Ctrl-C (SIGINT) or SIGTERM so the watcher
	// and timer goroutines can shut down gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Restore the default signal disposition after the first signal, so a
	// second Ctrl-C force-quits even if shutdown is waiting on a command.
	go func() {
		<-ctx.Done()
		stop()
		slog.Info("Shutdown requested, press Ctrl-C again to force exit")
	}()

	syncChannel := make(chan internal.SyncEvent, 1)

	cmd := flag.Args()
	if len(cmd) == 0 {
		slog.Error("You need to specify a command to run, gosn ... -- command")
		flag.Usage()
		os.Exit(1)
	}

	var watcher *internal.Watcher
	var timer *internal.Timer
	var wg sync.WaitGroup

	if len(*watcherPath) > 0 {
		var err error

		// Route the watcher through a debouncer so a burst of filesystem
		// events coalesces into a single command run. With the interval at 0
		// the watcher writes to the sync channel directly, exactly as before.
		watcherInput := syncChannel
		var debouncer *internal.Debouncer
		if *debounceInterval > 0 {
			debouncer = internal.NewDebouncer(syncChannel, *debounceInterval)
			watcherInput = debouncer.In()
		} else {
			slog.Info("Debouncing disabled, forwarding file events immediately")
		}

		watcher, err = internal.NewWatcher(watcherInput, *watcherPath)
		if err != nil {
			slog.Error("Unable to initialize watcher", "error", err)
			os.Exit(1)
		}

		if debouncer != nil {
			wg.Go(func() {
				debouncer.Run(ctx)
			})
		}
		wg.Go(func() {
			watcher.Run(ctx)
		})
	} else {
		slog.Warn("watcher-path not specified, disabling watcher")
	}

	if *timerInterval > 0 {
		timer = internal.NewTimer(syncChannel, time.Duration(*timerInterval)*time.Minute)
		wg.Go(func() {
			timer.Run(ctx)
		})
	} else {
		slog.Warn("timer-interval is 0, disabling timer")
	}

	if timer == nil && watcher == nil {
		slog.Error("Either timer or watcher needs to be active")
		os.Exit(1)
	}

	notifier, err := internal.NewNotifier(*notifierEnabled, backend, *notifierExecutable)
	if err != nil {
		slog.Warn("Error creating notifier", "error", err)
	}
	defer notifier.Close()

	runner, err := internal.NewRunner(cmd)
	if err != nil {
		slog.Error("Error creating runner", "error", err)
		os.Exit(1)
	}

	syncChannel <- internal.SyncEvent{
		Origin: internal.OriginInit,
	}

eventLoop:
	for {
		select {
		case <-ctx.Done():
			// A signal arrived. An in-flight command (runner.Run) still runs to
			// completion before we get here; watcher and timer already stopped.
			slog.Info("Shutting down, waiting for watcher and timer to stop", "reason", ctx.Err())
			break eventLoop
		case e := <-syncChannel:
			// A queued event may have raced with the signal; do not start
			// another command while shutting down.
			if ctx.Err() != nil {
				break eventLoop
			}

			/* Event was generated from file modification */
			if e.Type != internal.SyncEventNone && timer != nil {
				timer.Reset()
			}

			if e.Type != internal.SyncEventNone {
				attrs := []any{
					"filename", e.Filename,
					"type", e.Type.String(),
					"origin", e.Origin.String(),
				}
				if e.Count > 1 {
					// A debounced burst of file events triggered this run.
					attrs = append(attrs, "count", e.Count)
				}
				slog.Info("Got event for file will run command", attrs...)
			} else {
				slog.Info("Got event will run command",
					"origin", e.Origin.String())
			}

			if err := runner.Run(); err != nil {
				if ctx.Err() != nil {
					// Shutdown raced with the command; don't report a failure
					// notification while exiting.
					slog.Info("Command did not complete cleanly during shutdown", "error", err)
				} else {
					slog.Error("Error running command", "error", err)
					if notifyErr := notifier.Critical(fmt.Sprintf("Error running command: %v", err)); notifyErr != nil {
						slog.Warn("Error sending notification", "error", notifyErr)
					}
				}
			} else if *notifierPostCommand && ctx.Err() == nil {
				if notifyErr := notifier.Low("Command ran successfully"); notifyErr != nil {
					slog.Warn("Error sending notification", "error", notifyErr)
				}
			}
		}
	}

	wg.Wait()
	slog.Info("Shutdown complete")
}
