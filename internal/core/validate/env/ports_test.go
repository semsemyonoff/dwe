package env

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func TestParsePortsField(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []int
	}{
		{"empty", "", nil},
		{"exposed only", "5432/tcp", nil},
		{"single ipv4", "0.0.0.0:5432->5432/tcp", []int{5432}},
		{"localhost", "127.0.0.1:5432->5432/tcp", []int{5432}},
		{"ipv6 wildcard", ":::5432->5432/tcp", []int{5432}},
		{
			"dual stack dedupes",
			"0.0.0.0:5432->5432/tcp, :::5432->5432/tcp",
			[]int{5432},
		},
		{
			"mixed published + exposed",
			"5432/tcp, 0.0.0.0:8080->80/tcp",
			[]int{8080},
		},
		{
			"multiple distinct",
			"0.0.0.0:5432->5432/tcp, 0.0.0.0:6379->6379/tcp",
			[]int{5432, 6379},
		},
		{"junk port", "0.0.0.0:abc->80/tcp", nil},
		{"out of range", "0.0.0.0:99999->80/tcp", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePortsField(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parsePortsField(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParsePortBindings(t *testing.T) {
	// Two containers from two different compose projects on overlapping ports.
	ndjson := `{"Names":"ours-postgres-1","Ports":"0.0.0.0:5432->5432/tcp, :::5432->5432/tcp","Labels":{"com.docker.compose.project":"ours"}}
{"Names":"other-redis","Ports":"0.0.0.0:6379->6379/tcp","Labels":{"com.docker.compose.project":"other-proj"}}
{"Names":"orphan","Ports":"0.0.0.0:9000->9000/tcp","Labels":{}}
{"Names":"no-ports","Ports":"","Labels":{}}
`
	got := parsePortBindings([]byte(ndjson))

	if owners, ok := got[5432]; !ok || len(owners) != 1 || owners[0].Container != "ours-postgres-1" || owners[0].ComposeProject != "ours" {
		t.Errorf("5432 mapping: %+v", owners)
	}
	if owners, ok := got[6379]; !ok || len(owners) != 1 || owners[0].ComposeProject != "other-proj" {
		t.Errorf("6379 mapping: %+v", owners)
	}
	if owners, ok := got[9000]; !ok || len(owners) != 1 || owners[0].ComposeProject != "" {
		t.Errorf("9000 mapping: %+v", owners)
	}
	if _, ok := got[0]; ok {
		t.Errorf("empty-ports container must not produce a 0-port entry")
	}
}

func TestParsePortBindings_LegacyLabelFormat(t *testing.T) {
	// Older docker emits Labels as a "k=v,k=v" string.
	ndjson := `{"Names":"c1","Ports":"0.0.0.0:80->80/tcp","Labels":"com.docker.compose.project=legacy,foo=bar"}` + "\n"
	got := parsePortBindings([]byte(ndjson))
	if owners := got[80]; len(owners) != 1 || owners[0].ComposeProject != "legacy" {
		t.Errorf("legacy label format not decoded: %+v", owners)
	}
}

func TestCollectDeclaredPorts(t *testing.T) {
	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"web": {
				Enabled: true,
				Ports:   map[string]int{"http": 8080, "metrics": 9090},
			},
			"db": {
				Enabled: true,
				Ports:   map[string]int{"sql": 5432},
			},
			"disabled-svc": {
				Enabled: false,
				Ports:   map[string]int{"sql": 5433},
			},
			"bad-ports": {
				Enabled: true,
				Ports:   map[string]int{"zero": 0, "huge": 70000, "ok": 1234},
			},
		},
	}
	got := collectDeclaredPorts(cfg)
	// Expect sorted by (service, portName); disabled and out-of-range filtered out.
	want := []declaredPort{
		{Service: "bad-ports", PortName: "ok", HostPort: 1234},
		{Service: "db", PortName: "sql", HostPort: 5432},
		{Service: "web", PortName: "http", HostPort: 8080},
		{Service: "web", PortName: "metrics", HostPort: 9090},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("collectDeclaredPorts = %v, want %v", got, want)
	}
}

func TestCollectDeclaredPorts_NilCfg(t *testing.T) {
	if got := collectDeclaredPorts(nil); got != nil {
		t.Errorf("nil cfg should produce nil slice, got %v", got)
	}
}

func TestResolveComposeProject_NoDockerYml_FallsBackToBasename(t *testing.T) {
	dir := t.TempDir()
	got := resolveComposeProject(dir, &config.DweConfig{})
	want := strings.ToLower(filepath.Base(dir))
	if got != want {
		t.Errorf("resolveComposeProject = %q, want %q (dir basename fallback)", got, want)
	}
}

func TestClassifyPort_OursReused(t *testing.T) {
	bindings := map[int][]portOwner{
		5432: {{Container: "ours-db-1", ComposeProject: "ours"}},
	}
	got := classifyPortForConflict(declaredPort{Service: "db", PortName: "sql", HostPort: 5432}, bindings, "ours", false)
	if got != "" {
		t.Errorf("our own container should not be a conflict, got: %q", got)
	}
}

func TestClassifyPort_ForeignCompose(t *testing.T) {
	bindings := map[int][]portOwner{
		5432: {{Container: "rival-db-1", ComposeProject: "rival-proj"}},
	}
	got := classifyPortForConflict(declaredPort{Service: "db", PortName: "sql", HostPort: 5432}, bindings, "ours", false)
	if !strings.Contains(got, "rival-db-1") || !strings.Contains(got, "rival-proj") {
		t.Errorf("foreign container message missing details: %q", got)
	}
}

func TestClassifyPort_ForeignNoLabel(t *testing.T) {
	bindings := map[int][]portOwner{
		5432: {{Container: "raw-container", ComposeProject: ""}},
	}
	got := classifyPortForConflict(declaredPort{Service: "db", PortName: "sql", HostPort: 5432}, bindings, "ours", false)
	if !strings.Contains(got, "raw-container") {
		t.Errorf("expected container name in message: %q", got)
	}
	if strings.Contains(got, "compose project") {
		t.Errorf("must not mention compose project when label is empty: %q", got)
	}
}

func TestClassifyPort_FreeViaListen(t *testing.T) {
	orig := portListenFn
	t.Cleanup(func() { portListenFn = orig })
	portListenFn = func(port int) error { return nil } // pretend free
	got := classifyPortForConflict(declaredPort{Service: "db", PortName: "sql", HostPort: 5432}, map[int][]portOwner{}, "ours", false)
	if got != "" {
		t.Errorf("free port should produce no conflict, got %q", got)
	}
}

func TestClassifyPort_BusyNonDocker(t *testing.T) {
	orig := portListenFn
	t.Cleanup(func() { portListenFn = orig })
	portListenFn = func(port int) error { return errors.New("listen tcp :5432: bind: address already in use") }
	got := classifyPortForConflict(declaredPort{Service: "db", PortName: "sql", HostPort: 5432}, map[int][]portOwner{}, "ours", false)
	if !strings.Contains(got, "in use") {
		t.Errorf("expected 'in use' in non-docker conflict, got %q", got)
	}
}

func TestPortsFreeValidator_StopStageSkips(t *testing.T) {
	// Use a free port — even so, stop stage should produce zero diagnostics.
	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"web": {Enabled: true, Ports: map[string]int{"http": freeLocalPort(t)}},
		},
	}
	v := &portsFreeValidator{cfg: cfg}
	diags := v.Run(validate.Context{Stage: "stop", Cfg: cfg})
	if len(diags) != 0 {
		t.Errorf("stop stage must skip; got %d diags", len(diags))
	}
}

