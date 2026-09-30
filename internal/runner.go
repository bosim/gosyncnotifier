package internal

import (
	"os/exec"
)

type Runner struct {
	program string
	args    []string
}

func (r Runner) Run() error {
	return runCommand(r.program, r.args)
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
