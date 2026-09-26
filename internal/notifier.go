package internal

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
)

type Notifier struct {
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

func (n Notifier) run(eventType notifierEventType, message string) error {
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

	slog.Debug("Running command", "program", program, "args", args)

	cmd := exec.Command(program, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()

}

func (n Notifier) Low(message string) error {
	return n.run(low, message)
}

func (n Notifier) Normal(message string) error {
	return n.run(normal, message)
}

func (n Notifier) Critical(message string) error {
	return n.run(critical, message)
}

func NewNotifier() (*Notifier, error) {
	notifySendExec, err := exec.LookPath("notify-send")
	if err != nil {
		return nil, err
	}

	return &Notifier{
		notifySendExec: notifySendExec,
	}, nil
}
