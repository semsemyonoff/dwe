package envtest

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

func TestNewRunID(t *testing.T) {
	hexPattern := regexp.MustCompile(`^[0-9a-f]{6}$`)
	seen := make(map[string]bool)
	for i := range 50 {
		id, err := NewRunID()
		if err != nil {
			t.Fatalf("NewRunID: %v", err)
		}
		if !hexPattern.MatchString(id) {
			t.Fatalf("NewRunID() = %q, want 6 lowercase hex chars", id)
		}
		if seen[id] {
			t.Fatalf("NewRunID() produced duplicate %q across %d calls", id, i+1)
		}
		seen[id] = true
	}
}

func TestComposeProjectName(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.DweConfig
		scenario string
		runID    string
		want     string
	}{
		{
			name:     "name only",
			cfg:      &config.DweConfig{Project: config.ProjectConfig{Name: "myapp"}},
			scenario: "redis-off",
			runID:    "abc123",
			want:     "myapp-t-redis-off-abc123",
		},
		{
			name:     "prefix wins over name",
			cfg:      &config.DweConfig{Project: config.ProjectConfig{Name: "myapp", Prefix: "dev"}},
			scenario: "smoke",
			runID:    "def456",
			want:     "dev-t-smoke-def456",
		},
		{
			name:     "uppercase and disallowed chars normalised",
			cfg:      &config.DweConfig{Project: config.ProjectConfig{Name: "My App"}},
			scenario: "smoke",
			runID:    "abc123",
			want:     "my-app-t-smoke-abc123",
		},
		{
			name:     "dots and underscores",
			cfg:      &config.DweConfig{Project: config.ProjectConfig{Prefix: "my.proj_x"}},
			scenario: "smoke",
			runID:    "abc123",
			want:     "my-proj_x-t-smoke-abc123",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComposeProjectName(tt.cfg, tt.scenario, tt.runID)
			if got != tt.want {
				t.Errorf("ComposeProjectName() = %q, want %q", got, tt.want)
			}
			if !regexp.MustCompile(`^[a-z0-9_-]+$`).MatchString(got) {
				t.Errorf("ComposeProjectName() = %q contains chars outside [a-z0-9_-]", got)
			}
		})
	}
}

func TestPathHelpers(t *testing.T) {
	base := "/proj"
	if got, want := RunDir(base, "redis-off"), filepath.Join(base, ".dwe", "tests", "runs", "redis-off"); got != want {
		t.Errorf("RunDir() = %q, want %q", got, want)
	}
	if got, want := LockPath(base, "redis-off"), filepath.Join(base, ".dwe", "tests", "locks", "redis-off.lock"); got != want {
		t.Errorf("LockPath() = %q, want %q", got, want)
	}
	if got, want := ManifestPath(base, "redis-off", "abc123"), filepath.Join(base, ".dwe", "tests", "manifests", "redis-off-abc123.yml"); got != want {
		t.Errorf("ManifestPath() = %q, want %q", got, want)
	}
	if got, want := ManifestsDir(base), filepath.Join(base, ".dwe", "tests", "manifests"); got != want {
		t.Errorf("ManifestsDir() = %q, want %q", got, want)
	}
	if got, want := ReportsDir(base, "redis-off"), filepath.Join(base, ".dwe", "tests", "reports", "redis-off"); got != want {
		t.Errorf("ReportsDir() = %q, want %q", got, want)
	}
}

func TestScrubComposeEnv(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "leftover")
	t.Setenv("COMPOSE_FILE", "docker-compose.yml")
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	t.Setenv("DOCKER_CONTEXT", "desktop-linux")
	t.Setenv("SOME_OTHER_VAR", "keep-me")

	ScrubComposeEnv()

	for _, name := range []string{"COMPOSE_PROJECT_NAME", "COMPOSE_FILE"} {
		if v, ok := os.LookupEnv(name); ok {
			t.Errorf("ScrubComposeEnv() left %s=%q set", name, v)
		}
	}
	for _, name := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "SOME_OTHER_VAR"} {
		if _, ok := os.LookupEnv(name); !ok {
			t.Errorf("ScrubComposeEnv() removed unrelated var %s", name)
		}
	}
}