func TestPortsFreeValidator_NoServicesSkips(t *testing.T) {
	v := &portsFreeValidator{cfg: &config.DweConfig{}}
	diags := v.Run(validate.Context{Stage: "deploy"})
	if len(diags) != 0 {
		t.Errorf("no declared ports should produce zero diagnostics; got %d", len(diags))
	}
}

func TestPortsFreeValidator_DockerMissingSkips(t *testing.T) {
	dir := t.TempDir()
	withIsolatedPath(t, dir) // empty PATH — docker is not found
	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"web": {Enabled: true, Ports: map[string]int{"http": 8080}},
		},
	}
	v := &portsFreeValidator{cfg: cfg}
	diags := v.Run(validate.Context{Stage: "deploy", Cfg: cfg})
	if len(diags) != 0 {
		t.Errorf("missing docker bin should skip (docker_bin reports); got %d diags", len(diags))
	}
}

func TestPortsFreeValidator_ConflictReported(t *testing.T) {
	// Stub docker on PATH and stub the PS output via the seam.
	dir := t.TempDir()
	writeStubBinary(t, dir, "docker", 0, "")
	withIsolatedPath(t, dir)

	origOut := dockerPSOutFn
	origListen := portListenFn
	t.Cleanup(func() {
		dockerPSOutFn = origOut
		portListenFn = origListen
	})
	dockerPSOutFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`{"Names":"rival","Ports":"0.0.0.0:5432->5432/tcp","Labels":{"com.docker.compose.project":"rival"}}` + "\n"), nil
	}
	portListenFn = func(int) error { return nil }

	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"db": {Enabled: true, Ports: map[string]int{"sql": 5432}},
		},
	}
	v := &portsFreeValidator{cfg: cfg}
	diags := v.Run(validate.Context{Stage: "deploy", Cfg: cfg})
	if len(diags) != 1 || diags[0].Severity != validate.SeverityError {
		t.Fatalf("want 1 error diag, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "rival") {
		t.Errorf("conflict should name foreign container: %q", diags[0].Message)
	}
}

