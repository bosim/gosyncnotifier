package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Commands understood by the gosn control socket.
const (
	ControlCmdStatus  = "status"
	ControlCmdPause   = "pause"
	ControlCmdResume  = "resume"
	ControlCmdTrigger = "trigger"
)

// States reported by Status.State.
const (
	ControlStateRunning = "running"
	ControlStatePaused  = "paused"
)

// ErrControlSocketInUse is returned by ControlServer.Run when another live
// gosn instance already owns the configured socket path.
var ErrControlSocketInUse = errors.New("control socket already in use by another gosn instance")

const (
	// controlSocketPerms restricts the socket to the current user.
	controlSocketPerms = 0o600
	// controlProbeTimeout bounds the liveness probe of a pre-existing socket
	// file before it is considered stale (or, on timeout, in use).
	controlProbeTimeout = 250 * time.Millisecond
	// controlIOTimeout bounds reading a request from and writing a response
	// to one control connection.
	controlIOTimeout = 5 * time.Second
)

// ControlRequest is the single JSON message a gosnctl connection sends.
type ControlRequest struct {
	Cmd string `json:"cmd"`
	// Duration is the optional pause length (a Go duration such as "30m").
	// It is only read for ControlCmdPause; empty means pause indefinitely.
	Duration string `json:"duration,omitempty"`
}

// ControlResponse is the single JSON message the control socket replies with.
type ControlResponse struct {
	OK      bool    `json:"ok"`
	Error   string  `json:"error,omitempty"`
	Message string  `json:"message,omitempty"` // human-readable confirmation of a command
	Status  *Status `json:"status,omitempty"`  // set on successful commands
}

// Status is the snapshot gosnctl reports for the "status" command.
type Status struct {
	State          string       `json:"state"` // ControlStateRunning or ControlStatePaused
	PausedSince    *time.Time   `json:"paused_since,omitempty"`
	PauseDeadline  *time.Time   `json:"pause_deadline,omitempty"` // omitted while paused indefinitely
	CommandRunning bool         `json:"command_running"`
	CommandStarted *time.Time   `json:"command_started_at,omitempty"`
	LastSync       *SyncInfo    `json:"last_sync,omitempty"`
	LastEvent      *EventInfo   `json:"last_event,omitempty"`
	NextTimer      *time.Time   `json:"next_timer,omitempty"` // omitted when the timer is disabled
	StartedAt      time.Time    `json:"started_at"`
	Config         StatusConfig `json:"config"`
}

// SyncInfo describes the most recent command run.
type SyncInfo struct {
	FinishedAt time.Time `json:"finished_at"`
	Duration   string    `json:"duration"`
	OK         bool      `json:"ok"`
	Origin     string    `json:"origin"`
}

// EventInfo describes the most recent event observed by the event loop,
// including events skipped while paused.
type EventInfo struct {
	Time     time.Time `json:"time"`
	Origin   string    `json:"origin"`
	Type     string    `json:"type"` // SyncEventType name, "none" for timer/init events
	Filename string    `json:"filename,omitempty"`
	Files    int       `json:"files,omitempty"` // distinct files of a debounced burst
}

// StatusConfig mirrors the effective configuration in the status output.
type StatusConfig struct {
	WatcherPath      string   `json:"watcher_path,omitempty"`
	TimerInterval    string   `json:"timer_interval,omitempty"` // omitted when the timer is disabled
	DebounceInterval string   `json:"debounce_interval,omitempty"`
	Command          []string `json:"command"`
}

// DefaultSocketPath returns the default path of the gosn control socket:
// $XDG_RUNTIME_DIR/gosn.sock, which on a systemd system is
// /run/user/<uid>/gosn.sock. When XDG_RUNTIME_DIR is unset, /run/user/<uid>
// is used if it exists, otherwise the socket falls back to the system temp
// directory (e.g. on macOS or minimal containers).
func DefaultSocketPath() string {
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, "gosn.sock")
	}

	runtimeDir := filepath.Join("/run/user", strconv.Itoa(os.Getuid()))
	if fi, err := os.Stat(runtimeDir); err == nil && fi.IsDir() {
		return filepath.Join(runtimeDir, "gosn.sock")
	}

	return filepath.Join(os.TempDir(), fmt.Sprintf("gosn-%d.sock", os.Getuid()))
}

