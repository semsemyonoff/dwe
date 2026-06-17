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
	"time"

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
				Ports:   map[string]config.ServicePortSpec{"http": {Port: 8080}, "metrics": {Port: 9090}},
			},
			"db": {
				Enabled: true,
				Ports:   map[string]config.ServicePortSpec{"sql": {Port: 5432}},
			},
			"disabled-svc": {
				Enabled: false,
				Ports:   map[string]config.ServicePortSpec{"sql": {Port: 5433}},
			},
			"bad-ports": {
				Enabled: true,
				Ports:   map[string]config.ServicePortSpec{"zero": {Port: 0}, "huge": {Port: 70000}, "ok": {Port: 1234}},
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
	// Degenerate case: no docker.yml AND no project.name → FullName() is empty,
	// so compose itself defaults to the directory basename and we match that.
	dir := t.TempDir()
	got := resolveComposeProject(dir, &config.DweConfig{})
	want := strings.ToLower(filepath.Base(dir))
	if got != want {
		t.Errorf("resolveComposeProject = %q, want %q (dir basename fallback)", got, want)
	}
}

func TestResolveComposeProject_NoDockerYml_FallsBackToFullName(t *testing.T) {
	// Realistic case: a project with a name but no docker.yml. buildCompose now
	// stamps -p "<prefix>-<name>" on every compose call, so the conflict check
	// must resolve to the same FullName — NOT the directory basename — or our own
	// prior-deploy containers get misflagged as foreign conflicts.
	dir := t.TempDir()
	cfg := &config.DweConfig{}
	cfg.Project.Prefix = "dwe"
	cfg.Project.Name = "laravel"
	if got, want := resolveComposeProject(dir, cfg), "dwe-laravel"; got != want {
		t.Errorf("resolveComposeProject = %q, want %q (FullName, not basename)", got, want)
	}
}

func TestClassifyPort_OursReused(t *testing.T) {
	bindings := map[int][]portOwner{
		5432: {{Container: "ours-db-1", ComposeProject: "ours"}},
	}
	got := classifyPortForConflict(declaredPort{Service: "db", PortName: "sql", HostPort: 5432}, bindings, "ours", false, freshBudget())
	if got != "" {
		t.Errorf("our own container should not be a conflict, got: %q", got)
	}
}

func TestClassifyPort_ForeignCompose(t *testing.T) {
	bindings := map[int][]portOwner{
		5432: {{Container: "rival-db-1", ComposeProject: "rival-proj"}},
	}
	got := classifyPortForConflict(declaredPort{Service: "db", PortName: "sql", HostPort: 5432}, bindings, "ours", false, freshBudget())
	if !strings.Contains(got, "rival-db-1") || !strings.Contains(got, "rival-proj") {
		t.Errorf("foreign container message missing details: %q", got)
	}
}

func TestClassifyPort_ForeignNoLabel(t *testing.T) {
	bindings := map[int][]portOwner{
		5432: {{Container: "raw-container", ComposeProject: ""}},
	}
	got := classifyPortForConflict(declaredPort{Service: "db", PortName: "sql", HostPort: 5432}, bindings, "ours", false, freshBudget())
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
	got := classifyPortForConflict(declaredPort{Service: "db", PortName: "sql", HostPort: 5432}, map[int][]portOwner{}, "ours", false, freshBudget())
	if got != "" {
		t.Errorf("free port should produce no conflict, got %q", got)
	}
}

func TestClassifyPort_BusyNonDocker(t *testing.T) {
	stubNoSleep(t)
	orig := portListenFn
	t.Cleanup(func() { portListenFn = orig })
	portListenFn = func(port int) error { return errors.New("listen tcp :5432: bind: address already in use") }
	got := classifyPortForConflict(declaredPort{Service: "db", PortName: "sql", HostPort: 5432}, map[int][]portOwner{}, "ours", false, freshBudget())
	if !strings.Contains(got, "in use") {
		t.Errorf("expected 'in use' in non-docker conflict, got %q", got)
	}
}

// freshBudget returns a fresh, fully-stocked retry budget pointer for a direct
// classifyPortForConflict call — mirroring how CollectPortConflicts seeds one
// budget per pass. Single-port tests get the full portReleaseRetries each.
func freshBudget() *int {
	n := portReleaseRetries
	return &n
}

