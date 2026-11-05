package internal

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const controlTestTimeout = 2 * time.Second

// controlTestServer bundles a running ControlServer with its test handles.
type controlTestServer struct {
	server   *ControlServer
	path     string
	syncChan chan SyncEvent
	cancel   context.CancelFunc
}

// startControlServer runs a ControlServer on a socket in a temporary
// directory and shuts it down during cleanup, asserting a clean stop.
func startControlServer(t *testing.T, cfg StatusConfig, timer *Timer) *controlTestServer {
	t.Helper()

	path := filepath.Join(t.TempDir(), "gosn.sock")
	syncChan := make(chan SyncEvent, 4)
	server := NewControlServer(path, syncChan, cfg, timer)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	waitSocketReady(t, path)

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned an error on shutdown: %v", err)
			}
		case <-time.After(controlTestTimeout):
			t.Error("Run did not stop during cleanup")
		}
	})

	return &controlTestServer{
		server:   server,
		path:     path,
		syncChan: syncChan,
		cancel:   cancel,
	}
}

// controlCall performs one request/response exchange over the socket.
func controlCall(t *testing.T, path string, req ControlRequest) ControlResponse {
	t.Helper()

	conn, err := net.DialTimeout("unix", path, controlTestTimeout)
	if err != nil {
		t.Fatalf("unable to connect to control socket %s: %v", path, err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(controlTestTimeout))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("unable to send request: %v", err)
	}

	var resp ControlResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("unable to read response: %v", err)
	}
	return resp
}

