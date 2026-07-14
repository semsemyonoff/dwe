package containers

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

// writeCountingStub writes an executable sh stub that increments a counter file
// on every invocation, fails (exit 1, stderr message) while the count is below
// failUntil, then prints the requested service names one per line and exits 0.
// It returns the stub path and the counter path.
func writeCountingStub(t *testing.T, failUntil int) (stub, counter string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is sh-based")
	}
	dir := t.TempDir()
	stub = filepath.Join(dir, "docker-stub")
	counter = filepath.Join(dir, "count")
	body := "#!/bin/sh\n" +
		"n=$(cat " + counter + " 2>/dev/null || echo 0)\n" +
		"n=$((n+1))\n" +
		"echo $n > " + counter + "\n" +
		"if [ \"$n\" -lt " + strconv.Itoa(failUntil) + " ]; then\n" +
		"  echo 'transient probe failure' >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		// Echo everything after the literal `--services` token, one per line.
		"seen=0\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$seen\" = 1 ]; then echo \"$a\"; fi\n" +
		"  if [ \"$a\" = \"--services\" ]; then seen=1; fi\n" +
		"done\n" +
		"exit 0\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub, counter
}

func shrinkBackoff(t *testing.T) {
	t.Helper()
	orig := probeRetryBackoff
	probeRetryBackoff = time.Millisecond
	t.Cleanup(func() { probeRetryBackoff = orig })
}

func readCountOrZero(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return n
}

func readCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse counter %q: %v", data, err)
	}
	return n
}

func TestRunningServicesWithRetry_RecoversFromTransientFailure(t *testing.T) {
	shrinkBackoff(t)
	// Fail the first probe (exit 1), succeed on the second — the exact shape of
	// the post-`up --wait` transient failure the retry exists for.
	stub, counter := writeCountingStub(t, 2)
	compose := &docker.Compose{Bin: stub}

	running, err := runningServicesWithRetry(context.Background(), compose, []string{"nginx", "db"})
	if err != nil {
		t.Fatalf("expected recovery after one transient failure, got %v", err)
	}
	if len(running) != 2 || running[0] != "nginx" || running[1] != "db" {
		t.Fatalf("unexpected running services: %v", running)
	}
	if got := readCount(t, counter); got != 2 {
		t.Fatalf("expected exactly one retry (2 invocations), got %d", got)
	}
}

func TestRunningServicesWithRetry_GivesUpAfterRetries_SurfacesStderr(t *testing.T) {
	shrinkBackoff(t)
	// Always fail: the helper exhausts probeRetries+1 attempts and returns the
	// last error, which must still carry the subprocess stderr.
	stub, counter := writeCountingStub(t, 1000)
	compose := &docker.Compose{Bin: stub}

	_, err := runningServicesWithRetry(context.Background(), compose, []string{"nginx"})
	if err == nil {
		t.Fatal("expected an error when every probe fails")
	}
	if !strings.Contains(err.Error(), "transient probe failure") {
		t.Fatalf("final error must surface subprocess stderr, got %q", err.Error())
	}
	if got := readCount(t, counter); got != probeRetries+1 {
		t.Fatalf("expected %d attempts, got %d", probeRetries+1, got)
	}
}

func TestRunningServicesWithRetry_CancelledContextStopsRetrying(t *testing.T) {
	shrinkBackoff(t)
	stub, counter := writeCountingStub(t, 1000) // always fails
	compose := &docker.Compose{Bin: stub}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	_, err := runningServicesWithRetry(ctx, compose, []string{"nginx"})
	if err == nil {
		t.Fatal("expected an error with a cancelled context")
	}
	// A pre-cancelled context makes exec.CommandContext refuse to start the
	// process, so the stub may run 0 times; the retry loop must not keep
	// hammering after cancellation, so at most one attempt is tolerated.
	if got := readCountOrZero(counter); got > 1 {
		t.Fatalf("cancelled context must stop retries, got %d invocations", got)
	}
}