// stubNoSleep replaces the EADDRINUSE retry backoff with a no-op so tests that
// drive the retry loop do not incur real wall-clock delay.
func stubNoSleep(t *testing.T) {
	t.Helper()
	orig := portReleaseSleep
	t.Cleanup(func() { portReleaseSleep = orig })
	portReleaseSleep = func(time.Duration) {}
}

// TestClassifyPort_TransientReleaseRetried reproduces the `dwe restart` race on
// Docker Desktop: docker ps shows no owner (our caddy was just `docker down`ed)
// but the host port forwarder has not yet released :80, so the first listen
// probe fails with EADDRINUSE before clearing. The retry loop must absorb this
// and report NO conflict.
func TestClassifyPort_TransientReleaseRetried(t *testing.T) {
	stubNoSleep(t)
	orig := portListenFn
	t.Cleanup(func() { portListenFn = orig })
	var calls int
	portListenFn = func(int) error {
		calls++
		if calls <= 2 { // busy for the first two probes, then released
			return errors.New("listen tcp :80: bind: address already in use")
		}
		return nil
	}
	got := classifyPortForConflict(declaredPort{Service: "caddy", PortName: "http", HostPort: 80}, map[int][]portOwner{}, "ours", false, freshBudget())
	if got != "" {
		t.Errorf("transient port release should clear after retry, got conflict %q (calls=%d)", got, calls)
	}
	if calls != 3 {
		t.Errorf("expected 3 listen probes (2 busy + 1 free), got %d", calls)
	}
}

// TestClassifyPort_PersistentBusyExhaustsRetries verifies a genuine foreign
// holder — busy on every probe — is still reported as a conflict after the
// retry budget is exhausted, with the full attempt count.
func TestClassifyPort_PersistentBusyExhaustsRetries(t *testing.T) {
	stubNoSleep(t)
	orig := portListenFn
	t.Cleanup(func() { portListenFn = orig })
	var calls int
	portListenFn = func(int) error {
		calls++
		return errors.New("listen tcp :5432: bind: address already in use")
	}
	got := classifyPortForConflict(declaredPort{Service: "db", PortName: "sql", HostPort: 5432}, map[int][]portOwner{}, "ours", false, freshBudget())
	if !strings.Contains(got, "in use") {
		t.Errorf("persistent busy port must still be reported, got %q", got)
	}
	if want := portReleaseRetries + 1; calls != want {
		t.Errorf("expected %d listen probes (1 initial + %d retries), got %d", want, portReleaseRetries, calls)
	}
}

// TestClassifyPort_SharedBudgetBoundsPass verifies that a retry budget shared
// across a pass is consumed once, not per-port: a first port that stays busy
// drains the whole budget, leaving a second busy port with a single probe (no
// retries). This is what bounds total added latency to portReleaseRetries
// sleeps regardless of how many ports are simultaneously busy.
func TestClassifyPort_SharedBudgetBoundsPass(t *testing.T) {
	stubNoSleep(t)
	orig := portListenFn
	t.Cleanup(func() { portListenFn = orig })
	probes := map[int]int{}
	portListenFn = func(port int) error {
		probes[port]++
		return errors.New("listen tcp: bind: address already in use")
	}
	budget := portReleaseRetries
	a := classifyPortForConflict(declaredPort{Service: "a", PortName: "p", HostPort: 1111}, map[int][]portOwner{}, "ours", false, &budget)
	b := classifyPortForConflict(declaredPort{Service: "b", PortName: "p", HostPort: 2222}, map[int][]portOwner{}, "ours", false, &budget)
	if a == "" || b == "" {
		t.Fatalf("both persistently-busy ports must be reported (a=%q b=%q)", a, b)
	}
	if probes[1111] != portReleaseRetries+1 {
		t.Errorf("first port should drain the budget: want %d probes, got %d", portReleaseRetries+1, probes[1111])
	}
	if probes[2222] != 1 {
		t.Errorf("second port should get a single probe after budget exhausted, got %d", probes[2222])
	}
	if budget != 0 {
		t.Errorf("shared budget should be fully consumed, got %d left", budget)
	}
}