// waitSocketReady polls until the server accepts connections.
func waitSocketReady(t *testing.T, path string) {
	t.Helper()

	deadline := time.Now().Add(controlTestTimeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", path, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("control socket %s did not become ready", path)
}

func receiveSyncEvent(t *testing.T, ch chan SyncEvent) SyncEvent {
	t.Helper()

	select {
	case ev := <-ch:
		return ev
	case <-time.After(controlTestTimeout):
		t.Fatal("timed out waiting for sync event")
		return SyncEvent{}
	}
}

func expectNoSyncEvent(t *testing.T, ch chan SyncEvent) {
	t.Helper()

	select {
	case ev := <-ch:
		t.Errorf("unexpected sync event: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

// waitControlState polls the status until it reports the wanted state.
func waitControlState(t *testing.T, path, want string) {
	t.Helper()

	deadline := time.Now().Add(controlTestTimeout)
	for time.Now().Before(deadline) {
		resp := controlCall(t, path, ControlRequest{Cmd: ControlCmdStatus})
		if resp.OK && resp.Status != nil && resp.Status.State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("control status never reached state %q", want)
}

func testStatusConfig() StatusConfig {
	return StatusConfig{
		WatcherPath:      "/tmp/watched",
		TimerInterval:    "10m",
		DebounceInterval: "1s",
		Command:          []string{"rsync", "-avz", "/tmp/watched", "host:dir"},
	}
}

// TestControlStatusRoundtrip verifies the status exchange: configuration is
// reported verbatim, history fields start empty and the next timer run is
// present when a timer is configured.
func TestControlStatusRoundtrip(t *testing.T) {
	before := time.Now()
	timer := NewTimer(make(chan SyncEvent, 1), time.Minute)
	ts := startControlServer(t, testStatusConfig(), timer)

	resp := controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdStatus})

	if !resp.OK {
		t.Fatalf("status failed: %s", resp.Error)
	}
	if resp.Status == nil {
		t.Fatal("status response is missing the status payload")
	}

	st := *resp.Status
	if st.State != ControlStateRunning {
		t.Errorf("State = %q, want %q", st.State, ControlStateRunning)
	}
	if !reflect.DeepEqual(st.Config, testStatusConfig()) {
		t.Errorf("Config = %+v, want %+v", st.Config, testStatusConfig())
	}
	if st.NextTimer == nil {
		t.Error("NextTimer = nil, want the scheduled next run")
	}
	if st.LastSync != nil {
		t.Errorf("LastSync = %+v, want nil before any run", st.LastSync)
	}
	if st.LastEvent != nil {
		t.Errorf("LastEvent = %+v, want nil before any event", st.LastEvent)
	}
	if st.StartedAt.Before(before) || st.StartedAt.After(time.Now()) {
		t.Errorf("StartedAt = %v, want a timestamp within the test", st.StartedAt)
	}
}

// TestControlStatusWithoutTimer verifies the next run is omitted when gosn
// runs without a timer.
func TestControlStatusWithoutTimer(t *testing.T) {
	ts := startControlServer(t, testStatusConfig(), nil)

	resp := controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdStatus})
	if !resp.OK {
		t.Fatalf("status failed: %s", resp.Error)
	}
	if resp.Status.NextTimer != nil {
		t.Errorf("NextTimer = %v, want nil without a timer", resp.Status.NextTimer)
	}
}

// TestControlSocketPermissions verifies the socket is only accessible to the
// owner.
func TestControlSocketPermissions(t *testing.T) {
	ts := startControlServer(t, testStatusConfig(), nil)

	fi, err := os.Stat(ts.path)
	if err != nil {
		t.Fatalf("unable to stat control socket: %v", err)
	}
	if got := fi.Mode().Perm(); got != controlSocketPerms {
		t.Errorf("socket permissions = %o, want %o", got, controlSocketPerms)
	}
}

// TestControlPauseResume covers the pause lifecycle: indefinite and timed
// pauses, re-pausing, and the catch-up event a resume queues.
func TestControlPauseResume(t *testing.T) {
	ts := startControlServer(t, testStatusConfig(), nil)

	// Pause indefinitely.
	resp := controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdPause})
	if !resp.OK {
		t.Fatalf("pause failed: %s", resp.Error)
	}
	if resp.Status.State != ControlStatePaused {
		t.Errorf("State = %q, want %q", resp.Status.State, ControlStatePaused)
	}
	if resp.Status.PauseDeadline != nil {
		t.Errorf("PauseDeadline = %v, want nil for an indefinite pause", resp.Status.PauseDeadline)
	}
	if !ts.server.IsPaused() {
		t.Error("IsPaused = false, want true after pause")
	}
	if resp.Message != "sync paused indefinitely" {
		t.Errorf("Message = %q, want an indefinite pause confirmation", resp.Message)
	}

	if resp.Status.PausedSince == nil {
		t.Fatal("PausedSince = nil, want the pause start time")
	}
	pausedSince := *resp.Status.PausedSince

	// Timed pause replaces the deadline.
	resp = controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdPause, Duration: "1h"})
	if !resp.OK {
		t.Fatalf("timed pause failed: %s", resp.Error)
	}
	if resp.Status.PauseDeadline == nil {
		t.Fatal("PauseDeadline = nil, want a deadline for the timed pause")
	}
	if remaining := time.Until(*resp.Status.PauseDeadline); remaining < 59*time.Minute {
		t.Errorf("PauseDeadline remaining = %v, want close to 1h", remaining)
	}
	if !resp.Status.PausedSince.Equal(pausedSince) {
		t.Errorf("PausedSince = %v, want the original %v (re-pausing keeps it)", resp.Status.PausedSince, pausedSince)
	}

	// Re-pausing without a duration clears the deadline again.
	resp = controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdPause})
	if !resp.OK {
		t.Fatalf("re-pause failed: %s", resp.Error)
	}
	if resp.Status.PauseDeadline != nil {
		t.Errorf("PauseDeadline = %v, want nil after clearing the deadline", resp.Status.PauseDeadline)
	}

	// Events are skipped while paused but still recorded.
	ts.server.RecordEvent(SyncEvent{Filename: "a.txt", Type: SyncEventModified, Origin: OriginWatcher})
	resp = controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdStatus})
	if resp.Status.LastEvent == nil || resp.Status.LastEvent.Filename != "a.txt" {
		t.Errorf("LastEvent = %+v, want the event recorded while paused", resp.Status.LastEvent)
	}
	expectNoSyncEvent(t, ts.syncChan)

	// Resume queues the catch-up run with OriginInit.
	resp = controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdResume})
	if !resp.OK {
		t.Fatalf("resume failed: %s", resp.Error)
	}
	if resp.Status.State != ControlStateRunning {
		t.Errorf("State = %q, want %q after resume", resp.Status.State, ControlStateRunning)
	}
	if resp.Message != "sync resumed, catch-up run queued" {
		t.Errorf("Message = %q, want a catch-up confirmation", resp.Message)
	}
	if ts.server.IsPaused() {
		t.Error("IsPaused = true, want false after resume")
	}
	if ev := receiveSyncEvent(t, ts.syncChan); ev.Origin != OriginInit {
		t.Errorf("catch-up event origin = %v, want %v", ev.Origin, OriginInit)
	}

	// Resuming while running is idempotent and queues nothing.
	resp = controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdResume})
	if !resp.OK {
		t.Fatalf("second resume failed: %s", resp.Error)
	}
	if resp.Message != "sync already running" {
		t.Errorf("Message = %q, want an already-running confirmation", resp.Message)
	}
	expectNoSyncEvent(t, ts.syncChan)
}

