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

// RunCapture behaves like Run but additionally returns the combined stdout
// and stderr output of the command. The output is still forwarded to
// os.Stdout and os.Stderr while it is produced.
func (r Runner) RunCapture() (string, error) {
	return runCommandCapture(r.program, r.args)
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

// NewShellRunner wraps a shell command string so it can be used as a pre- or
// post-command: the script is executed as `sh -c <script>`, which allows
// pipelines, conditionals and quoted arguments.
func NewShellRunner(script string) (*Runner, error) {
	return NewRunner([]string{"sh", "-c", script})
}

// RunHooks are optional callbacks around the main command of a sequence.
// They let the caller keep external state (such as the gosnctl status
// BeginRun/EndRun markers) in sync without coupling the sequencing itself to
// the control server. Both fields may be nil.
type RunHooks struct {
	// BeforeMain is called after the pre-command succeeded, immediately
	// before the main command starts.
	BeforeMain func()
	// AfterMain is called with the result of the main command, immediately
	// after it finished and before the post-command starts.
	AfterMain func(mainErr error)
}

// RunResult reports the outcome of a pre/main/post command sequence.
type RunResult struct {
	// Skipped is true when the pre-command failed: neither the main command
	// nor the post-command ran.
	Skipped bool
	// PreErr holds the pre-command error when Skipped is set.
	PreErr error
	// MainErr is the error of the main command (nil on success).
	MainErr error
	// PostErr is the error of the post-command; nil when no post-command is
	// configured or it succeeded.
	PostErr error
	// PostOutput is the combined stdout and stderr of the post-command.
	PostOutput string
}

// RunSequence runs the pre-command (when non-nil), the main command (must not
// be nil) and the post-command (when non-nil) in that order. A failing
// pre-command skips the remaining commands; the post-command runs whenever
// the main command ran, even when the main command failed. hooks may be the
// zero value.
func RunSequence(pre, main, post *Runner, hooks RunHooks) RunResult {
	if pre != nil {
		if err := pre.Run(); err != nil {
			return RunResult{Skipped: true, PreErr: err}
		}
	}

	if hooks.BeforeMain != nil {
		hooks.BeforeMain()
	}

	result := RunResult{MainErr: main.Run()}

	if hooks.AfterMain != nil {
		hooks.AfterMain(result.MainErr)
	}

	if post != nil {
		result.PostOutput, result.PostErr = post.RunCapture()
	}

	return result
}