// ControlServer exposes the gosn state over a Unix domain socket and applies
// the commands it receives (pause, resume, trigger). Run serves the socket in
// the background; the state mutators (RecordEvent, BeginRun, EndRun,
// IsPaused, Snapshot) are called from the event loop and are safe for
// concurrent use with the serving goroutines.
type ControlServer struct {
	path     string
	syncChan chan SyncEvent
	timer    *Timer
	cfg      StatusConfig

	mu             sync.Mutex
	ctx            context.Context
	paused         bool
	pausedSince    time.Time
	pauseDeadline  time.Time // zero while paused indefinitely
	pauseTimer     *time.Timer
	pauseGen       uint64 // invalidates stale auto-resume callbacks
	commandRunning bool
	commandStarted time.Time
	commandOrigin  string
	lastSync       *SyncInfo
	lastEvent      *EventInfo

	startedAt time.Time
}

// NewControlServer creates a control server bound to path. Events that
// trigger a run (resume, trigger) are enqueued on syncChan; timer may be nil
// when the timer is disabled, in which case status omits the next run time.
// cfg is reported verbatim in the status output.
func NewControlServer(path string, syncChan chan SyncEvent, cfg StatusConfig, timer *Timer) *ControlServer {
	return &ControlServer{
		path:      path,
		syncChan:  syncChan,
		timer:     timer,
		cfg:       cfg,
		startedAt: time.Now(),
	}
}

// Run serves the control socket until ctx is cancelled and returns nil on
// clean shutdown. If another live gosn instance already owns the path, it
// returns ErrControlSocketInUse without touching that socket; a stale socket
// file left behind by a killed instance is removed and reused.
func (s *ControlServer) Run(ctx context.Context) error {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()

	bound := false
	defer func() {
		s.mu.Lock()
		if s.pauseTimer != nil {
			s.pauseTimer.Stop()
			s.pauseTimer = nil
		}
		s.mu.Unlock()
		if bound {
			// Best effort: remove the socket we own so a restart can rebind.
			_ = os.Remove(s.path)
		}
	}()

	if err := s.claimSocket(); err != nil {
		return err
	}

	ln, err := net.Listen("unix", s.path)
	if err != nil {
		return fmt.Errorf("unable to listen on control socket %s: %w", s.path, err)
	}
	bound = true
	defer ln.Close()

	// net.Listen creates the socket with umask-dependent permissions; restrict
	// it to the current user explicitly (only the owner can control gosn).
	if err := os.Chmod(s.path, controlSocketPerms); err != nil {
		return fmt.Errorf("unable to restrict permissions of control socket %s: %w", s.path, err)
	}

	// Unblock Accept as soon as the context is cancelled.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	slog.Info("Control socket listening", "path", s.path)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				slog.Info("Control socket stopped", "path", s.path)
				return nil
			}
			slog.Warn("Control socket accept failed", "error", err)
			continue
		}
		go s.handleConn(conn)
	}
}

// claimSocket prepares the socket path for binding: it returns
// ErrControlSocketInUse when a live listener answers on the existing file and
// removes the file when nothing is listening (a stale socket or leftover
// file).
func (s *ControlServer) claimSocket() error {
	fi, err := os.Stat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("unable to inspect control socket path %s: %w", s.path, err)
	}

	if fi.IsDir() {
		return fmt.Errorf("control socket path %s is a directory", s.path)
	}

	conn, err := net.DialTimeout("unix", s.path, controlProbeTimeout)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("%w (%s)", ErrControlSocketInUse, s.path)
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		// Unresponsive but bound: assume it is in use rather than stealing it.
		return fmt.Errorf("%w (%s, liveness probe timed out)", ErrControlSocketInUse, s.path)
	}

	slog.Info("Removing stale control socket", "path", s.path)
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("unable to remove stale control socket %s: %w", s.path, err)
	}
	return nil
}

// handleConn reads one request, dispatches it and writes one response.
func (s *ControlServer) handleConn(conn net.Conn) {
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(controlIOTimeout))

	var req ControlRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		slog.Debug("Control request rejected", "error", err)
		respond(conn, ControlResponse{OK: false, Error: "malformed request: " + err.Error()})
		return
	}

	respond(conn, s.handleRequest(req))
}

