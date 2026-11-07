package internal

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	notificationAppName = "gosn"
	notificationSummary = "gosn"
	notificationTimeout = 2 * time.Second
)

type Notifier interface {
	Low(message string) error
	Normal(message string) error
	Critical(message string) error
	Close() error
}

type NotifierBackend string

const (
	NotifierBackendDBus    NotifierBackend = "dbus"
	NotifierBackendCommand NotifierBackend = "command"
)

func ParseNotifierBackend(value string) (NotifierBackend, error) {
	switch NotifierBackend(value) {
	case NotifierBackendDBus, NotifierBackendCommand:
		return NotifierBackend(value), nil
	default:
		return "", fmt.Errorf("unknown notifier backend %q (want %q or %q)", value, NotifierBackendDBus, NotifierBackendCommand)
	}
}

type notifier struct {
	backend notifierBackend
}

type notifierEventType int

const (
	low notifierEventType = iota
	normal
	critical
)

func (nt notifierEventType) String() string {
	switch nt {
	case low:
		return "low"
	case normal:
		return "normal"
	case critical:
		return "critical"
	}

	return "unknown"
}

type notifierBackend interface {
	send(eventType notifierEventType, message string) error
	close() error
}

func (n *notifier) Low(message string) error {
	return n.backend.send(low, message)
}

func (n *notifier) Normal(message string) error {
	return n.backend.send(normal, message)
}

func (n *notifier) Critical(message string) error {
	return n.backend.send(critical, message)
}

func (n *notifier) Close() error {
	return n.backend.close()
}

type dummyBackend struct {
}

func (d dummyBackend) send(eventType notifierEventType, message string) error {
	return nil
}

func (d dummyBackend) close() error {
	return nil
}

type commandBackend struct {
	program string
	run     func(program string, args []string) error
}

func (b *commandBackend) send(eventType notifierEventType, message string) error {
	if b.program == "" {
		return fmt.Errorf("notify-send is not found")
	}

	args := []string{
		"-u",
		eventType.String(),
		notificationSummary,
		message,
	}

	return b.run(b.program, args)
}

func (b *commandBackend) close() error {
	return nil
}

// dbusSession abstracts a live notification connection so the reconnect logic
// can be tested without a running session bus.
type dbusSession interface {
	notify(eventType notifierEventType, message string) error
	close() error
}

type realDBusSession struct {
	conn *dbus.Conn
}

func (s *realDBusSession) notify(eventType notifierEventType, message string) error {
	ctx, cancel := context.WithTimeout(context.Background(), notificationTimeout)
	defer cancel()

	obj := s.conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.CallWithContext(
		ctx,
		"org.freedesktop.Notifications.Notify",
		0,
		notificationAppName,
		uint32(0),
		"",
		notificationSummary,
		message,
		[]string{},
		notificationHints(eventType),
		int32(-1),
	)
	if call.Err != nil {
		return call.Err
	}

	var id uint32
	return call.Store(&id)
}

func (s *realDBusSession) close() error {
	return s.conn.Close()
}

func notificationHints(eventType notifierEventType) map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"urgency": dbus.MakeVariant(byte(eventType)),
	}
}

func newDBusSession() (dbusSession, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}

	return &realDBusSession{conn: conn}, nil
}

type dbusBackend struct {
	mu      sync.Mutex
	session dbusSession
	dial    func() (dbusSession, error)
}

func newDBusBackend() *dbusBackend {
	return &dbusBackend{
		dial: newDBusSession,
	}
}

func (b *dbusBackend) send(eventType notifierEventType, message string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.session == nil {
		session, err := b.dial()
		if err != nil {
			return err
		}
		b.session = session
	}

	err := b.session.notify(eventType, message)
	if err == nil {
		return nil
	}

	// The session bus may have restarted or the connection may have gone away.
	// Close the stale connection and retry once with a fresh one.
	_ = b.session.close()
	b.session = nil

	session, dialErr := b.dial()
	if dialErr != nil {
		return err
	}
	b.session = session

	err = b.session.notify(eventType, message)
	if err != nil {
		_ = b.session.close()
		b.session = nil
	}

	return err
}

func (b *dbusBackend) close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.session == nil {
		return nil
	}

	err := b.session.close()
	b.session = nil
	return err
}

func NewNotifier(enabled bool, backend NotifierBackend, notifySendExec string) (Notifier, error) {
	dummyNotifier := &notifier{
		backend: &dummyBackend{},
	}

	if !enabled {
		return dummyNotifier, nil
	}

	switch backend {
	case NotifierBackendDBus:
		backend := newDBusBackend()

		// Try to connect at startup so a missing session bus is reported
		// immediately. The backend still reconnects on later send attempts.
		session, err := backend.dial()
		if err != nil {
			return &notifier{backend: backend}, err
		}
		backend.session = session

		return &notifier{backend: backend}, nil

	case NotifierBackendCommand:
		notifySendExec, err := exec.LookPath(notifySendExec)
		if err != nil {
			return dummyNotifier, err
		}

		return &notifier{
			backend: &commandBackend{
				program: notifySendExec,
				run:     runCommand,
			},
		}, nil

	default:
		return dummyNotifier, fmt.Errorf("unknown notifier backend %q", backend)
	}
}
