package internal

import (
	"context"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	notificationAppName = "gosn"
	notificationTimeout = 2 * time.Second
)

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