func respond(conn net.Conn, resp ControlResponse) {
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		slog.Debug("Unable to send control response", "error", err)
	}
}

// handleRequest dispatches one control command.
func (s *ControlServer) handleRequest(req ControlRequest) ControlResponse {
	switch req.Cmd {
	case ControlCmdStatus:
		return controlSuccess(s.Snapshot())

	case ControlCmdPause:
		var d time.Duration
		if req.Duration != "" {
			parsed, err := time.ParseDuration(req.Duration)
			if err != nil {
				return controlFailure(fmt.Errorf("invalid pause duration %q: %w", req.Duration, err))
			}
			if parsed < 0 {
				return controlFailure(fmt.Errorf("pause duration %q must not be negative", req.Duration))
			}
			d = parsed
		}
		s.pause(d)
		slog.Info("Sync paused via control socket", "duration", pauseDescription(d))
		st := s.Snapshot()
		return ControlResponse{OK: true, Status: &st, Message: pauseMessage(st)}

	case ControlCmdResume:
		resumed, err := s.resume()
		if err != nil {
			return controlFailure(err)
		}
		if resumed {
			slog.Info("Sync resumed via control socket")
		}
		message := "sync already running"
		if resumed {
			message = "sync resumed, catch-up run queued"
		}
		st := s.Snapshot()
		return ControlResponse{OK: true, Status: &st, Message: message}

	case ControlCmdTrigger:
		if err := s.trigger(); err != nil {
			return controlFailure(err)
		}
		slog.Info("Command run requested via control socket")
		st := s.Snapshot()
		return ControlResponse{OK: true, Status: &st, Message: "sync run queued"}

	default:
		return controlFailure(fmt.Errorf("unknown command %q (want %s, %s, %s or %s)",
			req.Cmd, ControlCmdStatus, ControlCmdPause, ControlCmdResume, ControlCmdTrigger))
	}
}

func controlSuccess(st Status) ControlResponse {
	return ControlResponse{OK: true, Status: &st}
}

func controlFailure(err error) ControlResponse {
	return ControlResponse{OK: false, Error: err.Error()}
}

func pauseDescription(d time.Duration) string {
	if d == 0 {
		return "indefinite"
	}
	return d.String()
}

// pauseMessage renders the confirmation for a pause command from the status
// snapshot taken right after pausing.
func pauseMessage(st Status) string {
	if st.PauseDeadline == nil {
		return "sync paused indefinitely"
	}
	remaining := time.Until(*st.PauseDeadline).Round(time.Second)
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("sync paused until %s (in %s)", st.PauseDeadline.Local().Format("2006-01-02 15:04:05"), remaining)
}

// pause suspends command runs. A non-positive duration pauses indefinitely
// (clearing an armed auto-resume); a positive duration (re)arms the auto
// resume. Pausing while already paused replaces the previous deadline.
func (s *ControlServer) pause(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.paused {
		s.paused = true
		s.pausedSince = time.Now()
	}

	// Invalidate a possibly already firing auto-resume callback and re-arm.
	s.pauseGen++
	s.stopPauseTimerLocked()
	s.pauseDeadline = time.Time{}
	if d > 0 {
		s.pauseDeadline = time.Now().Add(d)
		gen := s.pauseGen
		s.pauseTimer = time.AfterFunc(d, func() { s.autoResume(gen) })
	}
}

// resume ends the current pause. On a paused -> running transition a catch-up
// event with OriginInit is queued, mirroring the run gosn performs at
// startup; resuming while not paused does nothing. The bool reports whether
// a transition happened.
func (s *ControlServer) resume() (bool, error) {
	s.mu.Lock()
	if !s.paused {
		s.mu.Unlock()
		return false, nil
	}
	s.paused = false
	s.pausedSince = time.Time{}
	s.pauseDeadline = time.Time{}
	s.pauseGen++
	s.stopPauseTimerLocked()
	s.mu.Unlock()

	if err := s.enqueue(SyncEvent{Origin: OriginInit}); err != nil {
		return true, err
	}
	slog.Debug("Queued catch-up run after resume")
	return true, nil
}

