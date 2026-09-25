package internal

import (
	"log/slog"
	"os"
	"os/exec"
)

type Runner struct {
	cmd []string
}

func (r Runner) Run() error {
	program := r.cmd[0]
	args := r.cmd[1:]

	slog.Debug("Running command", "program", program, "args", args)

	cmd := exec.Command(program, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func NewRunner(cmd []string) *Runner {
	return &Runner{
		cmd: cmd,
	}
}