// TestWaitPortsReleased_AllFreeNoWait verifies the common path: every declared
// port is already free, so the seed probe drains the watch set and no wait
// happens (returns nil). docker is absent from PATH so no ps probe runs.
func TestWaitPortsReleased_AllFreeNoWait(t *testing.T) {
	stubNoSleep(t)
	withIsolatedPath(t, t.TempDir()) // docker not found → no ps probe
	orig := portListenFn
	t.Cleanup(func() { portListenFn = orig })
	portListenFn = func(int) error { return nil } // all free

	cfg := &config.DweConfig{Services: map[string]config.ServiceConfig{
		"web": {Enabled: true, Ports: map[string]config.ServicePortSpec{"http": {Port: 8080}}},
	}}
	if busy := WaitPortsReleased(context.Background(), cfg, time.Second, nil); busy != nil {
		t.Errorf("all-free should return nil, got %v", busy)
	}
}

// TestWaitPortsReleased_TransientReleased reproduces the post-`docker down`
// race: caddy's :80 is busy on the seed probe and the next probe, then the
// host forwarder releases it. The poll loop must drain the watch set and return
// nil (no lingering conflict carried into the run leg).
func TestWaitPortsReleased_TransientReleased(t *testing.T) {
	stubNoSleep(t)
	withIsolatedPath(t, t.TempDir())
	orig := portListenFn
	t.Cleanup(func() { portListenFn = orig })
	var calls int
	portListenFn = func(int) error {
		calls++
		if calls <= 2 { // busy for seed + first re-probe, then released
			return errors.New("listen tcp :80: bind: address already in use")
		}
		return nil
	}
	cfg := &config.DweConfig{Services: map[string]config.ServiceConfig{
		"caddy": {Enabled: true, Ports: map[string]config.ServicePortSpec{"http": {Port: 80}}},
	}}
	if busy := WaitPortsReleased(context.Background(), cfg, 2*time.Second, nil); busy != nil {
		t.Errorf("transient release should drain to nil, got %v (calls=%d)", busy, calls)
	}
}

// TestWaitPortsReleased_PersistentBusyTimesOut verifies a port that never frees
// is returned at timeout so the caller can warn.
func TestWaitPortsReleased_PersistentBusyTimesOut(t *testing.T) {
	stubNoSleep(t)
	withIsolatedPath(t, t.TempDir())
	orig := portListenFn
	t.Cleanup(func() { portListenFn = orig })
	portListenFn = func(int) error {
		return errors.New("listen tcp :80: bind: address already in use")
	}
	cfg := &config.DweConfig{Services: map[string]config.ServiceConfig{
		"caddy": {Enabled: true, Ports: map[string]config.ServicePortSpec{"http": {Port: 80}}},
	}}
	busy := WaitPortsReleased(context.Background(), cfg, time.Second, nil)
	if !reflect.DeepEqual(busy, []int{80}) {
		t.Errorf("persistent busy port should be returned, got %v", busy)
	}
}

// TestWaitPortsReleased_LiveContainerSkipped verifies that a busy port still
// owned by a live container is NOT waited on — a wait cannot free a live
// binding, so it is excluded and the call returns nil immediately (no hang on a
// foreign service occupying the port).
func TestWaitPortsReleased_LiveContainerSkipped(t *testing.T) {
	stubNoSleep(t)
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
		return []byte(`{"Names":"rival","Ports":"0.0.0.0:80->80/tcp","Labels":{"com.docker.compose.project":"rival"}}` + "\n"), nil
	}
	portListenFn = func(int) error {
		return errors.New("listen tcp :80: bind: address already in use") // busy
	}
	cfg := &config.DweConfig{Services: map[string]config.ServiceConfig{
		"caddy": {Enabled: true, Ports: map[string]config.ServicePortSpec{"http": {Port: 80}}},
	}}
	if busy := WaitPortsReleased(context.Background(), cfg, time.Second, nil); busy != nil {
		t.Errorf("live-container-owned port must be skipped, got %v", busy)
	}
}

