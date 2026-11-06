package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/bosim/gosyncnotifier/internal"
)

const programName = "gosnctl"

// Exit codes: 0 success, 1 connection or server-side failure, 2 usage error.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const connectTimeout = 2 * time.Second
const requestTimeout = 10 * time.Second

// localTimeFormat matches the timestamps gosnctl prints for humans.
const localTimeFormat = "2006-01-02 15:04:05"

func main() {
	socketPath := flag.String("socket", internal.DefaultSocketPath(), "Unix socket of the gosn instance to talk to")
	jsonOutput := flag.Bool("json", false, "Print the status command output as JSON")

	flag.Usage = func() {
		fmt.Printf("%s - query and control gosn over its control socket\n\n", programName)
		fmt.Printf("Basic syntax:\n")
		fmt.Printf("  %s [flags] <command> [arguments]\n\n", programName)
		fmt.Printf("Commands:\n")
		fmt.Printf("  status             Show sync status, last sync and configuration (default)\n")
		fmt.Printf("  pause [duration]   Pause syncs until resume, optionally auto-resume\n")
		fmt.Printf("                     after a Go duration such as 30m or 1h30m\n")
		fmt.Printf("  resume             Resume syncs and queue a catch-up run\n")
		fmt.Printf("  trigger            Queue a sync run now\n\n")
		fmt.Printf("The following flags are available:\n\n")
		flag.PrintDefaults()
		fmt.Printf("\nExamples:\n")
		fmt.Printf("  %s status\n", programName)
		fmt.Printf("  %s -json status\n", programName)
		fmt.Printf("  %s pause 30m\n", programName)
		fmt.Printf("  %s resume\n", programName)
	}

	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		args = []string{internal.ControlCmdStatus}
	}

	req, err := buildRequest(args[0], args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", programName, err)
		fmt.Fprintf(os.Stderr, "Try '%s -help'\n", programName)
		os.Exit(exitUsage)
	}

	resp, err := request(*socketPath, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", programName, err)
		os.Exit(exitError)
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "%s: %s\n", programName, resp.Error)
		os.Exit(exitError)
	}

	if args[0] == internal.ControlCmdStatus {
		printStatus(resp, *jsonOutput)
		return
	}

	if resp.Message != "" {
		fmt.Println(resp.Message)
	}
	// Give the indefinite pause a follow-up hint the server cannot phrase.
	if args[0] == internal.ControlCmdPause && req.Duration == "" {
		fmt.Printf("resume with: %s resume\n", programName)
	}
}

// buildRequest validates the command line and turns it into a control
// request. Unknown commands and unexpected arguments are usage errors.
func buildRequest(cmd string, args []string) (internal.ControlRequest, error) {
	switch cmd {
	case internal.ControlCmdStatus, internal.ControlCmdResume, internal.ControlCmdTrigger:
		if len(args) > 0 {
			return internal.ControlRequest{}, fmt.Errorf("%s takes no arguments", cmd)
		}
		return internal.ControlRequest{Cmd: cmd}, nil

	case internal.ControlCmdPause:
		if len(args) > 1 {
			return internal.ControlRequest{}, fmt.Errorf("pause takes at most one duration argument")
		}
		req := internal.ControlRequest{Cmd: internal.ControlCmdPause}
		if len(args) == 1 {
			d, err := time.ParseDuration(args[0])
			if err != nil {
				return internal.ControlRequest{}, fmt.Errorf("invalid pause duration %q: %w", args[0], err)
			}
			if d < 0 {
				return internal.ControlRequest{}, fmt.Errorf("pause duration %q must not be negative", args[0])
			}
			req.Duration = args[0]
		}
		return req, nil

	default:
		return internal.ControlRequest{}, fmt.Errorf("unknown command %q (want status, pause, resume or trigger)", cmd)
	}
}

// request sends one request over the control socket and reads one response.
func request(socketPath string, req internal.ControlRequest) (internal.ControlResponse, error) {
	conn, err := net.DialTimeout("unix", socketPath, connectTimeout)
	if err != nil {
		return internal.ControlResponse{}, fmt.Errorf("cannot connect to gosn at %s: %w (is gosn running?)", socketPath, err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(requestTimeout))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return internal.ControlResponse{}, fmt.Errorf("unable to send request to gosn: %w", err)
	}

	var resp internal.ControlResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return internal.ControlResponse{}, fmt.Errorf("unable to read response from gosn: %w", err)
	}
	return resp, nil
}

