package internal

import (
	"fmt"
	"os/exec"
)

type Notifier interface {
	Low(message string) error
	Normal(message string) error
	Critical(message string) error
}

type dummyNotifier struct {
}

func (n dummyNotifier) Low(message string) error { return nil }
func (n dummyNotifier) Normal(message string) error { return nil }
func (n dummyNotifier) Critical(message string) error { return nil }

type notifier struct {
	notifySendExec string
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

func (n notifier) run(eventType notifierEventType, message string) error {
	if n.notifySendExec == "" {
		return fmt.Errorf("notify-send is not found")
	}
	
	program := n.notifySendExec
	args := []string{
		"-u",
		eventType.String(),
		"gosn",
		message,
	}

	return runCommand(program, args)
}

func (n notifier) Low(message string) error {
	return n.run(low, message)
}

func (n notifier) Normal(message string) error {
	return n.run(normal, message)
}

func (n notifier) Critical(message string) error {
	return n.run(critical, message)
}

func NewNotifier(enabled bool, notifySendExec string) (Notifier, error) {
	if !enabled {
		return &dummyNotifier{}, nil
	}

	var err error
	notifySendExec, err = exec.LookPath(notifySendExec)
	if err != nil {
		return nil, err
	}

	return &notifier{
		notifySendExec: notifySendExec,
	}, nil
}