// TestWaitPortsReleased_OnWaitReportsLabels verifies the progress callback is
// invoked with the service/port labels for the seed set and again as ports
// drain — the data a live spinner footer renders.
func TestWaitPortsReleased_OnWaitReportsLabels(t *testing.T) {
	stubNoSleep(t)
	withIsolatedPath(t, t.TempDir())
	orig := portListenFn
	t.Cleanup(func() { portListenFn = orig })
	var calls int
	portListenFn = func(port int) error {
		calls++
		// caddy:80 frees after the seed + one re-probe; db:5432 frees later.
		switch port {
		case 80:
			if calls >= 3 {
				return nil
			}
		case 5432:
			if calls >= 6 {
				return nil
			}
		}
		return errors.New("listen: bind: address already in use")
	}
	cfg := &config.DweConfig{Services: map[string]config.ServiceConfig{
		"caddy": {Enabled: true, Ports: map[string]config.ServicePortSpec{"http": {Port: 80}}},
		"db":    {Enabled: true, Ports: map[string]config.ServicePortSpec{"sql": {Port: 5432}}},
	}}
	var snapshots [][]PortWait
	onWait := func(w []PortWait) { snapshots = append(snapshots, w) }
	if busy := WaitPortsReleased(context.Background(), cfg, 5*time.Second, onWait); busy != nil {
		t.Fatalf("all ports should drain, got busy=%v", busy)
	}
	if len(snapshots) == 0 {
		t.Fatal("onWait must be called at least once (seed)")
	}
	// Seed snapshot carries both ports, sorted by host port (80 before 5432).
	seed := snapshots[0]
	if len(seed) != 2 || seed[0].HostPort != 80 || seed[0].Service != "caddy" || seed[1].HostPort != 5432 || seed[1].Service != "db" {
		t.Fatalf("seed snapshot mismatch: %+v", seed)
	}
	// Final snapshot must show the set shrinking (db alone before it drains).
	last := snapshots[len(snapshots)-1]
	if len(last) != 1 || last[0].HostPort != 5432 {
		t.Errorf("expected last reported snapshot to be [db:5432], got %+v", last)
	}
}

// TestWaitPortsReleased_NoPortsOrNilCfg covers the trivial guards.
func TestWaitPortsReleased_NoPortsOrNilCfg(t *testing.T) {
	if busy := WaitPortsReleased(context.Background(), nil, time.Second, nil); busy != nil {
		t.Errorf("nil cfg should return nil, got %v", busy)
	}
	cfg := &config.DweConfig{Services: map[string]config.ServiceConfig{
		"web": {Enabled: false, Ports: map[string]config.ServicePortSpec{"http": {Port: 8080}}},
	}}
	if busy := WaitPortsReleased(context.Background(), cfg, time.Second, nil); busy != nil {
		t.Errorf("no enabled declared ports should return nil, got %v", busy)
	}
}

func TestPortsFreeValidator_StopStageSkips(t *testing.T) {
	// Use a free port — even so, stop stage should produce zero diagnostics.
	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"web": {Enabled: true, Ports: map[string]config.ServicePortSpec{"http": {Port: freeLocalPort(t)}}},
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
			"web": {Enabled: true, Ports: map[string]config.ServicePortSpec{"http": {Port: 8080}}},
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
			"db": {Enabled: true, Ports: map[string]config.ServicePortSpec{"sql": {Port: 5432}}},
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
			"web": {Enabled: true, Ports: map[string]config.ServicePortSpec{"http": {Port: 8080}}},
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
			"web": {Enabled: true, Ports: map[string]config.ServicePortSpec{"http": {Port: 8080}}},
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
			"db": {Enabled: true, Ports: map[string]config.ServicePortSpec{"sql": {Port: 5432}}},
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
			"db": {Enabled: true, Ports: map[string]config.ServicePortSpec{"sql": {Port: 5432}}},
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
			"web": {Enabled: true, Ports: map[string]config.ServicePortSpec{"http": {Port: 8080}}},
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
	stubNoSleep(t)
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
			"web": {Enabled: true, Ports: map[string]config.ServicePortSpec{"http": {Port: 8080}}},
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
			"web": {Enabled: false, Ports: map[string]config.ServicePortSpec{"http": {Port: 8080}}},
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