// printStatus renders the status either as JSON or as aligned human output.
func printStatus(resp internal.ControlResponse, jsonOutput bool) {
	if resp.Status == nil {
		fmt.Fprintf(os.Stderr, "%s: gosn returned no status\n", programName)
		os.Exit(exitError)
	}

	if jsonOutput {
		out, err := json.MarshalIndent(resp.Status, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: unable to format status: %v\n", programName, err)
			os.Exit(exitError)
		}
		fmt.Println(string(out))
		return
	}

	fmt.Print(formatStatus(*resp.Status))
}

// formatStatus renders a status snapshot for humans.
func formatStatus(st internal.Status) string {
	var b strings.Builder
	line := func(label, value string) {
		fmt.Fprintf(&b, "%-14s %s\n", label+":", value)
	}

	// State and pause information.
	state := st.State
	if st.State == internal.ControlStatePaused {
		state = "paused"
		var parts []string
		if st.PausedSince != nil {
			parts = append(parts, "since "+formatTime(*st.PausedSince))
		}
		if st.PauseDeadline != nil {
			parts = append(parts,
				"until "+formatTime(*st.PauseDeadline),
				"in "+formatDuration(time.Until(*st.PauseDeadline)))
		} else {
			parts = append(parts, "indefinite")
		}
		state += " (" + strings.Join(parts, ", ") + ")"
	}
	line("state", state)

	// Command currently running or idle.
	if st.CommandRunning && st.CommandStarted != nil {
		line("run", "running since "+formatTime(*st.CommandStarted)+
			" ("+formatDuration(time.Since(*st.CommandStarted))+")")
	} else {
		line("run", "idle")
	}

	// Last completed sync.
	if st.LastSync != nil {
		outcome := "ok"
		if !st.LastSync.OK {
			outcome = "failed"
		}
		line("last sync", fmt.Sprintf("%s (%s, %s, origin %s)",
			formatTime(st.LastSync.FinishedAt), outcome, st.LastSync.Duration, st.LastSync.Origin))
	} else {
		line("last sync", "never")
	}

	// Last observed event, including events skipped while paused.
	if st.LastEvent != nil {
		line("last event", describeEvent(*st.LastEvent))
	} else {
		line("last event", "never")
	}

	// Next timer trigger.
	if st.NextTimer != nil {
		remaining := time.Until(*st.NextTimer)
		if remaining < 0 {
			line("next timer", formatTime(*st.NextTimer)+" (due)")
		} else {
			line("next timer", formatTime(*st.NextTimer)+" (in "+formatDuration(remaining)+")")
		}
	} else {
		line("next timer", "timer disabled")
	}

	line("started", formatTime(st.StartedAt)+
		" (uptime "+formatDuration(time.Since(st.StartedAt))+")")

	// Effective configuration.
	if st.Config.WatcherPath != "" {
		line("watcher", st.Config.WatcherPath)
	} else {
		line("watcher", "disabled")
	}
	if st.Config.TimerInterval != "" {
		line("timer", st.Config.TimerInterval)
	} else {
		line("timer", "disabled")
	}
	if st.Config.DebounceInterval != "" {
		line("debounce", st.Config.DebounceInterval)
	}
	line("command", strings.Join(st.Config.Command, " "))

	return b.String()
}

func describeEvent(ev internal.EventInfo) string {
	var what string
	if ev.Type != internal.SyncEventNone.String() && ev.Type != "" {
		what = fmt.Sprintf("%s via %s", ev.Type, ev.Origin)
	} else {
		what = ev.Origin + " event"
	}

	switch {
	case ev.Files > 1:
		what += fmt.Sprintf(", %d files", ev.Files)
	case ev.Filename != "":
		what += ", " + ev.Filename
	}

	return formatTime(ev.Time) + " (" + what + ")"
}

func formatTime(t time.Time) string {
	return t.Local().Format(localTimeFormat)
}

// formatDuration rounds for readability ("2h5m0s" stays, sub-second becomes
// "0s"), keeping the output compact.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	if d < time.Second {
		return "0s"
	}
	return d.Round(time.Second).String()
}
