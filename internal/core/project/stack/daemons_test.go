package stack

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/statusview"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

func TestParseDaemonRows_ModernLabelsShape(t *testing.T) {
	in := strings.NewReader(`{"Names":"proj-php_queue_default","Labels":{"dwe.project":"proj","dwe.daemon.id":"services.main.queue","dwe.daemon.params":"{\"name\":\"default\"}"},"CreatedAt":"2026-05-21 12:00:00 +0000 UTC"}
{"Names":"proj-php_queue_emails","Labels":{"dwe.project":"proj","dwe.daemon.id":"services.main.queue","dwe.daemon.params":"{\"name\":\"emails\"}"},"CreatedAt":"2026-05-21 12:00:00 +0000 UTC"}`)
	rows, errs := parseDaemonRows(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].ID != "services.main.queue" || rows[1].ID != "services.main.queue" {
		t.Errorf("ID mismatch: %+v", rows)
	}
	if rows[0].Params != "name=default" || rows[1].Params != "name=emails" {
		t.Errorf("Params mismatch: %+v", rows)
	}
	if rows[0].Container != "proj-php_queue_default" {
		t.Errorf("container mismatch: %q", rows[0].Container)
	}
}

func TestParseDaemonRows_LegacyLabelsString(t *testing.T) {
	// Older docker emits Labels as a comma-separated string. The parser
	// tolerates both shapes so future docker version changes don't silently
	// break completion / status.
	in := strings.NewReader(`{"Names":"proj-foo","Labels":"dwe.project=proj,dwe.daemon.id=svc.foo,dwe.daemon.params={\"k\":\"v\"}"}`)
	rows, errs := parseDaemonRows(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].ID != "svc.foo" {
		t.Errorf("ID mismatch: %q", rows[0].ID)
	}
}

func TestParseDaemonRows_LegacyLabelsStringMultiParam(t *testing.T) {
	// Legacy string with a multi-param JSON value that contains commas.
	// The depth-aware parser must not split inside the JSON object.
	in := strings.NewReader(`{"Names":"proj-foo","Labels":"dwe.project=proj,dwe.daemon.id=svc.foo,dwe.daemon.params={\"name\":\"default\",\"queue\":\"emails\"}"}`)
	rows, errs := parseDaemonRows(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].ID != "svc.foo" {
		t.Errorf("ID mismatch: %q", rows[0].ID)
	}
	// prettyParams should render both keys sorted.
	if rows[0].Params != "name=default, queue=emails" {
		t.Errorf("Params mismatch: %q", rows[0].Params)
	}
}

func TestParseDaemonRows_LegacyLabelsStringSpecialCharsInValue(t *testing.T) {
	// A param value that contains '}' and ',' inside a JSON string must not
	// confuse the depth/split logic.
	in := strings.NewReader(`{"Names":"proj-foo","Labels":"dwe.project=proj,dwe.daemon.id=svc.foo,dwe.daemon.params={\"name\":\"foo},bar\",\"queue\":\"emails\"}"}`)
	rows, errs := parseDaemonRows(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].ID != "svc.foo" {
		t.Errorf("ID mismatch: %q", rows[0].ID)
	}
	if rows[0].Params != "name=foo},bar, queue=emails" {
		t.Errorf("Params mismatch: %q", rows[0].Params)
	}
}

func TestParseDaemonRows_SkipsContainerWithoutDaemonID(t *testing.T) {
	in := strings.NewReader(`{"Names":"unmanaged","Labels":{"foo":"bar"}}`)
	rows, _ := parseDaemonRows(in)
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
}

func TestParseDaemonRows_InvalidJSONLineYieldsError(t *testing.T) {
	in := strings.NewReader("{not json}\n{\"Names\":\"ok\",\"Labels\":{\"dwe.daemon.id\":\"a\"}}\n")
	rows, errs := parseDaemonRows(in)
	if len(errs) != 1 {
		t.Fatalf("got %d errs, want 1", len(errs))
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
}

func TestParseDaemonRows_SanitisesControlChars(t *testing.T) {
	// Container created by an external actor with newline + ANSI in the
	// labels MUST not reach the renderer untouched.
	in := strings.NewReader("{\"Names\":\"bad\\u001b[31mname\",\"Labels\":{\"dwe.daemon.id\":\"i\\u0007d\"}}")
	rows, errs := parseDaemonRows(in)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d (errs=%v)", len(rows), errs)
	}
	if strings.ContainsAny(rows[0].Container, "\x1b\x07") {
		t.Errorf("control chars leaked through: %q", rows[0].Container)
	}
	if strings.ContainsAny(rows[0].ID, "\x1b\x07") {
		t.Errorf("control chars leaked through: %q", rows[0].ID)
	}
}

