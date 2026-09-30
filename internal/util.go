package internal

import (
	"os"
	"os/exec"
	"log/slog"
)

func runCommand(program string, args []string) error {
	slog.Debug("Running command", "program", program, "args", args)

	cmd := exec.Command(program, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