// TestControlPauseInvalidRequests covers rejected commands.
func TestControlPauseInvalidRequests(t *testing.T) {
	ts := startControlServer(t, testStatusConfig(), nil)

	tests := []struct {
		name    string
		req     ControlRequest
		wantErr string
	}{
		{
			name:    "unparsable duration",
			req:     ControlRequest{Cmd: ControlCmdPause, Duration: "soon"},
			wantErr: "invalid pause duration",
		},
		{
			name:    "negative duration",
			req:     ControlRequest{Cmd: ControlCmdPause, Duration: "-5m"},
			wantErr: "must not be negative",
		},
		{
			name:    "unknown command",
			req:     ControlRequest{Cmd: "selfdestruct"},
			wantErr: "unknown command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := controlCall(t, ts.path, tt.req)
			if resp.OK {
				t.Fatalf("resp.OK = true, want rejection of %+v", tt.req)
			}
			if !strings.Contains(resp.Error, tt.wantErr) {
				t.Errorf("Error = %q, want it to contain %q", resp.Error, tt.wantErr)
			}
		})
	}

	// A request that is not JSON at all is rejected with an error response.
	t.Run("malformed request", func(t *testing.T) {
		conn, err := net.DialTimeout("unix", ts.path, controlTestTimeout)
		if err != nil {
			t.Fatalf("unable to connect: %v", err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(controlTestTimeout))

		if _, err := conn.Write([]byte("this is not json\n")); err != nil {
			t.Fatalf("unable to write: %v", err)
		}

		var resp ControlResponse
		if err := json.NewDecoder(conn).Decode(&resp); err != nil {
			t.Fatalf("unable to read response: %v", err)
		}
		if resp.OK || !strings.Contains(resp.Error, "malformed request") {
			t.Errorf("resp = %+v, want a malformed request rejection", resp)
		}
	})
}

// TestControlPauseAutoResume verifies a timed pause resumes by itself and
// queues the catch-up run.
func TestControlPauseAutoResume(t *testing.T) {
	ts := startControlServer(t, testStatusConfig(), nil)

	resp := controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdPause, Duration: "50ms"})
	if !resp.OK {
		t.Fatalf("timed pause failed: %s", resp.Error)
	}
	if resp.Status.PauseDeadline == nil {
		t.Fatal("PauseDeadline = nil, want the auto-resume deadline")
	}

	if ev := receiveSyncEvent(t, ts.syncChan); ev.Origin != OriginInit {
		t.Errorf("auto-resume event origin = %v, want %v", ev.Origin, OriginInit)
	}
	waitControlState(t, ts.path, ControlStateRunning)
}

// TestControlRepauseInvalidatesAutoResume verifies that pausing again while
// a timed pause is armed replaces the deadline: the original timer must not
// resume the sync early.
func TestControlRepauseInvalidatesAutoResume(t *testing.T) {
	ts := startControlServer(t, testStatusConfig(), nil)

	if resp := controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdPause, Duration: "300ms"}); !resp.OK {
		t.Fatalf("timed pause failed: %s", resp.Error)
	}
	resp := controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdPause, Duration: "1h"})
	if !resp.OK {
		t.Fatalf("re-pause failed: %s", resp.Error)
	}

	// Wait past the original deadline; the stale timer must not fire.
	time.Sleep(500 * time.Millisecond)
	if !ts.server.IsPaused() {
		t.Error("IsPaused = false, want the re-pause to still be active")
	}

	resp = controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdStatus})
	if resp.Status.PauseDeadline == nil {
		t.Fatal("PauseDeadline = nil, want the replacement deadline")
	}
	if remaining := time.Until(*resp.Status.PauseDeadline); remaining < 59*time.Minute {
		t.Errorf("PauseDeadline remaining = %v, want close to 1h", remaining)
	}

	// Clean up: a resume queues the catch-up event the helper does not consume.
	if resp := controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdResume}); !resp.OK {
		t.Fatalf("resume failed: %s", resp.Error)
	}
	receiveSyncEvent(t, ts.syncChan)
}

