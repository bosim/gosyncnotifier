package internal

import (
	"io"
	"os"
	"testing"
)

// captureOutput runs fn while capturing everything written to os.Stdout and
// os.Stderr. Tests using it must not run in parallel, since it swaps globals.
func captureOutput(t *testing.T, fn func() error) (stdout string, stderr string, err error) {
	t.Helper()

	outR, outW, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("os.Pipe() for stdout error = %v", pipeErr)
	}
	errR, errW, pipeErr := os.Pipe()
	if pipeErr != nil {
		_ = outR.Close()
		_ = outW.Close()
		t.Fatalf("os.Pipe() for stderr error = %v", pipeErr)
	}

	oldStdout, oldStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	defer func() {
		os.Stdout, os.Stderr = oldStdout, oldStderr
		_ = outR.Close()
		_ = errR.Close()
	}()

	runErr := fn()

	if closeErr := outW.Close(); closeErr != nil {
		t.Fatalf("closing stdout pipe error = %v", closeErr)
	}
	if closeErr := errW.Close(); closeErr != nil {
		t.Fatalf("closing stderr pipe error = %v", closeErr)
	}

	outBytes, readErr := io.ReadAll(outR)
	if readErr != nil {
		t.Fatalf("reading stdout error = %v", readErr)
	}
	errBytes, readErr := io.ReadAll(errR)
	if readErr != nil {
		t.Fatalf("reading stderr error = %v", readErr)
	}

	return string(outBytes), string(errBytes), runErr
}

// TestRunCommand verifies runCommand forwards output, arguments, and errors.
func TestRunCommand(t *testing.T) {
	tests := []struct {
		name       string
		program    string
		args       []string
		wantStdout string
		wantStderr string
		wantErr    bool
	}{
		{
			name:       "Run echo with single argument",
			program:    "echo",
			args:       []string{"test output"},
			wantStdout: "test output\n",
			wantErr:    false,
		},
		{
			name:       "Run echo with multiple arguments",
			program:    "echo",
			args:       []string{"arg1", "arg2"},
			wantStdout: "arg1 arg2\n",
			wantErr:    false,
		},
		{
			name:       "Non-zero exit code returns an error and preserves output",
			program:    "sh",
			args:       []string{"-c", "echo stdout-text; echo stderr-text >&2; exit 3"},
			wantStdout: "stdout-text\n",
			wantStderr: "stderr-text\n",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := captureOutput(t, func() error {
				return runCommand(tt.program, tt.args)
			})

			if tt.wantErr && err == nil {
				t.Error("Expected an error, but got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if stdout != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout, tt.wantStdout)
			}
			if stderr != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr, tt.wantStderr)
			}
		})
	}
}

// TestRunCommand_InvalidCommand tests behavior when the program does not exist.
func TestRunCommand_InvalidCommand(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runCommand("nonexistentcommand", []string{})
	})

	if err == nil {
		t.Fatal("Expected an error, but got nil")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}