// autoResume is the timed pause callback; gen invalidates callbacks of
// superseded pauses.
func (s *ControlServer) autoResume(gen uint64) {
	s.mu.Lock()
	if !s.paused || s.pauseGen != gen {
		s.mu.Unlock()
		return
	}
	s.paused = false
	s.pausedSince = time.Time{}
	s.pauseDeadline = time.Time{}
	s.pauseTimer = nil
	s.mu.Unlock()

	slog.Info("Pause expired via control socket")

	if err := s.enqueue(SyncEvent{Origin: OriginInit}); err != nil {
		slog.Warn("Unable to queue catch-up run after pause expired", "error", err)
	}
}

// trigger queues an explicit run (gosnctl trigger). It is rejected while
// paused so an explicit run cannot bypass a deliberate pause.
func (s *ControlServer) trigger() error {
	s.mu.Lock()
	paused := s.paused
	s.mu.Unlock()

	if paused {
		return errors.New("sync is paused, run resume first")
	}

	return s.enqueue(SyncEvent{Origin: OriginControl})
}

// enqueue hands an event to the event loop without blocking: if the channel
// is full a run is already pending and picks the change up anyway. This keeps
// control responses fast even while a long command is running.
func (s *ControlServer) enqueue(ev SyncEvent) error {
	s.mu.Lock()
	ctx := s.ctx
	s.mu.Unlock()

	if ctx == nil {
		return errors.New("control server is not running")
	}
	if ctx.Err() != nil {
		return fmt.Errorf("gosn is shutting down: %w", ctx.Err())
	}

	select {
	case s.syncChan <- ev:
		return nil
	default:
		slog.Debug("Control event not queued, a run is already pending", "origin", ev.Origin.String())
		return nil
	}
}

// stopPauseTimerLocked disarms the auto-resume timer; s.mu must be held.
func (s *ControlServer) stopPauseTimerLocked() {
	if s.pauseTimer != nil {
		s.pauseTimer.Stop()
		s.pauseTimer = nil
	}
}

// IsPaused reports whether command runs are currently suspended.
func (s *ControlServer) IsPaused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paused
}

// RecordEvent stores the most recent event of the event loop, including
// events skipped because gosn is paused.
func (s *ControlServer) RecordEvent(ev SyncEvent) {
	info := &EventInfo{
		Time:     time.Now(),
		Origin:   ev.Origin.String(),
		Type:     ev.Type.String(),
		Filename: ev.Filename,
		Files:    len(ev.Events),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastEvent = info
}

// BeginRun marks a command run as started, attributed to origin.
func (s *ControlServer) BeginRun(origin OriginType) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commandRunning = true
	s.commandStarted = time.Now()
	s.commandOrigin = origin.String()
}

// EndRun records the result of the command run started by BeginRun.
func (s *ControlServer) EndRun(ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commandRunning = false
	s.lastSync = &SyncInfo{
		FinishedAt: time.Now(),
		Duration:   time.Since(s.commandStarted).Round(time.Millisecond).String(),
		OK:         ok,
		Origin:     s.commandOrigin,
	}
}

// Snapshot returns a consistent copy of the current status.
func (s *ControlServer) Snapshot() Status {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := Status{
		State:          ControlStateRunning,
		CommandRunning: s.commandRunning,
		StartedAt:      s.startedAt,
		Config:         s.cfg,
	}

	if s.paused {
		st.State = ControlStatePaused
		since := s.pausedSince
		st.PausedSince = &since
		if !s.pauseDeadline.IsZero() {
			deadline := s.pauseDeadline
			st.PauseDeadline = &deadline
		}
	}

	if s.commandRunning {
		started := s.commandStarted
		st.CommandStarted = &started
	}

	if s.lastSync != nil {
		lastSync := *s.lastSync
		st.LastSync = &lastSync
	}

	if s.lastEvent != nil {
		lastEvent := *s.lastEvent
		st.LastEvent = &lastEvent
	}

	// s.timer is nil when gosn runs without a timer; a non-positive interval
	// timer would never fire, so it is not reported either.
	if s.timer != nil && s.timer.interval > 0 {
		next := s.timer.NextRun()
		st.NextTimer = &next
	}

	return st
}