// TestControlTrigger verifies explicit runs and their rejection while paused.
func TestControlTrigger(t *testing.T) {
	ts := startControlServer(t, testStatusConfig(), nil)

	resp := controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdTrigger})
	if !resp.OK {
		t.Fatalf("trigger failed: %s", resp.Error)
	}
	if resp.Message != "sync run queued" {
		t.Errorf("Message = %q, want a queue confirmation", resp.Message)
	}
	if ev := receiveSyncEvent(t, ts.syncChan); ev.Origin != OriginControl {
		t.Errorf("trigger event origin = %v, want %v", ev.Origin, OriginControl)
	}

	// Triggering while paused is rejected so it cannot bypass the pause.
	if resp := controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdPause}); !resp.OK {
		t.Fatalf("pause failed: %s", resp.Error)
	}
	resp = controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdTrigger})
	if resp.OK {
		t.Error("resp.OK = true, want trigger rejected while paused")
	}
	if !strings.Contains(resp.Error, "sync is paused") {
		t.Errorf("Error = %q, want a paused rejection", resp.Error)
	}
}

// TestControlTriggerDoesNotBlockWhenQueueFull verifies control responses stay
// fast even when no one consumes the sync channel, e.g. while a long command
// runs and events piled up.
func TestControlTriggerDoesNotBlockWhenQueueFull(t *testing.T) {
	ts := startControlServer(t, testStatusConfig(), nil)

	for range cap(ts.syncChan) {
		ts.syncChan <- SyncEvent{}
	}

	start := time.Now()
	resp := controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdTrigger})
	if !resp.OK {
		t.Fatalf("trigger failed: %s", resp.Error)
	}
	if elapsed := time.Since(start); elapsed > controlTestTimeout/2 {
		t.Errorf("trigger took %v, want a prompt response", elapsed)
	}
}

// TestControlRecordsRunsAndEvents verifies the state the event loop records.
func TestControlRecordsRunsAndEvents(t *testing.T) {
	ts := startControlServer(t, testStatusConfig(), nil)

	ts.server.RecordEvent(SyncEvent{
		Filename: "a.txt",
		Type:     SyncEventModified,
		Origin:   OriginWatcher,
		Events: []FileEvent{
			{Filename: "a.txt", Type: SyncEventModified},
			{Filename: "b.txt", Type: SyncEventCreated},
		},
	})
	ts.server.BeginRun(OriginTimer)

	resp := controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdStatus})
	if !resp.OK {
		t.Fatalf("status failed: %s", resp.Error)
	}
	st := *resp.Status
	if !st.CommandRunning {
		t.Error("CommandRunning = false, want true between BeginRun and EndRun")
	}
	if st.CommandStarted == nil {
		t.Error("CommandStarted = nil, want the run start time")
	}
	if st.LastEvent == nil || st.LastEvent.Files != 2 || st.LastEvent.Type != "modified" {
		t.Errorf("LastEvent = %+v, want the recorded burst with 2 files", st.LastEvent)
	}
	if st.LastSync != nil {
		t.Errorf("LastSync = %+v, want nil before EndRun", st.LastSync)
	}

	ts.server.EndRun(true)
	resp = controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdStatus})
	st = *resp.Status
	if st.CommandRunning {
		t.Error("CommandRunning = true, want false after EndRun")
	}
	if st.LastSync == nil {
		t.Fatal("LastSync = nil, want the recorded run")
	}
	if !st.LastSync.OK || st.LastSync.Origin != "timer" || st.LastSync.Duration == "" {
		t.Errorf("LastSync = %+v, want a successful timer run with duration", st.LastSync)
	}

	// Failed runs are recorded as such.
	ts.server.BeginRun(OriginWatcher)
	ts.server.EndRun(false)
	resp = controlCall(t, ts.path, ControlRequest{Cmd: ControlCmdStatus})
	if resp.Status.LastSync.OK {
		t.Errorf("LastSync.OK = true, want false after a failed run")
	}
}

// TestControlSocketReplacesStaleFile verifies a socket file left behind by a
// killed instance (or an unrelated leftover file) does not prevent startup.
func TestControlSocketReplacesStaleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gosn.sock")
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatalf("unable to create stale file: %v", err)
	}

	server := NewControlServer(path, make(chan SyncEvent, 1), testStatusConfig(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	waitSocketReady(t, path)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run failed to take over the stale file: %v", err)
	}
}

