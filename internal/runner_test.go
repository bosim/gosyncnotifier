package internal

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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

// TestNewShellRunner verifies that NewShellRunner resolves sh and passes the
// script verbatim as the -c argument, and that the script is executed by a
// shell (pipelines work).
func TestNewShellRunner(t *testing.T) {
	script := `echo "hello world" | tr a-z A-Z`

	runner, err := NewShellRunner(script)
	if err != nil {
		t.Fatalf("NewShellRunner(%q) error = %v", script, err)
	}

	wantProgram, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("exec.LookPath(sh) error = %v", err)
	}
	if runner.program != wantProgram {
		t.Errorf("program = %q, want %q", runner.program, wantProgram)
	}
	if want := []string{"-c", script}; !slices.Equal(runner.args, want) {
		t.Errorf("args = %q, want %q", runner.args, want)
	}

	stdout, stderr, err := captureOutput(t, runner.Run)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if want := "HELLO WORLD\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

// TestNewShellRunnerUnknownShell verifies that a missing shell is reported
// at construction time, so a broken configuration fails fast at startup.
func TestNewShellRunnerUnknownShell(t *testing.T) {
	// NewShellRunner resolves "sh" through PATH; an empty PATH makes the
	// lookup fail no matter what the script is.
	t.Setenv("PATH", "")

	runner, err := NewShellRunner("echo hi")
	if err == nil {
		t.Fatal("NewShellRunner() error = nil, want an error")
	}
	if runner != nil {
		t.Errorf("NewShellRunner() runner = %+v, want nil", runner)
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

// TestRunnerRunCapture verifies that RunCapture forwards the output to
// stdout/stderr as usual while additionally returning it combined, and that
// it reports non-zero exits. Tests using captureOutput must not run in
// parallel, since it swaps os.Stdout and os.Stderr.
func TestRunnerRunCapture(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		wantStdout string // also a substring of the captured output
		wantStderr string // also a substring of the captured output
		wantErr    bool
	}{
		{
			name:       "captures stdout and stderr",
			script:     "echo stdout-text; echo stderr-text >&2",
			wantStdout: "stdout-text\n",
			wantStderr: "stderr-text\n",
		},
		{
			name:       "captures output of a failing command",
			script:     "echo conflict-detail; echo detail >&2; exit 5",
			wantStdout: "conflict-detail\n",
			wantStderr: "detail\n",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := Runner{program: "sh", args: []string{"-c", tt.script}}

			var captured string
			stdout, stderr, err := captureOutput(t, func() error {
				var runErr error
				captured, runErr = runner.RunCapture()
				return runErr
			})

			if tt.wantErr && err == nil {
				t.Error("RunCapture() error = nil, want an error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("RunCapture() error = %v, want nil", err)
			}

			// The captured output is stdout followed by stderr.
			if want := tt.wantStdout + tt.wantStderr; captured != want {
				t.Errorf("captured output = %q, want %q", captured, want)
			}
			// Both streams are still forwarded unchanged.
			if stdout != tt.wantStdout {
				t.Errorf("forwarded stdout = %q, want %q", stdout, tt.wantStdout)
			}
			if stderr != tt.wantStderr {
				t.Errorf("forwarded stderr = %q, want %q", stderr, tt.wantStderr)
			}
		})
	}
}

// TestRunSequence verifies the pre/main/post ordering: a failing precmd
// skips everything, the postcmd runs even after a failed main command, and
// the hooks bracket only the main command.
func TestRunSequence(t *testing.T) {
	shellRunner := func(t *testing.T, script string) *Runner {
		t.Helper()
		runner, err := NewShellRunner(script)
		if err != nil {
			t.Fatalf("NewShellRunner(%q) error = %v", script, err)
		}
		return runner
	}

	t.Run("failing precmd skips main, post and hooks", func(t *testing.T) {
		pre := shellRunner(t, "exit 3")
		main := shellRunner(t, "echo main-ran")
		post := shellRunner(t, "echo post-ran")

		var res RunResult
		hookCalls := 0
		stdout, _, err := captureOutput(t, func() error {
			res = RunSequence(pre, main, post, RunHooks{
				BeforeMain: func() { hookCalls++ },
				AfterMain:  func(error) { hookCalls++ },
			})
			return nil
		})
		if err != nil {
			t.Fatalf("captureOutput() error = %v", err)
		}

		if !res.Skipped {
			t.Error("Skipped = false, want true")
		}
		if res.PreErr == nil {
			t.Error("PreErr = nil, want an error")
		}
		if res.MainErr != nil {
			t.Errorf("MainErr = %v, want nil", res.MainErr)
		}
		if res.PostOutput != "" || res.PostErr != nil {
			t.Errorf("post ran with PostOutput = %q, PostErr = %v; want both empty", res.PostOutput, res.PostErr)
		}
		if hookCalls != 0 {
			t.Errorf("hook calls = %d, want 0", hookCalls)
		}
		if strings.Contains(stdout, "main-ran") || strings.Contains(stdout, "post-ran") {
			t.Errorf("stdout = %q, want no output from main or post", stdout)
		}
	})

	t.Run("postcmd runs after a failed main command", func(t *testing.T) {
		main := shellRunner(t, "echo main-output; exit 4")
		post := shellRunner(t, "echo post-output")

		var res RunResult
		afterMainErr := errors.New("hook not called")
		hookCalled := false
		stdout, _, err := captureOutput(t, func() error {
			res = RunSequence(nil, main, post, RunHooks{
				AfterMain: func(mainErr error) {
					hookCalled = true
					afterMainErr = mainErr
				},
			})
			return nil
		})
		if err != nil {
			t.Fatalf("captureOutput() error = %v", err)
		}

		if res.Skipped {
			t.Error("Skipped = true, want false")
		}
		if res.MainErr == nil {
			t.Error("MainErr = nil, want an error")
		}
		if res.PostErr != nil {
			t.Errorf("PostErr = %v, want nil", res.PostErr)
		}
		if want := "post-output\n"; res.PostOutput != want {
			t.Errorf("PostOutput = %q, want %q", res.PostOutput, want)
		}
		if !hookCalled {
			t.Error("AfterMain hook not called")
		}
		if afterMainErr != res.MainErr {
			t.Errorf("AfterMain hook error = %v, want %v", afterMainErr, res.MainErr)
		}
		for _, want := range []string{"main-output\n", "post-output\n"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout = %q, want it to contain %q", stdout, want)
			}
		}
	})

	t.Run("postcmd failure is reported with its output", func(t *testing.T) {
		main := shellRunner(t, "exit 0")
		post := shellRunner(t, "echo conflict-detail; echo detail >&2; exit 1")

		res := RunSequence(nil, main, post, RunHooks{})

		if res.Skipped {
			t.Error("Skipped = true, want false")
		}
		if res.MainErr != nil {
			t.Errorf("MainErr = %v, want nil", res.MainErr)
		}
		if res.PostErr == nil {
			t.Error("PostErr = nil, want an error")
		}
		for _, want := range []string{"conflict-detail\n", "detail\n"} {
			if !strings.Contains(res.PostOutput, want) {
				t.Errorf("PostOutput = %q, want it to contain %q", res.PostOutput, want)
			}
		}
	})

	t.Run("nil pre and post run only the main command", func(t *testing.T) {
		main := shellRunner(t, "echo only-main")

		var res RunResult
		beforeCalled := false
		stdout, _, err := captureOutput(t, func() error {
			res = RunSequence(nil, main, nil, RunHooks{
				BeforeMain: func() { beforeCalled = true },
			})
			return nil
		})
		if err != nil {
			t.Fatalf("captureOutput() error = %v", err)
		}

		if res.Skipped {
			t.Error("Skipped = true, want false")
		}
		if res.MainErr != nil {
			t.Errorf("MainErr = %v, want nil", res.MainErr)
		}
		if res.PostErr != nil || res.PostOutput != "" {
			t.Errorf("post result = (%v, %q), want zero values", res.PostErr, res.PostOutput)
		}
		if !beforeCalled {
			t.Error("BeforeMain hook not called")
		}
		if want := "only-main\n"; stdout != want {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
	})
}
