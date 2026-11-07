package internal

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"os/exec"
)

func runCommand(program string, args []string) error {
	slog.Debug("Running command", "program", program, "args", args)

	cmd := exec.Command(program, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// runCommandCapture behaves like runCommand but additionally returns the
// combined stdout and stderr output of the command.
//
// os/exec copies stdout and stderr from two separate goroutines, so each
// stream gets its own buffer; the combined output is stdout followed by
// stderr (the interleaving of the two streams is not defined anyway).
func runCommandCapture(program string, args []string) (string, error) {
	slog.Debug("Running command (captured)", "program", program, "args", args)

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(program, args...)
	cmd.Stdout = io.MultiWriter(os.Stdout, &stdout)
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)

	err := cmd.Run()
	return stdout.String() + stderr.String(), err
}
