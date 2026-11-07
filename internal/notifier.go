package internal

import (
	"fmt"
	"os/exec"
)

const notificationSummary = "gosn"

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

type Notifier interface {
	Low(message string) error
	Normal(message string) error
	Critical(message string) error
	Close() error
}

type notifier struct {
	backend notifierBackend
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

func NewNotifier(enabled bool, backend NotifierBackend, notifySendExec string) (Notifier, error) {
	disabledNotifier := &notifier{
		backend: &dummyBackend{},
	}

	if !enabled {
		return disabledNotifier, nil
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
			return disabledNotifier, err
		}

		return &notifier{
			backend: &commandBackend{
				program: notifySendExec,
				run:     runCommand,
			},
		}, nil

	default:
		return disabledNotifier, fmt.Errorf("unknown notifier backend %q", backend)
	}
}
