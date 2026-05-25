// Command fake-linter is a test helper that mimics a real linter binary for
// the internal/validate/linters runtime tests. Behavior is selected via the
// FAKE_LINTER_MODE environment variable so tests can put one binary on PATH
// and exercise multiple code paths.
//
// Modes:
//   - clean        — exit 0, no output (success path).
//   - findings     — exit 1 with a JSON payload, models "linter found issues".
//   - crash        — exit 2 with a message on stderr; payload on stdout invalid.
//   - hang         — sleep until killed (used with a tight DefaultLinterTimeout).
//   - huge-output  — emit MANY bytes to stdout to exercise the boundedWriter cap.
//
// This binary is only built by tests (via `go build` into t.TempDir()); it is
// never shipped in the user-facing devbox binary.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	mode := os.Getenv("FAKE_LINTER_MODE")
	switch mode {
	case "", "clean":
		os.Exit(0)
	case "findings":
		fmt.Println(`[{"file":"a.sh","line":3,"level":"warning","code":2034,"message":"unused var"}]`)
		os.Exit(1)
	case "crash":
		fmt.Fprintln(os.Stderr, "fake-linter: internal failure")
		fmt.Println("not json")
		os.Exit(2)
	case "hang":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "huge-output":
		// 64 KB of x's — comfortably larger than the test cap we set.
		line := strings.Repeat("x", 1024) + "\n"
		for range 64 {
			fmt.Print(line) //nolint:forbidigo // test helper binary; not user-facing
		}
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "fake-linter: unknown FAKE_LINTER_MODE=%q\n", mode)
		os.Exit(99)
	}
}
