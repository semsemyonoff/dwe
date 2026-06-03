package prompt

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/promptcache"
)

// TestPromptCacheIntegration_RunningWriterToPromptReader verifies that a state
// written via promptcache.Write is visible to runFromDir as a stack icon.
func TestPromptCacheIntegration_RunningWriterToPromptReader(t *testing.T) {
	cases := []struct {
		name     string
		state    string
		wantIcon string
	}{
		{"running", promptcache.StateRunning, "●"},
		{"partial", promptcache.StatePartial, "◐"},
		{"stopped", promptcache.StateStopped, "○"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "workspace.yml"),
				"project:\n  name: testproject\n")

			if err := promptcache.Write(root, tc.state); err != nil {
				t.Fatalf("promptcache.Write: %v", err)
			}

			var buf bytes.Buffer
			if code := runFromDir(&buf, nil, root, false); code != 0 {
				t.Fatalf("runFromDir exit code = %d", code)
			}
			out := buf.String()
			if !strings.Contains(out, tc.wantIcon) {
				t.Errorf("output %q missing stack icon %q", out, tc.wantIcon)
			}
		})
	}
}

// TestPromptCacheIntegration_InvalidatedCache_RefreshHitsDockerPs verifies that
// when the cache file is absent and the docker stub returns container IDs, the
// running icon is rendered (refresh path).
func TestPromptCacheIntegration_InvalidatedCache_RefreshHitsDockerPs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "workspace.yml"),
		"project:\n  name: testproject\n")

	swapDockerPs(t, func(_ context.Context, _ string) ([]byte, error) {
		return []byte("abc123\n"), nil
	})

	// No cache present — refresh runs and writes "running".
	var buf bytes.Buffer
	if code := runFromDir(&buf, nil, root, false); code != 0 {
		t.Fatalf("runFromDir exit code = %d", code)
	}
	out := buf.String()
	if !strings.Contains(out, "●") {
		t.Errorf("expected running glyph after refresh; got %q", out)
	}
}