func TestRenderDaemons_EmptyHidesSection(t *testing.T) {
	body, errs := RenderDaemons(nil)
	if body != "" {
		t.Errorf("expected empty body, got %q", body)
	}
	if len(errs) != 0 {
		t.Errorf("expected no errs, got %v", errs)
	}
}

func TestRenderDaemons_TableContents(t *testing.T) {
	rows := []statusview.DaemonRow{
		{ID: "services.main.queue", Params: "name=default", Container: "proj-php_queue_default", Uptime: 5 * time.Minute},
	}
	body, _ := RenderDaemons(rows)
	if !strings.Contains(body, "Daemons") {
		t.Errorf("missing section title: %q", body)
	}
	if !strings.Contains(body, "services.main.queue") || !strings.Contains(body, "proj-php_queue_default") {
		t.Errorf("missing row content: %q", body)
	}
	if !strings.Contains(body, "5m0s") {
		t.Errorf("uptime missing: %q", body)
	}
}

func TestFormatUptime(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, ""},
		{500 * time.Millisecond, "<1s"},
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m30s"},
		{2*time.Hour + 15*time.Minute, "2h15m"},
		{49 * time.Hour, "2d1h"},
	}
	for _, c := range cases {
		got := formatUptime(c.d)
		if got != c.want {
			t.Errorf("formatUptime(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestCollectDaemons_ShellError(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Required: true},
		},
		map[string]testTool(nil),
		nil,
		nil,
	)
	cfg.Project.Name = "proj"
	orig := daemonsShellOutFn
	defer func() { daemonsShellOutFn = orig }()
	daemonsShellOutFn = func(_ context.Context, _ *docker.Compose, _ string) ([]byte, error) {
		return nil, errors.New("permission denied")
	}
	rows, errs := CollectDaemons(context.Background(), cfg, &config.DockerConfig{}, "")
	if rows != nil {
		t.Errorf("expected nil rows on error, got %v", rows)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "docker ps") {
		t.Errorf("expected 'docker ps' in error message, got: %v", errs[0])
	}
}

func TestCollectDaemons_NilCfg(t *testing.T) {
	rows, errs := CollectDaemons(context.Background(), nil, nil, "")
	if rows != nil || errs != nil {
		t.Errorf("nil cfg → expected (nil, nil), got (%v, %v)", rows, errs)
	}
}

func TestCollectDaemons_ShellSeam(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Required: true},
		},
		map[string]testTool(nil),
		nil,
		nil,
	)
	cfg.Project.Name = "proj"
	orig := daemonsShellOutFn
	defer func() { daemonsShellOutFn = orig }()
	daemonsShellOutFn = func(_ context.Context, _ *docker.Compose, _ string) ([]byte, error) {
		return []byte(`{"Names":"proj-php_queue_default","Labels":{"dwe.daemon.id":"services.main.queue","dwe.daemon.params":"{\"name\":\"default\"}"}}`), nil
	}
	rows, errs := CollectDaemons(context.Background(), cfg, &config.DockerConfig{}, "")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(rows) != 1 || rows[0].ID != "services.main.queue" {
		t.Fatalf("expected 1 row id=services.main.queue, got %+v", rows)
	}
}

func TestCollectDaemons_HonorsDockerYmlProjectName(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Required: true},
		},
		map[string]testTool(nil),
		nil,
		nil,
	)
	cfg.Project.Name = "proj" // FullName() == "proj"
	orig := daemonsShellOutFn
	defer func() { daemonsShellOutFn = orig }()
	var gotProject string
	daemonsShellOutFn = func(_ context.Context, _ *docker.Compose, projectFull string) ([]byte, error) {
		gotProject = projectFull
		return nil, nil
	}
	// A docker.yml project_name override must scope the daemon label filter,
	// not the dash-joined FullName, so daemons match compose-managed services.
	CollectDaemons(context.Background(), cfg, &config.DockerConfig{ProjectName: "dwe_proj"}, "")
	if gotProject != "dwe_proj" {
		t.Errorf("projectFull = %q, want %q (docker.yml project_name, not FullName 'proj')", gotProject, "dwe_proj")
	}
}
