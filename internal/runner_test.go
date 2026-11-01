package internal

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// TestNewRunner verifies that NewRunner resolves the program through PATH and
// keeps the remaining arguments untouched.
func TestNewRunner(t *testing.T) {
	tests := []struct {
		name string
		cmd  []string
	}{
		{
			name: "command without arguments",
			cmd:  []string{"echo"},
		},
		{
			name: "command with arguments",
			cmd:  []string{"echo", "hello", "world"},
		},
		{
			name: "arguments that look like flags",
			cmd:  []string{"sh", "-c", "exit 0"},
		},
		{
			name: "argument containing spaces",
			cmd:  []string{"echo", "hello world"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantProgram, err := exec.LookPath(tt.cmd[0])
			if err != nil {
				t.Fatalf("exec.LookPath(%q) error = %v", tt.cmd[0], err)
			}

			runner, err := NewRunner(tt.cmd)
			if err != nil {
				t.Fatalf("NewRunner(%q) error = %v", tt.cmd, err)
			}
			if runner == nil {
				t.Fatal("NewRunner() returned nil runner")
			}

			if runner.program != wantProgram {
				t.Errorf("program = %q, want %q", runner.program, wantProgram)
			}

			wantArgs := tt.cmd[1:]
			if !slices.Equal(runner.args, wantArgs) {
				t.Errorf("args = %q, want %q", runner.args, wantArgs)
			}
		})
	}
}

// TestNewRunnerAbsolutePath verifies that an executable addressed by an
// absolute path is accepted even though it is not on PATH.
func TestNewRunnerAbsolutePath(t *testing.T) {
	script := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", script, err)
	}

	runner, err := NewRunner([]string{script, "argument"})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	if runner.program != script {
		t.Errorf("program = %q, want %q", runner.program, script)
	}
	if want := []string{"argument"}; !slices.Equal(runner.args, want) {
		t.Errorf("args = %q, want %q", runner.args, want)
	}
}

// TestNewRunnerUnknownCommand verifies that NewRunner fails when the program
// cannot be found.
func TestNewRunnerUnknownCommand(t *testing.T) {
	runner, err := NewRunner([]string{"gosn-command-that-does-not-exist"})
	if err == nil {
		t.Fatal("NewRunner() error = nil, want an error")
	}
	if runner != nil {
		t.Errorf("NewRunner() runner = %+v, want nil", runner)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("NewRunner() error = %v, want it to wrap exec.ErrNotFound", err)
	}
}

// TestRunnerRun verifies that Run executes the configured program, forwards
// stdout and stderr, and reports non-zero exits. Tests using captureOutput must
// not run in parallel, since it swaps os.Stdout and os.Stderr.
func TestRunnerRun(t *testing.T) {
	tests := []struct {
		name       string
		program    string
		args       []string
		wantStdout string
		wantStderr string
		wantErr    bool
	}{
		{
			name:       "single argument",
			program:    "echo",
			args:       []string{"test output"},
			wantStdout: "test output\n",
		},
		{
			name:       "multiple arguments",
			program:    "echo",
			args:       []string{"arg1", "arg2"},
			wantStdout: "arg1 arg2\n",
		},
		{
			name:       "argument with spaces stays a single argument",
			program:    "echo",
			args:       []string{"hello world"},
			wantStdout: "hello world\n",
		},
		{
			name:       "non-zero exit forwards output and returns an error",
			program:    "sh",
			args:       []string{"-c", "echo stdout-text; echo stderr-text >&2; exit 3"},
			wantStdout: "stdout-text\n",
			wantStderr: "stderr-text\n",
			wantErr:    true,
		},
		{
			name:    "missing program returns an error",
			program: "gosn-command-that-does-not-exist",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := Runner{program: tt.program, args: tt.args}

			stdout, stderr, err := captureOutput(t, runner.Run)

			if tt.wantErr && err == nil {
				t.Error("Run() error = nil, want an error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Run() error = %v, want nil", err)
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

// TestRunnerRunFromConstructor verifies the end-to-end path used by main:
// build a runner from CLI arguments and run it.
func TestRunnerRunFromConstructor(t *testing.T) {
	runner, err := NewRunner([]string{"sh", "-c", "echo from-runner"})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	stdout, stderr, err := captureOutput(t, runner.Run)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if want := "from-runner\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

// TestRunnerRunPreservesExitCode verifies that callers can inspect the exit
// status of a failed command, which is what main logs and reports.
func TestRunnerRunPreservesExitCode(t *testing.T) {
	runner := Runner{program: "sh", args: []string{"-c", "exit 7"}}

	_, _, err := captureOutput(t, runner.Run)
	if err == nil {
		t.Fatal("Run() error = nil, want an error")
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Run() error = %v, want *exec.ExitError", err)
	}
	if got, want := exitErr.ExitCode(), 7; got != want {
		t.Errorf("ExitCode() = %d, want %d", got, want)
	}
}
