package bridge

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// composegenTestConfig covers the selection matrix: opted-in app with
// overrides, plain opted-in app, default-off app (bridge is strictly
// opt-in), explicitly disabled app, disabled service, default-off infra,
// and opted-in infra.
func composegenTestConfig() *config.DweConfig {
	return &config.DweConfig{
		Project: config.ProjectConfig{Name: "shop", Prefix: "acme"},
		Services: map[string]config.ServiceConfig{
			"admin": {Type: config.ServiceTypeApp, Container: "app-admin", Enabled: true,
				Dir: "./services/admin", DirInternal: "/srv/admin",
				Bridge: config.ServiceBridgeConfig{Enabled: new(true), ShimPath: "/opt/dwe/bin/dwe", OnUnreachable: config.BridgeOnUnreachableWarn}},
			"main": {Type: config.ServiceTypeApp, Container: "app-main", Enabled: true,
				Dir: "./services/main", DirInternal: "/workspace",
				Bridge: config.ServiceBridgeConfig{Enabled: new(true)}},
			"plain": {Type: config.ServiceTypeApp, Container: "app-plain", Enabled: true,
				Dir: "./services/plain", DirInternal: "/srv/plain"},
			"legacy": {Type: config.ServiceTypeApp, Container: "app-legacy", Enabled: true,
				Dir: "./services/legacy", DirInternal: "/srv/legacy",
				Bridge: config.ServiceBridgeConfig{Enabled: new(false)}},
			"paused": {Type: config.ServiceTypeApp, Container: "app-paused", Enabled: false,
				Dir: "./services/paused", DirInternal: "/srv/paused",
				Bridge: config.ServiceBridgeConfig{Enabled: new(true)}},
			"redis": {Type: config.ServiceTypeInfra, Container: "redis", Enabled: true},
			"queue": {Type: config.ServiceTypeInfra, Container: "queue", Enabled: true,
				Bridge: config.ServiceBridgeConfig{Enabled: new(true)}},
		},
	}
}

// fakeArchResolver maps fully-resolved container names (project prefix
// included) to fixed architectures, failing on anything unexpected.
func fakeArchResolver(t *testing.T) ArchResolver {
	t.Helper()
	return func(containerName string) (string, error) {
		switch containerName {
		case "acme-shop-app-admin":
			return "amd64", nil
		case "acme-shop-app-main":
			return "arm64", nil
		case "acme-shop-queue":
			return "arm64", nil
		default:
			return "", fmt.Errorf("unexpected container %q", containerName)
		}
	}
}

func TestBuildOverlaySpec_selectionAndFields(t *testing.T) {
	spec := BuildOverlaySpec("/host/proj", composegenTestConfig(), fakeArchResolver(t), nil)

	want := OverlaySpec{
		BridgeDir: "/host/proj/.dwe/bridge",
		Project:   "acme-shop",
		Services: []OverlayService{
			{Name: "app-admin", Key: "admin", ShimFile: "shim-linux-amd64", ShimPath: "/opt/dwe/bin/dwe",
				HostWorkspace: "/host/proj/services/admin", ContainerWorkspace: "/srv/admin",
				UnreachableWarn: true},
			{Name: "app-main", Key: "main", ShimFile: "shim-linux-arm64", ShimPath: "/usr/local/bin/dwe",
				HostWorkspace: "/host/proj/services/main", ContainerWorkspace: "/workspace"},
			{Name: "queue", Key: "queue", ShimFile: "shim-linux-arm64", ShimPath: "/usr/local/bin/dwe"},
		},
	}
	if !reflect.DeepEqual(spec, want) {
		t.Errorf("BuildOverlaySpec mismatch:\ngot:  %+v\nwant: %+v", spec, want)
	}
}

func TestBuildOverlaySpec_archFallbackOnResolverError(t *testing.T) {
	var warnings []string
	logf := func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}
	failing := func(string) (string, error) { return "", errors.New("docker daemon unreachable") }

	spec := BuildOverlaySpec("/host/proj", composegenTestConfig(), failing, logf)

	wantShim := "shim-linux-" + hostShimArch()
	for _, svc := range spec.Services {
		if svc.ShimFile != wantShim {
			t.Errorf("service %s: ShimFile = %q, want host fallback %q", svc.Name, svc.ShimFile, wantShim)
		}
	}
	if len(warnings) != len(spec.Services) {
		t.Fatalf("warnings = %d, want one per service (%d): %v", len(warnings), len(spec.Services), warnings)
	}
	for _, w := range warnings {
		if !strings.Contains(w, "falling back to host arch") {
			t.Errorf("warning %q does not mention the host-arch fallback", w)
		}
	}
}

