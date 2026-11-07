package internal

import (
	"errors"
	"testing"
)

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
