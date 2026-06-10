package bridge

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// LaunchSpec describes the dwe subprocess for one session: argv pass-through
// from HELLO, the validated host cwd, and the filtered env with the
// host-controlled variables appended (design D5 step 3).
type LaunchSpec struct {
	// ExecPath is the dwe binary; empty means the daemon's own executable.
	ExecPath string
	Argv     []string
	Dir      string
	Env      []string
}

// Process is one running bridged subprocess, as seen by the session pump.
type Process interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	// Signal delivers sig to the whole process group.
	Signal(sig syscall.Signal) error
	// Kill force-terminates the process group (SIGKILL).
	Kill() error
	// Wait reaps the process and returns its exit code; a signal death maps
	// to the shell convention 128+signal. Call only after both output pipes
	// hit EOF (os/exec closes the pipes on Wait).
	Wait() int
}

// LaunchFunc is the injectable exec seam: production uses launchOS; tests
// substitute an in-process fake. Tests MUST substitute it — the production
// path resolves os.Executable(), and forking the test binary re-executes
// the test suite recursively (see internal/cli/lifecycle/testhelpers_test.go
// for the documented hazard).
type LaunchFunc func(spec LaunchSpec) (Process, error)

// launchOS is the production launcher: `dwe <argv...>` in its own process
// group with plain pipes — the bridge never allocates a pty (design D11).
func launchOS(spec LaunchSpec) (Process, error) {
	path := spec.ExecPath
	if path == "" {
		var err error
		path, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolving dwe executable: %w", err)
		}
	}
	cmd := exec.Command(path, spec.Argv...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	setProcessGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("opening stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("opening stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("opening stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &osProcess{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

type osProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func (p *osProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *osProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *osProcess) Stderr() io.ReadCloser { return p.stderr }
func (p *osProcess) Signal(sig syscall.Signal) error {
	return signalProcessGroup(p.cmd, sig)
}
func (p *osProcess) Kill() error { return signalProcessGroup(p.cmd, syscall.SIGKILL) }

func (p *osProcess) Wait() int {
	err := p.cmd.Wait()
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitStatus(exitErr)
	}
	return 1
}
