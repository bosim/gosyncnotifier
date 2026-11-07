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

// TestDummyNotifier verifies that a disabled notifier is a no-op.
func TestDummyNotifier(t *testing.T) {
	n, err := NewNotifier(false, NotifierBackendDBus, "")
	if err != nil {
		t.Fatalf("NewNotifier() error = %v", err)
	}

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
