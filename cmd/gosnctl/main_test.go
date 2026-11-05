package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bosim/gosyncnotifier/internal"
)

func TestBuildRequest(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		args    []string
		wantCmd string
		wantDur string
		wantErr string
	}{
		{name: "status", cmd: "status", wantCmd: internal.ControlCmdStatus},
		{name: "pause without duration", cmd: "pause", wantCmd: internal.ControlCmdPause},
		{name: "pause with duration", cmd: "pause", args: []string{"30m"}, wantCmd: internal.ControlCmdPause, wantDur: "30m"},
		{name: "pause with compound duration", cmd: "pause", args: []string{"1h30m"}, wantCmd: internal.ControlCmdPause, wantDur: "1h30m"},
		{name: "resume", cmd: "resume", wantCmd: internal.ControlCmdResume},
		{name: "trigger", cmd: "trigger", wantCmd: internal.ControlCmdTrigger},

		{name: "unknown command", cmd: "explode", wantErr: "unknown command"},
		{name: "status with arguments", cmd: "status", args: []string{"extra"}, wantErr: "takes no arguments"},
		{name: "resume with arguments", cmd: "resume", args: []string{"now"}, wantErr: "takes no arguments"},
		{name: "trigger with arguments", cmd: "trigger", args: []string{"now"}, wantErr: "takes no arguments"},
		{name: "pause with two durations", cmd: "pause", args: []string{"1m", "2m"}, wantErr: "at most one"},
		{name: "pause with invalid duration", cmd: "pause", args: []string{"soon"}, wantErr: "invalid pause duration"},
		{name: "pause with negative duration", cmd: "pause", args: []string{"-5m"}, wantErr: "must not be negative"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := buildRequest(tt.cmd, tt.args)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("err = nil, want an error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("err = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("err = %v, want no error", err)
			}
			if req.Cmd != tt.wantCmd {
				t.Errorf("Cmd = %q, want %q", req.Cmd, tt.wantCmd)
			}
			if req.Duration != tt.wantDur {
				t.Errorf("Duration = %q, want %q", req.Duration, tt.wantDur)
			}
		})
	}
}

// statusLine returns the trimmed value of the "label: value" line in out.
func statusLine(t *testing.T, out, label string) string {
	t.Helper()

	prefix := label + ":"
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}

	t.Fatalf("no %q line in:\n%s", label, out)
	return ""
}

func assertContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("value %q, want it to contain %q", got, want)
		}
	}
}

func TestFormatStatusRunning(t *testing.T) {
	nextTimer := time.Now().Add(5 * time.Minute)

	out := formatStatus(internal.Status{
		State:     internal.ControlStateRunning,
		StartedAt: time.Now().Add(-90 * time.Minute),
		LastSync: &internal.SyncInfo{
			FinishedAt: time.Now().Add(-5 * time.Minute),
			Duration:   "1.8s",
			OK:         true,
			Origin:     "watcher",
		},
		NextTimer: &nextTimer,
		Config: internal.StatusConfig{
			WatcherPath:      "/data",
			TimerInterval:    "10m",
			DebounceInterval: "1s",
			Command:          []string{"rsync", "-a", "/data", "host:dir"},
		},
	})

	if got := statusLine(t, out, "state"); got != "running" {
		t.Errorf("state = %q, want %q", got, "running")
	}
	if got := statusLine(t, out, "run"); got != "idle" {
		t.Errorf("run = %q, want %q", got, "idle")
	}
	assertContains(t, statusLine(t, out, "last sync"), "ok, 1.8s, origin watcher")
	if got := statusLine(t, out, "last event"); got != "never" {
		t.Errorf("last event = %q, want %q", got, "never")
	}
	assertContains(t, statusLine(t, out, "next timer"), formatTime(nextTimer), "in ")
	assertContains(t, statusLine(t, out, "started"), "uptime 1h30m")
	if got := statusLine(t, out, "watcher"); got != "/data" {
		t.Errorf("watcher = %q, want %q", got, "/data")
	}
	if got := statusLine(t, out, "timer"); got != "10m" {
		t.Errorf("timer = %q, want %q", got, "10m")
	}
	if got := statusLine(t, out, "debounce"); got != "1s" {
		t.Errorf("debounce = %q, want %q", got, "1s")
	}
	if got := statusLine(t, out, "command"); got != "rsync -a /data host:dir" {
		t.Errorf("command = %q, want the joined command", got)
	}
}

func TestFormatStatusPaused(t *testing.T) {
	out := formatStatus(internal.Status{
		State:       internal.ControlStatePaused,
		PausedSince: timePtr(time.Now().Add(-10 * time.Minute)),
		Config:      internal.StatusConfig{Command: []string{"true"}},
	})

	assertContains(t, statusLine(t, out, "state"), "paused (since ", "indefinite")
	if got := statusLine(t, out, "timer"); got != "disabled" {
		t.Errorf("timer = %q, want %q", got, "disabled")
	}
	if got := statusLine(t, out, "watcher"); got != "disabled" {
		t.Errorf("watcher = %q, want %q", got, "disabled")
	}
	if got := statusLine(t, out, "last sync"); got != "never" {
		t.Errorf("last sync = %q, want %q", got, "never")
	}
}

func TestFormatStatusRunningCommandAndFailure(t *testing.T) {
	out := formatStatus(internal.Status{
		State:          internal.ControlStatePaused,
		PauseDeadline:  timePtr(time.Now().Add(20 * time.Minute)),
		CommandRunning: true,
		CommandStarted: timePtr(time.Now().Add(-2 * time.Minute)),
		LastSync: &internal.SyncInfo{
			FinishedAt: time.Now().Add(-time.Hour),
			Duration:   "3.2s",
			OK:         false,
			Origin:     "timer",
		},
		Config: internal.StatusConfig{Command: []string{"false"}},
	})

	assertContains(t, statusLine(t, out, "state"), "paused", "until ")
	assertContains(t, statusLine(t, out, "run"), "running since ")
	assertContains(t, statusLine(t, out, "last sync"), "failed, 3.2s, origin timer")
}

func TestDescribeEvent(t *testing.T) {
	tests := []struct {
		name string
		ev   internal.EventInfo
		want []string
	}{
		{
			name: "file modification",
			ev: internal.EventInfo{
				Time:     time.Now(),
				Origin:   "watcher",
				Type:     "modified",
				Filename: "/data/a.txt",
			},
			want: []string{"modified via watcher", "/data/a.txt"},
		},
		{
			name: "debounced burst",
			ev: internal.EventInfo{
				Time:   time.Now(),
				Origin: "watcher",
				Type:   "created",
				Files:  3,
			},
			want: []string{"created via watcher", "3 files"},
		},
		{
			name: "timer event without a file",
			ev: internal.EventInfo{
				Time:   time.Now(),
				Origin: "timer",
				Type:   "none",
			},
			want: []string{"timer event"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := describeEvent(tt.ev)
			for _, want := range tt.want {
				if !strings.Contains(out, want) {
					t.Errorf("describeEvent() = %q, want it to contain %q", out, want)
				}
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{500 * time.Millisecond, "0s"},
		{-90 * time.Second, "1m30s"},
		{time.Hour + 30*time.Minute, "1h30m0s"},
	}
	for _, tt := range tests {
		if got := formatDuration(tt.d); got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}