func TestPortsFreeValidator_AllFreeReportsOK(t *testing.T) {
	dir := t.TempDir()
	writeStubBinary(t, dir, "docker", 0, "")
	withIsolatedPath(t, dir)
	origOut := dockerPSOutFn
	origListen := portListenFn
	t.Cleanup(func() {
		dockerPSOutFn = origOut
		portListenFn = origListen
	})
	dockerPSOutFn = func(_ context.Context, _ string) ([]byte, error) { return nil, nil }
	portListenFn = func(int) error { return nil }

	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"web": {Enabled: true, Ports: map[string]int{"http": 8080}},
		},
	}
	v := &portsFreeValidator{cfg: cfg}
	diags := v.Run(validate.Context{Stage: "deploy", Cfg: cfg})
	if len(diags) != 1 || diags[0].Severity != validate.SeverityOK {
		t.Fatalf("want 1 OK diag, got %+v", diags)
	}
}

// freeLocalPort returns a TCP port that was free at the moment of the call.
// Racy by nature — only used in tests that immediately consume it.
func freeLocalPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func TestCollectPortConflicts_DeclaredPortFree(t *testing.T) {
	dir := t.TempDir()
	writeStubBinary(t, dir, "docker", 0, "")
	withIsolatedPath(t, dir)
	origOut := dockerPSOutFn
	origListen := portListenFn
	t.Cleanup(func() {
		dockerPSOutFn = origOut
		portListenFn = origListen
	})
	dockerPSOutFn = func(_ context.Context, _ string) ([]byte, error) { return nil, nil }
	portListenFn = func(int) error { return nil }

	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"web": {Enabled: true, Ports: map[string]int{"http": 8080}},
		},
	}
	conflicts, err := CollectPortConflicts(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("CollectPortConflicts: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("free port should produce zero conflicts, got %d", len(conflicts))
	}
}

func TestCollectPortConflicts_ForeignContainerConflict(t *testing.T) {
	dir := t.TempDir()
	writeStubBinary(t, dir, "docker", 0, "")
	withIsolatedPath(t, dir)
	origOut := dockerPSOutFn
	origListen := portListenFn
	t.Cleanup(func() {
		dockerPSOutFn = origOut
		portListenFn = origListen
	})
	dockerPSOutFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`{"Names":"rival-db-1","Ports":"0.0.0.0:5432->5432/tcp","Labels":{"com.docker.compose.project":"rival"}}` + "\n"), nil
	}
	portListenFn = func(int) error { return nil }

	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"db": {Enabled: true, Ports: map[string]int{"sql": 5432}},
		},
	}
	conflicts, err := CollectPortConflicts(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("CollectPortConflicts: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("want 1 conflict, got %d", len(conflicts))
	}
	pc := conflicts[0]
	if pc.Service != "db" || pc.PortName != "sql" || pc.RequestedPort != 5432 {
		t.Errorf("conflict metadata: want db/sql/5432, got %s/%s/%d", pc.Service, pc.PortName, pc.RequestedPort)
	}
	if !strings.Contains(pc.OccupiedBy, "rival-db-1") {
		t.Errorf("OccupiedBy must name the foreign container: %q", pc.OccupiedBy)
	}
}

