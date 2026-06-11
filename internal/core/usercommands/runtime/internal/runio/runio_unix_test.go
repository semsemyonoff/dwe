//go:build !windows

package runio

// The raw syscall.Pipe repro is unix-only ([]int fds); the production shape
// it pins (a daemon-forked dwe inheriting a blocking pipe as fd 0) is too.

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/bridgeclient"
)

// TestBridgedPTY_cleanupUnblocksOnBlockingPipeStdin pins the daemon-forked
// shape: the bridge daemon wires the subprocess stdin via exec.Cmd StdinPipe,
// so fd 0 is a plain BLOCKING pipe — Go cannot poll it and SetReadDeadline
// fails. Cleanup must still return instead of waiting for the unkillable
// stdin pump (the v1 hang: every bridged command froze after its child
// exited).
func TestBridgedPTY_cleanupUnblocksOnBlockingPipeStdin(t *testing.T) {
	clearColorVars(t)
	stubStdoutTTY(t, false)
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv(bridgeclient.EnvBridgeStdinTTY, "1")

	// A raw syscall pipe stays in blocking mode; os.NewFile then has no
	// runtime poller, exactly like the fd 0 a daemon-forked dwe inherits.
	var fds [2]int
	if err := syscall.Pipe(fds[:]); err != nil {
		t.Fatalf("pipe: %v", err)
	}
	r := os.NewFile(uintptr(fds[0]), "blocking-pipe-r")
	w := os.NewFile(uintptr(fds[1]), "blocking-pipe-w")
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }() // never written: the pump stays parked in read(2)

	var out syncBuffer
	rc := spec.RunContext{Stdout: &out, Stderr: &out, Stdin: r}
	c := exec.Command("true")
	cleanup := WireChildIO(rc, c)
	if err := c.Run(); err != nil {
		t.Fatalf("child run: %v", err)
	}

	done := make(chan struct{})
	go func() { cleanup(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup deadlocked on a blocking-pipe stdin (the bridge-forked shape)")
	}
}
