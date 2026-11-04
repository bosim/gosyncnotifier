package internal

import (
	"errors"
	"reflect"
	"testing"
)

type fakeNotifierBackend struct {
	eventTypes []notifierEventType
	messages   []string
	err        error
	closed     bool
}

func (f *fakeNotifierBackend) send(eventType notifierEventType, message string) error {
	f.eventTypes = append(f.eventTypes, eventType)
	f.messages = append(f.messages, message)
	return f.err
}

func (f *fakeNotifierBackend) close() error {
	f.closed = true
	return nil
}

// TestNotifierUrgencyMapping verifies each public notification level is passed
// through to the backend with the expected urgency.
func TestNotifierUrgencyMapping(t *testing.T) {
	tests := []struct {
		name string
		send func(*notifier, string) error
		want notifierEventType
	}{
		{
			name: "low",
			send: func(n *notifier, message string) error {
				return n.Low(message)
			},
			want: low,
		},
		{
			name: "normal",
			send: func(n *notifier, message string) error {
				return n.Normal(message)
			},
			want: normal,
		},
		{
			name: "critical",
			send: func(n *notifier, message string) error {
				return n.Critical(message)
			},
			want: critical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &fakeNotifierBackend{}
			n := &notifier{backend: backend}

			if err := tt.send(n, "test message"); err != nil {
				t.Fatalf("send() error = %v", err)
			}

			if len(backend.eventTypes) != 1 {
				t.Fatalf("eventTypes length = %d, want 1", len(backend.eventTypes))
			}
			if backend.eventTypes[0] != tt.want {
				t.Errorf("eventType = %v, want %v", backend.eventTypes[0], tt.want)
			}
			if backend.messages[0] != "test message" {
				t.Errorf("message = %q, want %q", backend.messages[0], "test message")
			}
		})
	}
}

// TestNotifierPropagatesBackendError verifies backend errors are returned.
func TestNotifierPropagatesBackendError(t *testing.T) {
	wantErr := errors.New("notification failed")
	backend := &fakeNotifierBackend{err: wantErr}
	n := &notifier{backend: backend}

	if err := n.Normal("test message"); !errors.Is(err, wantErr) {
		t.Errorf("Normal() error = %v, want %v", err, wantErr)
	}
}

// TestNotifierClose verifies Close is forwarded to the backend.
func TestNotifierClose(t *testing.T) {
	backend := &fakeNotifierBackend{}
	n := &notifier{backend: backend}

	if err := n.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !backend.closed {
		t.Error("backend was not closed")
	}
}

