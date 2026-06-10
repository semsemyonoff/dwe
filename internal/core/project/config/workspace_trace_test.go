package config

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// TestLoadConfig_DebugSummary verifies the config-load summary slog.Debug
// record reaches the trace sink when the trace slog handler is installed at
// Debug, and stays silent (dropped by the handler routing) otherwise.
func TestLoadConfig_DebugSummary(t *testing.T) {
	workspacePath := writeLayeredFixture(t, sampleWorkspaceYML, "", "")

	t.Run("debug surfaces the summary", func(t *testing.T) {
		var buf strings.Builder
		trace.Configure(&buf, trace.LevelDebug)
		prev := slog.Default()
		slog.SetDefault(slog.New(trace.NewSlogHandler()))
		t.Cleanup(func() {
			slog.SetDefault(prev)
			trace.Configure(nil, trace.LevelOff)
		})

		if _, err := LoadConfig(workspacePath); err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		out := buf.String()
		for _, want := range []string{"config loaded", "services=", "enabled=", "layers="} {
			if !strings.Contains(out, want) {
				t.Errorf("config summary %q missing %q", out, want)
			}
		}
	})

	t.Run("silent when handler not installed", func(t *testing.T) {
		// At Off/Verbose the trace slog handler is NOT installed (root only
		// installs it at Debug), so the Debug record never reaches the trace
		// sink — it goes to Go's default handler, which drops Debug records.
		var buf strings.Builder
		trace.Configure(&buf, trace.LevelVerbose)
		t.Cleanup(func() { trace.Configure(nil, trace.LevelOff) })

		if _, err := LoadConfig(workspacePath); err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if strings.Contains(buf.String(), "config loaded") {
			t.Errorf("config summary should not reach the trace sink without the handler, got %q", buf.String())
		}
	})
}