func TestBuildOverlaySpec_missingContainerSilentFallback(t *testing.T) {
	var buf bytes.Buffer
	trace.Configure(&buf, trace.LevelVerbose)
	t.Cleanup(func() { trace.Configure(nil, trace.LevelOff) })

	var notes []string
	logf := func(format string, args ...any) {
		notes = append(notes, fmt.Sprintf(format, args...))
	}
	// The docker CLI's first-deploy shape: nothing created yet.
	missing := func(name string) (string, error) {
		return "", fmt.Errorf("Error response from daemon: No such container: %s", name)
	}

	spec := BuildOverlaySpec("/host/proj", composegenTestConfig(), missing, logf)

	wantShim := "shim-linux-" + hostShimArch()
	for _, svc := range spec.Services {
		if svc.ShimFile != wantShim {
			t.Errorf("service %s: ShimFile = %q, want host fallback %q", svc.Name, svc.ShimFile, wantShim)
		}
	}
	// A missing container is the expected fresh-start state: no user-facing
	// warning, only a -v decision line per service.
	if len(notes) != 0 {
		t.Errorf("missing container must not warn, got %v", notes)
	}
	if got := buf.String(); strings.Count(got, "has no container yet") != len(spec.Services) {
		t.Errorf("want one -v decision line per service (%d), got trace:\n%s", len(spec.Services), got)
	}
}

func TestBuildOverlaySpec_composeProjectNameFromDockerYML(t *testing.T) {
	// docker.yml's project_name (here with an underscore) is the prefix
	// compose names containers with — the arch resolver must receive it, not
	// Project.FullName(); DWE_BRIDGE_PROJECT stays the FullName identity.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "workspace"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace", "docker.yml"),
		[]byte("project_name: acme_shop\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var seen []string
	resolver := func(containerName string) (string, error) {
		seen = append(seen, containerName)
		return "arm64", nil
	}

	spec := BuildOverlaySpec(dir, composegenTestConfig(), resolver, nil)

	want := []string{"acme_shop-app-admin", "acme_shop-app-main", "acme_shop-queue"}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("resolver saw %v, want %v", seen, want)
	}
	if spec.Project != "acme-shop" {
		t.Errorf("spec.Project = %q, want the FullName identity %q", spec.Project, "acme-shop")
	}
}

func TestBuildOverlaySpec_archFallbackOnUnknownArch(t *testing.T) {
	var warnings []string
	logf := func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}
	exotic := func(string) (string, error) { return "s390x", nil }

	spec := BuildOverlaySpec("/host/proj", composegenTestConfig(), exotic, logf)

	wantShim := "shim-linux-" + hostShimArch()
	for _, svc := range spec.Services {
		if svc.ShimFile != wantShim {
			t.Errorf("service %s: ShimFile = %q, want host fallback %q", svc.Name, svc.ShimFile, wantShim)
		}
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "has no shim") {
		t.Errorf("expected 'has no shim' warnings, got %v", warnings)
	}
}

func TestBuildOverlaySpec_nilResolverUsesHostArch(t *testing.T) {
	spec := BuildOverlaySpec("/host/proj", composegenTestConfig(), nil, nil)
	wantShim := "shim-linux-" + hostShimArch()
	for _, svc := range spec.Services {
		if svc.ShimFile != wantShim {
			t.Errorf("service %s: ShimFile = %q, want %q", svc.Name, svc.ShimFile, wantShim)
		}
	}
}

func TestRenderOverlay_golden(t *testing.T) {
	spec := BuildOverlaySpec("/host/proj", composegenTestConfig(), fakeArchResolver(t), nil)
	data, err := RenderOverlay(spec)
	if err != nil {
		t.Fatalf("RenderOverlay: %v", err)
	}

	goldenPath := filepath.Join("testdata", "compose_bridge_overlay.golden.yml")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("creating testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, data, 0o644); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
		t.Logf("updated golden: %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden %s: %v", goldenPath, err)
	}
	if string(data) != string(want) {
		t.Errorf("overlay mismatch:\ngot:\n%s\nwant:\n%s", data, want)
	}
}

func TestRegenerateOverlay_writeIfChangedAndDelete(t *testing.T) {
	dir := t.TempDir()
	cfg := composegenTestConfig()
	arch := fakeArchResolver(t)

	changed, err := RegenerateOverlay(dir, cfg, arch, nil)
	if err != nil {
		t.Fatalf("RegenerateOverlay: %v", err)
	}
	if !changed {
		t.Error("first regeneration: changed = false, want true")
	}
	path := OverlayPath(dir)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading overlay: %v", err)
	}
	if !strings.HasPrefix(string(data), overlayHeader) {
		t.Errorf("overlay does not start with the GENERATED header:\n%s", data)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat overlay: %v", err)
	}

	// Identical content → no rewrite.
	changed, err = RegenerateOverlay(dir, cfg, arch, nil)
	if err != nil {
		t.Fatalf("second RegenerateOverlay: %v", err)
	}
	if changed {
		t.Error("second regeneration: changed = true, want false")
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat overlay: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("unchanged content rewrote the overlay (mtime moved)")
	}

	// Nothing bridged → the file is deleted, not left stale in the chain.
	for name, svc := range cfg.Services {
		svc.Bridge.Enabled = new(false)
		cfg.Services[name] = svc
	}
	changed, err = RegenerateOverlay(dir, cfg, arch, nil)
	if err != nil {
		t.Fatalf("delete regeneration: %v", err)
	}
	if !changed {
		t.Error("delete regeneration: changed = false, want true")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("overlay still present after delete regeneration: %v", err)
	}

	// Still nothing bridged and no file → quiet no-op.
	changed, err = RegenerateOverlay(dir, cfg, arch, nil)
	if err != nil {
		t.Fatalf("no-op regeneration: %v", err)
	}
	if changed {
		t.Error("no-op regeneration: changed = true, want false")
	}
}