// TestDummyNotifier verifies that the disabled notifier is a no-op.
func TestDummyNotifier(t *testing.T) {
	n := &dummyNotifier{}

	if err := n.Low("low"); err != nil {
		t.Errorf("Low() error = %v", err)
	}
	if err := n.Normal("normal"); err != nil {
		t.Errorf("Normal() error = %v", err)
	}
	if err := n.Critical("critical"); err != nil {
		t.Errorf("Critical() error = %v", err)
	}
	if err := n.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestParseNotifierBackend(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    NotifierBackend
		wantErr bool
	}{
		{
			name:  "dbus",
			value: "dbus",
			want:  NotifierBackendDBus,
		},
		{
			name:  "command",
			value: "command",
			want:  NotifierBackendCommand,
		},
		{
			name:    "unknown",
			value:   "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseNotifierBackend(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Expected an error, but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseNotifierBackend() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ParseNotifierBackend() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCommandBackendSend verifies the notify-send-compatible arguments.
func TestCommandBackendSend(t *testing.T) {
	var gotProgram string
	var gotArgs []string

	backend := &commandBackend{
		program: "/usr/bin/notify-send",
		run: func(program string, args []string) error {
			gotProgram = program
			gotArgs = append([]string(nil), args...)
			return nil
		},
	}

	if err := backend.send(critical, "sync failed"); err != nil {
		t.Fatalf("send() error = %v", err)
	}

	if gotProgram != backend.program {
		t.Errorf("program = %q, want %q", gotProgram, backend.program)
	}

	wantArgs := []string{"-u", "critical", notificationSummary, "sync failed"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("args = %#v, want %#v", gotArgs, wantArgs)
	}
}

// TestCommandBackendMissingExecutable verifies the command backend reports a
// missing executable instead of trying to run an empty program.
func TestCommandBackendMissingExecutable(t *testing.T) {
	backend := &commandBackend{
		run: func(program string, args []string) error {
			t.Fatal("run should not be called without a program")
			return nil
		},
	}

	if err := backend.send(low, "test message"); err == nil {
		t.Fatal("Expected an error, but got nil")
	}
}

// TestNotificationHints verifies the urgency hint uses the D-Bus byte type and
// matches the notifier levels.
func TestNotificationHints(t *testing.T) {
	tests := []struct {
		name  string
		event notifierEventType
		want  byte
	}{
		{name: "low", event: low, want: 0},
		{name: "normal", event: normal, want: 1},
		{name: "critical", event: critical, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hints := notificationHints(tt.event)
			urgency, ok := hints["urgency"]
			if !ok {
				t.Fatal("urgency hint is missing")
			}
			if urgency.Signature().String() != "y" {
				t.Errorf("urgency signature = %q, want %q", urgency.Signature().String(), "y")
			}
			got, ok := urgency.Value().(byte)
			if !ok {
				t.Fatalf("urgency value has type %T, want byte", urgency.Value())
			}
			if got != tt.want {
				t.Errorf("urgency = %d, want %d", got, tt.want)
			}
		})
	}
}

type fakeDBusSession struct {
	notifyErr  error
	eventTypes []notifierEventType
	messages   []string
	closed     bool
}

func (s *fakeDBusSession) notify(eventType notifierEventType, message string) error {
	s.eventTypes = append(s.eventTypes, eventType)
	s.messages = append(s.messages, message)
	return s.notifyErr
}

func (s *fakeDBusSession) close() error {
	s.closed = true
	return nil
}

// TestDBusBackendReusesSession verifies that a healthy connection is kept
// between notifications.
func TestDBusBackendReusesSession(t *testing.T) {
	session := &fakeDBusSession{}
	dialCalls := 0
	backend := &dbusBackend{
		dial: func() (dbusSession, error) {
			dialCalls++
			return session, nil
		},
	}

	if err := backend.send(normal, "first"); err != nil {
		t.Fatalf("first send() error = %v", err)
	}
	if err := backend.send(critical, "second"); err != nil {
		t.Fatalf("second send() error = %v", err)
	}

	if dialCalls != 1 {
		t.Errorf("dial calls = %d, want 1", dialCalls)
	}
	if len(session.messages) != 2 {
		t.Fatalf("messages length = %d, want 2", len(session.messages))
	}
	if session.messages[0] != "first" || session.messages[1] != "second" {
		t.Errorf("messages = %#v, want %#v", session.messages, []string{"first", "second"})
	}
}

// TestDBusBackendReconnectsOnError verifies that a failed send closes the
// connection, dials again, and retries once.
func TestDBusBackendReconnectsOnError(t *testing.T) {
	first := &fakeDBusSession{notifyErr: errors.New("connection lost")}
	second := &fakeDBusSession{}
	sessions := []dbusSession{first, second}
	dialCalls := 0

	backend := &dbusBackend{
		dial: func() (dbusSession, error) {
			if dialCalls >= len(sessions) {
				t.Fatalf("unexpected dial call %d", dialCalls+1)
			}
			session := sessions[dialCalls]
			dialCalls++
			return session, nil
		},
	}

	if err := backend.send(critical, "retry me"); err != nil {
		t.Fatalf("send() error = %v", err)
	}

	if dialCalls != 2 {
		t.Errorf("dial calls = %d, want 2", dialCalls)
	}
	if !first.closed {
		t.Error("failed session was not closed")
	}
	if len(second.messages) != 1 {
		t.Fatalf("retried messages length = %d, want 1", len(second.messages))
	}
	if second.messages[0] != "retry me" {
		t.Errorf("retried message = %q, want %q", second.messages[0], "retry me")
	}
}

// TestDBusBackendReturnsErrorAfterRetry verifies that the second failure is
// returned and the retried connection is cleaned up.
func TestDBusBackendReturnsErrorAfterRetry(t *testing.T) {
	firstErr := errors.New("first failure")
	secondErr := errors.New("second failure")
	first := &fakeDBusSession{notifyErr: firstErr}
	second := &fakeDBusSession{notifyErr: secondErr}
	sessions := []dbusSession{first, second}
	dialCalls := 0

	backend := &dbusBackend{
		dial: func() (dbusSession, error) {
			session := sessions[dialCalls]
			dialCalls++
			return session, nil
		},
	}

	err := backend.send(low, "still failing")
	if !errors.Is(err, secondErr) {
		t.Errorf("send() error = %v, want %v", err, secondErr)
	}
	if !second.closed {
		t.Error("retried session was not closed after failure")
	}
}

// TestDBusBackendDialError verifies that a dial failure is returned.
func TestDBusBackendDialError(t *testing.T) {
	wantErr := errors.New("no session bus")
	backend := &dbusBackend{
		dial: func() (dbusSession, error) {
			return nil, wantErr
		},
	}

	if err := backend.send(low, "test message"); !errors.Is(err, wantErr) {
		t.Errorf("send() error = %v, want %v", err, wantErr)
	}
}

// TestDBusBackendClose verifies the active connection is closed and a later
// send creates a new one.
func TestDBusBackendClose(t *testing.T) {
	first := &fakeDBusSession{}
	second := &fakeDBusSession{}
	sessions := []dbusSession{first, second}
	dialCalls := 0

	backend := &dbusBackend{
		dial: func() (dbusSession, error) {
			session := sessions[dialCalls]
			dialCalls++
			return session, nil
		},
	}

	if err := backend.send(normal, "first"); err != nil {
		t.Fatalf("send() error = %v", err)
	}
	if err := backend.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}
	if !first.closed {
		t.Error("active session was not closed")
	}

	if err := backend.send(normal, "second"); err != nil {
		t.Fatalf("send() after close error = %v", err)
	}
	if dialCalls != 2 {
		t.Errorf("dial calls = %d, want 2", dialCalls)
	}
	if len(second.messages) != 1 {
		t.Errorf("new session messages length = %d, want 1", len(second.messages))
	}
}