func TestCollectPortConflicts_OwnComposeContainerNotConflict(t *testing.T) {
	// Create a docker.yml with project_name to ensure resolveComposeProject returns "ours".
	dir := t.TempDir()
	dockerCfgPath := filepath.Join(dir, "workspace", "docker.yml")
	if err := os.MkdirAll(filepath.Dir(dockerCfgPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dockerCfgPath, []byte("project_name: ours\n"), 0o644); err != nil {
		t.Fatalf("write docker.yml: %v", err)
	}

	writeStubBinary(t, dir, "docker", 0, "")
	withIsolatedPath(t, dir)
	origOut := dockerPSOutFn
	origListen := portListenFn
	t.Cleanup(func() {
		dockerPSOutFn = origOut
		portListenFn = origListen
	})
	dockerPSOutFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`{"Names":"ours-db-1","Ports":"0.0.0.0:5432->5432/tcp","Labels":{"com.docker.compose.project":"ours"}}` + "\n"), nil
	}
	portListenFn = func(int) error { return nil }

	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"db": {Enabled: true, Ports: map[string]int{"sql": 5432}},
		},
	}
	conflicts, err := CollectPortConflicts(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("CollectPortConflicts: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("own compose container should not be a conflict, got %d", len(conflicts))
	}
}

func TestCollectPortConflicts_DockerMissingReturnsNil(t *testing.T) {
	dir := t.TempDir()
	withIsolatedPath(t, dir) // empty PATH — docker is not found
	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"web": {Enabled: true, Ports: map[string]int{"http": 8080}},
		},
	}
	conflicts, err := CollectPortConflicts(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("CollectPortConflicts: %v", err)
	}
	if conflicts != nil {
		t.Errorf("missing docker bin should return nil conflicts, got %d", len(conflicts))
	}
}

func TestCollectPortConflicts_DockerPSFailedFallback(t *testing.T) {
	dir := t.TempDir()
	writeStubBinary(t, dir, "docker", 0, "")
	withIsolatedPath(t, dir)
	origOut := dockerPSOutFn
	origListen := portListenFn
	t.Cleanup(func() {
		dockerPSOutFn = origOut
		portListenFn = origListen
	})
	dockerPSOutFn = func(_ context.Context, _ string) ([]byte, error) {
		return nil, errors.New("docker daemon unreachable")
	}
	// Port is in use (non-docker process)
	portListenFn = func(port int) error {
		if port == 8080 {
			return errors.New("listen tcp :8080: bind: address already in use")
		}
		return nil
	}

	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"web": {Enabled: true, Ports: map[string]int{"http": 8080}},
		},
	}
	conflicts, err := CollectPortConflicts(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("CollectPortConflicts: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("want 1 conflict (from listen fallback), got %d", len(conflicts))
	}
	pc := conflicts[0]
	if pc.OccupiedBy != "unknown (docker ps failed)" {
		t.Errorf("fallback conflict should have sentinel OccupiedBy, got %q", pc.OccupiedBy)
	}
}

func TestCollectPortConflicts_NilCfgReturnsNil(t *testing.T) {
	conflicts, err := CollectPortConflicts(context.Background(), nil, "")
	if err != nil {
		t.Fatalf("CollectPortConflicts: %v", err)
	}
	if conflicts != nil {
		t.Errorf("nil cfg should return nil conflicts, got %d", len(conflicts))
	}
}

func TestCollectPortConflicts_NoDeclaratedPortsReturnsNil(t *testing.T) {
	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"web": {Enabled: false, Ports: map[string]int{"http": 8080}},
		},
	}
	conflicts, err := CollectPortConflicts(context.Background(), cfg, "")
	if err != nil {
		t.Fatalf("CollectPortConflicts: %v", err)
	}
	if conflicts != nil {
		t.Errorf("no declared ports should return nil conflicts, got %d", len(conflicts))
	}
}
