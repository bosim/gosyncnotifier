package internal

import (
	"log/slog"
	"os"
	"os/exec"
)

type Runner struct {
	program string
	args    []string
}

func (r Runner) Run() error {

	slog.Debug("Running command", "program", r.program, "args", r.args)

	cmd := exec.Command(r.program, r.args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func NewRunner(cmd []string) (*Runner, error) {
	cmdExec, err := exec.LookPath(cmd[0])
	if err != nil {
		return nil, err
	}

	program := cmdExec
	args := cmd[1:]

	return &Runner{
		program: program,
		args:    args,
	}, nil
}