// TestControlSocketInUse verifies a live instance's socket is not stolen: the
// second server reports ErrControlSocketInUse and the first keeps serving.
func TestControlSocketInUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gosn.sock")

	first := NewControlServer(path, make(chan SyncEvent, 1), testStatusConfig(), nil)
	ctx1, cancel1 := context.WithCancel(t.Context())
	defer cancel1()
	done1 := make(chan error, 1)
	go func() { done1 <- first.Run(ctx1) }()
	waitSocketReady(t, path)

	second := NewControlServer(path, make(chan SyncEvent, 1), testStatusConfig(), nil)
	done2 := make(chan error, 1)
	go func() { done2 <- second.Run(context.Background()) }()

	select {
	case err := <-done2:
		if !errors.Is(err, ErrControlSocketInUse) {
			t.Errorf("second Run error = %v, want %v", err, ErrControlSocketInUse)
		}
	case <-time.After(controlTestTimeout):
		t.Fatal("second Run did not report the socket as in use")
	}

	// The first instance still owns and serves the socket.
	resp := controlCall(t, path, ControlRequest{Cmd: ControlCmdStatus})
	if !resp.OK {
		t.Errorf("first instance no longer serves the socket: %s", resp.Error)
	}

	cancel1()
	if err := <-done1; err != nil {
		t.Errorf("first Run returned an error: %v", err)
	}
}

// TestControlSocketRemovedOnShutdown verifies the socket file is cleaned up.
func TestControlSocketRemovedOnShutdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gosn.sock")

	server := NewControlServer(path, make(chan SyncEvent, 1), testStatusConfig(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	waitSocketReady(t, path)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("control socket still exists after shutdown (stat error: %v)", err)
	}
}

// TestControlCommandsRejectedAfterShutdown verifies a resume cannot enqueue
// once the context is cancelled.
func TestControlCommandsRejectedAfterShutdown(t *testing.T) {
	ts := startControlServer(t, testStatusConfig(), nil)

	ts.server.pause(0)

	// cancel() makes ctx.Err() visible immediately; Run's own shutdown is
	// still asserted by the helper's cleanup.
	ts.cancel()

	if _, err := ts.server.resume(); err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Errorf("resume error = %v, want a shutting down rejection", err)
	}
}

// TestControlConcurrentClients verifies parallel status requests are served.
func TestControlConcurrentClients(t *testing.T) {
	ts := startControlServer(t, testStatusConfig(), nil)

	const clients = 10
	errs := make(chan error, clients)
	for range clients {
		go func() {
			conn, err := net.DialTimeout("unix", ts.path, controlTestTimeout)
			if err != nil {
				errs <- err
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(controlTestTimeout))

			if err := json.NewEncoder(conn).Encode(ControlRequest{Cmd: ControlCmdStatus}); err != nil {
				errs <- err
				return
			}
			var resp ControlResponse
			if err := json.NewDecoder(conn).Decode(&resp); err != nil {
				errs <- err
				return
			}
			if !resp.OK {
				errs <- errors.New(resp.Error)
				return
			}
			errs <- nil
		}()
	}

	for range clients {
		select {
		case err := <-errs:
			if err != nil {
				t.Errorf("concurrent client failed: %v", err)
			}
		case <-time.After(controlTestTimeout):
			t.Fatal("concurrent clients did not all complete")
		}
	}
}

// TestDefaultSocketPath verifies the socket location rules: the XDG runtime
// directory wins, otherwise the platform runtime/temp locations are used.
func TestDefaultSocketPath(t *testing.T) {
	t.Run("xdg runtime dir", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "/run/user/1234")
		want := "/run/user/1234/gosn.sock"
		if got := DefaultSocketPath(); got != want {
			t.Errorf("DefaultSocketPath() = %q, want %q", got, want)
		}
	})

	t.Run("fallback", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "")
		got := DefaultSocketPath()
		base := filepath.Base(got)
		// Either <dir>/gosn.sock (runtime dir existed) or the temp
		// directory's per-user gosn-<uid>.sock fallback.
		if base != "gosn.sock" && !(strings.HasPrefix(base, "gosn-") && strings.HasSuffix(base, ".sock")) {
			t.Errorf("DefaultSocketPath() = %q, want a gosn.sock or gosn-<uid>.sock base name", got)
		}
		// Either the per-user runtime dir or the temp directory, never both.
		isRuntime := strings.HasPrefix(got, "/run/user/")
		isTemp := strings.HasPrefix(got, os.TempDir()+string(filepath.Separator))
		if !isRuntime && !isTemp {
			t.Errorf("DefaultSocketPath() = %q, want a /run/user or temp directory path", got)
		}
	})
}