func TestRegenerateOverlay_toggleRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := composegenTestConfig()
	arch := fakeArchResolver(t)

	mustRegenerate := func(wantChanged bool) string {
		t.Helper()
		changed, err := RegenerateOverlay(dir, cfg, arch, nil)
		if err != nil {
			t.Fatalf("RegenerateOverlay: %v", err)
		}
		if changed != wantChanged {
			t.Errorf("changed = %v, want %v", changed, wantChanged)
		}
		data, err := os.ReadFile(OverlayPath(dir))
		if err != nil {
			t.Fatalf("reading overlay: %v", err)
		}
		return string(data)
	}

	content := mustRegenerate(true)
	if !strings.Contains(content, "app-admin:") || !strings.Contains(content, "app-main:") {
		t.Fatalf("initial overlay missing bridged services:\n%s", content)
	}

	// Disable → regenerated without the service; nothing stale remains.
	svc := cfg.Services["admin"]
	svc.Enabled = false
	cfg.Services["admin"] = svc
	content = mustRegenerate(true)
	if strings.Contains(content, "app-admin:") {
		t.Errorf("disabled service still in overlay:\n%s", content)
	}
	if !strings.Contains(content, "app-main:") {
		t.Errorf("unrelated service dropped from overlay:\n%s", content)
	}

	// Re-enable → back in.
	svc.Enabled = true
	cfg.Services["admin"] = svc
	content = mustRegenerate(true)
	if !strings.Contains(content, "app-admin:") {
		t.Errorf("re-enabled service missing from overlay:\n%s", content)
	}
}

func TestRegenerateOverlay_skipsServiceWithoutContainer(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.DweConfig{
		Project: config.ProjectConfig{Name: "shop"},
		Services: map[string]config.ServiceConfig{
			"main": {Type: config.ServiceTypeApp, Enabled: true, Dir: "./services/main",
				Bridge: config.ServiceBridgeConfig{Enabled: new(true)}},
		},
	}
	var warnings []string
	logf := func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}

	changed, err := RegenerateOverlay(dir, cfg, nil, logf)
	if err != nil {
		t.Fatalf("RegenerateOverlay: %v", err)
	}
	if changed {
		t.Error("changed = true, want false (no renderable service)")
	}
	if _, err := os.Stat(OverlayPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("overlay written for container-less service: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "no container name") {
		t.Errorf("expected a no-container warning, got %v", warnings)
	}
}

func TestDockerArchResolver_twoStepInspect(t *testing.T) {
	var calls [][]string
	run := func(name string, args ...string) (string, error) {
		calls = append(calls, append([]string{name}, args...))
		switch len(calls) {
		case 1:
			return "sha256:abc123", nil
		case 2:
			return "arm64", nil
		default:
			return "", errors.New("unexpected call")
		}
	}

	arch, err := DockerArchResolver(run, "docker")("acme-shop-app-main")
	if err != nil {
		t.Fatalf("DockerArchResolver: %v", err)
	}
	if arch != "arm64" {
		t.Errorf("arch = %q, want arm64", arch)
	}
	wantCalls := [][]string{
		{"docker", "container", "inspect", "--format", "{{.Image}}", "acme-shop-app-main"},
		{"docker", "image", "inspect", "--format", "{{.Architecture}}", "sha256:abc123"},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("calls mismatch:\ngot:  %v\nwant: %v", calls, wantCalls)
	}
}

func TestDockerArchResolver_containerInspectError(t *testing.T) {
	run := func(string, ...string) (string, error) {
		return "", errors.New("no such container")
	}
	if _, err := DockerArchResolver(run, "docker")("missing"); err == nil {
		t.Fatal("expected the container inspect error to propagate")
	}
}

func TestNormalizeShimArch(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"amd64", "amd64", true},
		{"x86_64", "amd64", true},
		{"arm64", "arm64", true},
		{"aarch64", "arm64", true},
		{" arm64 ", "arm64", true},
		{"s390x", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := normalizeShimArch(tt.in)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("normalizeShimArch(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}
