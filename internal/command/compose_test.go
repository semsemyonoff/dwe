package command

import (
	"io"
	"testing"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

func makeComposeCfg(base string, overlays map[string]string, tools config.ToolsConfig, services map[string]config.ServiceConfig) *config.DevboxConfig {
	return &config.DevboxConfig{
		Compose: config.ComposeConfig{
			Base:     base,
			Overlays: overlays,
		},
		Tools:    tools,
		Services: services,
	}
}

func TestBuildComposeFileList_baseOnly(t *testing.T) {
	cfg := makeComposeCfg("compose.yaml", nil, config.ToolsConfig{}, nil)
	got := buildComposeFileList(cfg)
	if len(got) != 1 || got[0] != "compose.yaml" {
		t.Errorf("got %v, want [compose.yaml]", got)
	}
}

func TestBuildComposeFileList_noBase(t *testing.T) {
	cfg := makeComposeCfg("", map[string]string{"adminer": "compose/tools/adminer.yml"}, config.ToolsConfig{
		Adminer: config.ToolConfig{Enabled: true},
	}, nil)
	got := buildComposeFileList(cfg)
	// No base — only adminer overlay
	if len(got) != 1 || got[0] != "compose/tools/adminer.yml" {
		t.Errorf("got %v, want [compose/tools/adminer.yml]", got)
	}
}

func TestBuildComposeFileList_oneToolEnabled(t *testing.T) {
	cfg := makeComposeCfg("compose.yaml", map[string]string{
		"adminer":       "compose/tools/adminer.yml",
		"redis_insight": "compose/tools/redis_insight.yml",
		"mailpit":       "compose/tools/mailpit.yml",
	}, config.ToolsConfig{
		Adminer:      config.ToolConfig{Enabled: false},
		RedisInsight: config.ToolConfig{Enabled: true},
		Mailpit:      config.ToolConfig{Enabled: false},
	}, nil)
	got := buildComposeFileList(cfg)
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 entries", got)
	}
	if got[0] != "compose.yaml" {
		t.Errorf("got[0] = %q, want compose.yaml", got[0])
	}
	if got[1] != "compose/tools/redis_insight.yml" {
		t.Errorf("got[1] = %q, want compose/tools/redis_insight.yml", got[1])
	}
}

func TestBuildComposeFileList_multipleToolsEnabled(t *testing.T) {
	cfg := makeComposeCfg("compose.yaml", map[string]string{
		"adminer":       "compose/tools/adminer.yml",
		"redis_insight": "compose/tools/redis_insight.yml",
		"mailpit":       "compose/tools/mailpit.yml",
	}, config.ToolsConfig{
		Adminer:      config.ToolConfig{Enabled: true},
		RedisInsight: config.ToolConfig{Enabled: true},
		Mailpit:      config.ToolConfig{Enabled: false},
	}, nil)
	got := buildComposeFileList(cfg)
	// base + adminer + redis_insight (sorted: adminer < redis_insight < mailpit)
	if len(got) != 3 {
		t.Fatalf("got %v, want 3 entries", got)
	}
	if got[0] != "compose.yaml" {
		t.Errorf("got[0] = %q, want compose.yaml", got[0])
	}
	if got[1] != "compose/tools/adminer.yml" {
		t.Errorf("got[1] = %q, want adminer.yml", got[1])
	}
	if got[2] != "compose/tools/redis_insight.yml" {
		t.Errorf("got[2] = %q, want redis_insight.yml", got[2])
	}
}

func TestBuildComposeFileList_disabledExcluded(t *testing.T) {
	cfg := makeComposeCfg("compose.yaml", map[string]string{
		"adminer": "compose/tools/adminer.yml",
		"mailpit": "compose/tools/mailpit.yml",
	}, config.ToolsConfig{
		Adminer: config.ToolConfig{Enabled: false},
		Mailpit: config.ToolConfig{Enabled: false},
	}, nil)
	got := buildComposeFileList(cfg)
	if len(got) != 1 || got[0] != "compose.yaml" {
		t.Errorf("got %v, want only base", got)
	}
}

func TestBuildComposeFileList_serviceOverlayEnabled(t *testing.T) {
	cfg := makeComposeCfg("compose.yaml", nil, config.ToolsConfig{}, map[string]config.ServiceConfig{
		"main-debug": {
			Container: "app-main-debug",
			Enabled:   true,
			Compose:   []string{"compose/services/main/debug.yml"},
		},
	})
	got := buildComposeFileList(cfg)
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 entries", got)
	}
	if got[1] != "compose/services/main/debug.yml" {
		t.Errorf("got[1] = %q, want debug.yml", got[1])
	}
}

func TestBuildComposeFileList_serviceOverlayDisabled(t *testing.T) {
	cfg := makeComposeCfg("compose.yaml", nil, config.ToolsConfig{}, map[string]config.ServiceConfig{
		"main-debug": {
			Container: "app-main-debug",
			Enabled:   false,
			Compose:   []string{"compose/services/main/debug.yml"},
		},
	})
	got := buildComposeFileList(cfg)
	if len(got) != 1 {
		t.Errorf("got %v, want only base (service disabled)", got)
	}
}

func TestBuildComposeFileList_unknownOverlaySkipped(t *testing.T) {
	cfg := makeComposeCfg("compose.yaml", map[string]string{
		"unknown_tool": "compose/tools/unknown.yml",
	}, config.ToolsConfig{}, nil)
	got := buildComposeFileList(cfg)
	// unknown keys are not enabled → only base
	if len(got) != 1 || got[0] != "compose.yaml" {
		t.Errorf("got %v, want only base for unknown overlay key", got)
	}
}

// discardWriter returns a render.Writer that discards all output (useful in tests).
func discardWriter() *render.Writer {
	return render.NewWriter(io.Discard)
}

func TestWaitContainersHealthy_allHealthy(t *testing.T) {
	getHealth := func(id string) (string, error) { return "healthy", nil }
	err := waitContainersHealthy([]string{"c1", "c2"}, getHealth, 3, time.Millisecond, discardWriter())
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestWaitContainersHealthy_unhealthyReturnsError(t *testing.T) {
	getHealth := func(id string) (string, error) { return "unhealthy", nil }
	err := waitContainersHealthy([]string{"c1"}, getHealth, 3, time.Millisecond, discardWriter())
	if err == nil {
		t.Error("expected error for unhealthy container, got nil")
	}
}

func TestWaitContainersHealthy_noHealthcheckSkipped(t *testing.T) {
	getHealth := func(id string) (string, error) { return "none", nil }
	err := waitContainersHealthy([]string{"c1", "c2"}, getHealth, 3, time.Millisecond, discardWriter())
	if err != nil {
		t.Errorf("expected nil for no-healthcheck containers, got %v", err)
	}
}

func TestWaitContainersHealthy_startingThenHealthy(t *testing.T) {
	calls := 0
	getHealth := func(id string) (string, error) {
		calls++
		if calls < 3 {
			return "starting", nil
		}
		return "healthy", nil
	}
	err := waitContainersHealthy([]string{"c1"}, getHealth, 5, time.Millisecond, discardWriter())
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if calls < 3 {
		t.Errorf("expected at least 3 calls, got %d", calls)
	}
}

func TestWaitContainersHealthy_timeout(t *testing.T) {
	getHealth := func(id string) (string, error) { return "starting", nil }
	err := waitContainersHealthy([]string{"c1"}, getHealth, 2, time.Millisecond, discardWriter())
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

func TestWaitContainersHealthy_mixedNoHealthcheckAndHealthy(t *testing.T) {
	getHealth := func(id string) (string, error) {
		if id == "c1" {
			return "none", nil
		}
		return "healthy", nil
	}
	err := waitContainersHealthy([]string{"c1", "c2"}, getHealth, 3, time.Millisecond, discardWriter())
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}
