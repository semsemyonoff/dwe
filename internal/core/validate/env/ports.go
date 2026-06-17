package env

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/shared/daemon"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

// portsProbeTimeout caps the `docker ps` invocation so a hung daemon does not
// block validate forever.
const portsProbeTimeout = 5 * time.Second

// portReleaseRetries / portReleaseBackoff bound the retry loop that absorbs the
// asynchronous host-port release performed by Docker Desktop / OrbStack on
// macOS. When `docker compose down` removes a container, its published host
// port (e.g. caddy's 127.0.0.1:80) is freed by the host port-forwarder a beat
// LATER than the container disappears from `docker ps`. A `dwe restart`
// (stop = `docker down`, then immediately run = preflight `ports_free`) races
// that release: docker ps shows no owner, but net.Listen still sees EADDRINUSE,
// so the just-downed container of our OWN stack is falsely reported as a port
// conflict. Retrying the listen probe briefly lets the forwarder catch up — a
// genuine foreign holder persists across all attempts and is still reported,
// only ~portReleaseRetries*portReleaseBackoff later. On native Linux the port
// frees synchronously so the first attempt already succeeds (no added latency).
const (
	portReleaseRetries = 6
	portReleaseBackoff = 250 * time.Millisecond
)

// portReleaseSleep is the seam used by the EADDRINUSE retry loop so tests can
// run it without real wall-clock delay.
var portReleaseSleep = time.Sleep

// portReleaseWaitPoll is the interval between net.Listen re-probes in
// WaitPortsReleased. A var so tests can drive the loop through the shared
// portReleaseSleep seam without incurring real delay.
var portReleaseWaitPoll = 250 * time.Millisecond

// composeProjectLabel is the standard label every container created via
// `docker compose` carries. We use it to distinguish our containers (which
// are expected to bind the declared ports) from foreign processes.
const composeProjectLabel = docker.ComposeProjectLabel

// PortConflict describes a host port that is in use by something other than
// our own compose containers. It is returned by the exported CollectPortConflicts
// probe for use by the wizard and other callers.
type PortConflict struct {
	Service       string // service name from workspace/services/<name>/
	PortName      string // port key from service.yml
	RequestedPort int    // the port number we want to use
	OccupiedBy    string // human-readable description of who is using it
}

// dockerPSOutFn is the seam used by portsFreeValidator.Run to query Docker.
// Tests override it to return canned NDJSON without spawning a process.
var dockerPSOutFn = runDockerPS

// portListenFn is the seam used by portsFreeValidator.Run to probe host
// availability. Tests override it to simulate busy ports without binding
// real sockets.
var portListenFn = listenTCP

// CollectPortConflicts returns a list of host ports declared in cfg.Services
// that are in use by something other than our own compose containers.
//
// Behavior on Docker unavailability:
//   - If Docker binary is missing or unresolvable: returns (nil, nil). The
//     wizard sees an empty list and skips its port-fix step; preflight will
//     surface the missing-Docker problem separately when the user proceeds.
//   - If docker ps invocation fails after Docker binary is found: falls back
//     to net.Listen probes on the declared ports. Conflicts in this fallback
//     set OccupiedBy to "unknown (docker ps failed)" so callers can render a
//     meaningful label. Any other error during probing is returned wrapped.
//
// This exported function is the canonical port-conflict probe. Both the
// validator (portsFreeValidator) and the setup wizard use it — there is no
// second port enumeration path.
func CollectPortConflicts(ctx context.Context, cfg *config.DweConfig, baseDir string) ([]PortConflict, error) {
	if cfg == nil {
		return nil, nil
	}
	declared := collectDeclaredPorts(cfg)
	if len(declared) == 0 {
		return nil, nil
	}
	bin := config.DockerBin(cfg)
	if _, err := exec.LookPath(bin); err != nil {
		// docker_bin will surface this; do not double-report.
		return nil, nil
	}

	ourProject := resolveComposeProject(baseDir, cfg)

	parent := ctx
	if parent == nil {
		parent = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(parent, portsProbeTimeout)
	defer cancel()
	bindings, psErr := queryDockerPortBindings(probeCtx, bin)

	// The post-`docker down` host-port release window is shared across the
	// whole pass (a `dwe restart` downs the entire stack at once), so the retry
	// budget is shared too: each busy port draws from the same counter rather
	// than paying portReleaseRetries independently. This bounds total added
	// latency to ~portReleaseRetries*portReleaseBackoff for the pass — without
	// it, N genuinely-busy ports would sleep N×1.5s sequentially.
	retriesLeft := portReleaseRetries
	var conflicts []PortConflict
	for _, dp := range declared {
		owner := classifyPortForConflict(dp, bindings, ourProject, psErr != nil, &retriesLeft)
		if owner != "" {
			conflicts = append(conflicts, PortConflict{
				Service:       dp.Service,
				PortName:      dp.PortName,
				RequestedPort: dp.HostPort,
				OccupiedBy:    owner,
			})
		}
	}
	return conflicts, nil
}

// PortWait identifies one host port still being waited on by WaitPortsReleased,
// carrying the owning service + port name so a caller can render a descriptive
// progress line (e.g. a live spinner: "waiting for host port 80 (caddy.http)").
type PortWait struct {
	Service  string
	PortName string
	HostPort int
}

// WaitPortsReleased blocks until every host port declared by an enabled service
// is released, or until timeout elapses, and returns the sorted host ports that
// were still busy at timeout (nil when all released).
//
// It absorbs the asynchronous host-port release that Docker Desktop / OrbStack
// perform AFTER `docker compose down` returns: the container is already gone
// from `docker ps`, yet its published host port (e.g. caddy's :80) lingers a
// beat in the host port-forwarder — long enough for an immediately-following
// `dwe run` (the run leg of a restart) preflight to falsely report a
// self-conflict on our OWN just-freed port. Stop calls this after the down so
// the next start sees a clean slate; the preflight retry loop remains the final
// backstop for runs that did not go through dwe's stop.
//
// A live container holding a declared port is excluded: that is a real binding
// (foreign, or ours if the down left it up) which a wait cannot free, so it
// never makes stop hang. The set that IS waited on is "busy now AND owned by no
// live container" — normally exactly a lingering forward of our own just-downed
// container, but note a foreign NON-docker host process on a declared port also
// matches and will be waited on for the full timeout (then warned about): we
// cannot distinguish it from a lingering forward via net.Listen alone. On native
// Linux ports free synchronously, so the seed probe already finds them free and
// this returns nil with no wait.
//
// The wait honors ctx: if it is cancelled (deadline/parent cancel) the loop
// stops at the next poll boundary and returns whatever is still busy.
//
// onWait (optional, nil-safe) is invoked with the currently-watched ports
// whenever the set is non-empty: once after the seed probe and again after each
// poll that releases at least one port. Callers drive a live progress display
// from it; the timer/animation is the caller's concern (this function only
// reports WHICH ports remain, not elapsed time).
func WaitPortsReleased(ctx context.Context, cfg *config.DweConfig, timeout time.Duration, onWait func([]PortWait)) []int {
	if cfg == nil || timeout <= 0 {
		return nil
	}
	declared := collectDeclaredPorts(cfg)
	if len(declared) == 0 {
		return nil
	}
	parent := ctx
	if parent == nil {
		parent = context.Background()
	}

	// One docker ps snapshot: a port still owned by a live container is a real
	// binding, not a lingering forward — exclude it so we never wait on
	// something a wait cannot fix. Docker being unavailable is fine: bindings
	// stays nil and every busy port is treated as a candidate forward.
	var bindings map[int][]portOwner
	if _, err := exec.LookPath(config.DockerBin(cfg)); err == nil {
		probeCtx, cancel := context.WithTimeout(parent, portsProbeTimeout)
		bindings, _ = queryDockerPortBindings(probeCtx, config.DockerBin(cfg))
		cancel()
	}

	// Seed the watch set with declared ports busy now and unowned by a live
	// container. listenTCP only errors on EADDRINUSE (see its doc), so a non-nil
	// probe means genuinely busy.
	watch := map[int]declaredPort{}
	for _, dp := range declared {
		if len(bindings[dp.HostPort]) > 0 {
			continue
		}
		if portListenFn(dp.HostPort) == nil {
			continue // already free
		}
		watch[dp.HostPort] = dp
	}
	if len(watch) == 0 {
		return nil
	}
	notify := func() {
		if onWait != nil {
			onWait(portWaitList(watch))
		}
	}
	notify() // seed: announce the initial set so the caller can start its display

	// Poll until the set drains or the configured budget is spent. The budget is
	// tracked as an intended-duration countdown (remaining -= step), NOT measured
	// wall-clock: that keeps the portReleaseSleep test seam (a no-op) terminating
	// in the same iteration count, while production honors the EXACT timeout —
	// the final step is the sub-poll remainder rather than a truncated full poll,
	// so e.g. a 300ms budget waits 250ms+50ms, not just 250ms. portReleaseSleep +
	// portListenFn are seams so tests run instantly.
	for remaining := timeout; remaining > 0 && len(watch) > 0; {
		if parent.Err() != nil {
			break // ctx cancelled — stop waiting and report whatever is still busy
		}
		step := min(remaining, portReleaseWaitPoll)
		portReleaseSleep(step)
		remaining -= step
		changed := false
		for p, dp := range watch {
			if portListenFn(dp.HostPort) == nil {
				delete(watch, p)
				changed = true
			}
		}
		if changed && len(watch) > 0 {
			notify()
		}
	}
	if len(watch) == 0 {
		return nil
	}
	remaining := make([]int, 0, len(watch))
	for p := range watch {
		remaining = append(remaining, p)
	}
	sort.Ints(remaining)
	return remaining
}

// portWaitList converts the internal watch set into a sorted (by host port)
// slice of PortWait for the onWait callback.
func portWaitList(watch map[int]declaredPort) []PortWait {
	out := make([]PortWait, 0, len(watch))
	for _, dp := range watch {
		out = append(out, PortWait(dp))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HostPort < out[j].HostPort })
	return out
}

type portsFreeValidator struct {
	cfg *config.DweConfig
}

var _ validate.Validator = (*portsFreeValidator)(nil)

func (v *portsFreeValidator) ID() string     { return "ports_free" }
func (v *portsFreeValidator) Domain() string { return "env" }

func (v *portsFreeValidator) Run(vctx validate.Context) []validate.Diagnostic {
	// Stopping the project cannot fail on a port conflict — irrelevant scope.
	if vctx.Stage == "stop" {
		return nil
	}

	declared := collectDeclaredPorts(v.cfg)
	if len(declared) == 0 {
		return nil
	}

	bin := config.DockerBin(v.cfg)
	if _, err := exec.LookPath(bin); err != nil {
		// docker_bin will surface this; do not double-report.
		return nil
	}

	parent := vctx.Ctx
	if parent == nil {
		parent = context.Background()
	}
	// CollectPortConflicts never returns a non-nil error; docker ps failures are
	// encoded in the conflict result as "unknown (docker ps failed)" instead.
	conflicts, _ := CollectPortConflicts(parent, v.cfg, vctx.ProjectRoot)

	var diags []validate.Diagnostic
	for _, pc := range conflicts {
		diags = append(diags, fail(
			"ports_free",
			fmt.Sprintf("port %d (%s.%s) is in use: %s",
				pc.RequestedPort, pc.Service, pc.PortName, pc.OccupiedBy),
			"free the port (stop the conflicting process or container) and retry\nlsof -i :"+strconv.Itoa(pc.RequestedPort),
		))
	}
	if len(diags) == 0 {
		return []validate.Diagnostic{ok("ports_free")}
	}
	return diags
}

// declaredPort identifies a host port that one of our services declared in
// workspace/services.yml (or overlays). Service + PortName are kept so the
// diagnostic can pinpoint exactly which service expected the port.
type declaredPort struct {
	Service  string
	PortName string
	HostPort int
}

// collectDeclaredPorts walks cfg.Services and produces one entry per declared
// host port on an enabled service. Disabled services are skipped — they do not
// bind ports, so a conflict on their declared port does not block startup.
// Output is sorted (service, portName) for deterministic diagnostics.
func collectDeclaredPorts(cfg *config.DweConfig) []declaredPort {
	if cfg == nil {
		return nil
	}
	var out []declaredPort
	for name, svc := range cfg.Services {
		if !svc.Enabled {
			continue
		}
		for portName, spec := range svc.Ports {
			if spec.Port <= 0 || spec.Port > 65535 {
				continue
			}
			out = append(out, declaredPort{
				Service:  name,
				PortName: portName,
				HostPort: spec.Port,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].PortName < out[j].PortName
	})
	return out
}

// portOwner describes who owns a host port observed via `docker ps`.
type portOwner struct {
	Container      string
	ComposeProject string
}

// classifyPortForConflict is like classifyPortOwner but handles the docker ps
// failure case by returning a sentinel in place of the owner name.
// retriesLeft is a shared, mutable EADDRINUSE-retry budget threaded across all
// ports in one CollectPortConflicts pass (see the call site). It is decremented
// as this function consumes retries so the whole pass — not each port — is
// bounded to portReleaseRetries sleeps total.
func classifyPortForConflict(dp declaredPort, bindings map[int][]portOwner, ourProject string, dockerPSFailed bool, retriesLeft *int) string {
	owners := bindings[dp.HostPort]
	if len(owners) > 0 {
		for _, o := range owners {
			if ourProject != "" && o.ComposeProject == ourProject {
				// Our own container holds it — compose will reuse on `up`.
				return ""
			}
		}
		o := owners[0]
		who := o.Container
		if o.ComposeProject != "" && o.ComposeProject != ourProject {
			who += " (compose project: " + o.ComposeProject + ")"
		}
		return who
	}
	// Not held by Docker — probe directly. listenTCP only returns a non-nil
	// error for EADDRINUSE (other errors are treated as "free"), so any error
	// here means the port is busy. Retry briefly to absorb Docker Desktop's
	// asynchronous host-port release after a `docker down` (see the
	// portReleaseRetries doc): a genuine foreign holder survives every attempt;
	// a port mid-release by our own just-downed container clears.
	err := portListenFn(dp.HostPort)
	for err != nil && *retriesLeft > 0 {
		*retriesLeft--
		portReleaseSleep(portReleaseBackoff)
		err = portListenFn(dp.HostPort)
	}
	if err != nil {
		if dockerPSFailed {
			return "unknown (docker ps failed)"
		}
		return firstLine(err.Error(), "address already in use")
	}
	return ""
}

// resolveComposeProject returns the compose project name for the current dwe
// project — the value Docker writes to the com.docker.compose.project label,
// which this check matches running containers against to tell our own
// prior-deploy stack (reuse, not a conflict) from a foreign project.
//
// It mirrors the exact precedence buildCompose stamps onto every `docker
// compose` invocation: the resolved docker.yml project_name, else the canonical
// "<prefix>-<name>" (cfg.Project.FullName()), else — only when FullName is empty
// — the lowercased directory basename that Docker Compose v2 itself defaults to.
// Keeping this aligned with buildCompose is load-bearing: if it returned the
// basename while compose now labels with FullName, our own containers would be
// misidentified as foreign conflicts and block the next `dwe run` / `dwe deploy run`.
func resolveComposeProject(baseDir string, cfg *config.DweConfig) string {
	if baseDir == "" || cfg == nil {
		return ""
	}
	dockerCfg, err := config.LoadDockerConfig(baseDir, cfg)
	if err == nil && dockerCfg != nil && dockerCfg.ProjectName != "" {
		return dockerCfg.ProjectName
	}
	if full := cfg.Project.FullName(); full != "" {
		return full
	}
	return strings.ToLower(filepath.Base(baseDir))
}

// runDockerPS shells out to `docker ps --format=json --no-trunc` (no filter)
// to get every running container's name, ports, and labels in one call. The
// no-filter approach lets us see foreign containers from other projects so we
// can name them in the conflict message instead of just saying "in use."
func runDockerPS(ctx context.Context, bin string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, "ps", "--format=json", "--no-trunc") //nolint:gosec
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// queryDockerPortBindings parses `docker ps --format=json` NDJSON into a
// host-port → owners map. Returns an empty map (not nil) if Docker is
// unreachable or output is unparseable — the caller falls back to net.Listen
// for ports not present in the map.
func queryDockerPortBindings(ctx context.Context, bin string) (map[int][]portOwner, error) {
	out, err := dockerPSOutFn(ctx, bin)
	if err != nil || len(out) == 0 {
		return map[int][]portOwner{}, err
	}
	return parsePortBindings(out), nil
}

// psPortRecord mirrors the minimal `docker ps --format=json` fields we need.
// Labels carries two on-the-wire encodings (object or "k=v,k=v" string),
// handled via daemon.DecodeLabels.
type psPortRecord struct {
	Names  string          `json:"Names"`
	Ports  string          `json:"Ports"`
	Labels json.RawMessage `json:"Labels"`
}

// parsePortBindings turns NDJSON `docker ps --format=json` output into a map
// from host port to the list of containers binding it. Invalid lines are
// skipped silently (best-effort: we'd rather miss one container than fail the
// whole probe).
func parsePortBindings(data []byte) map[int][]portOwner {
	result := map[int][]portOwner{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec psPortRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		ports := parsePortsField(rec.Ports)
		if len(ports) == 0 {
			continue
		}
		labels := daemon.DecodeLabels(rec.Labels)
		owner := portOwner{
			Container:      rec.Names,
			ComposeProject: labels[composeProjectLabel],
		}
		for _, p := range ports {
			result[p] = append(result[p], owner)
		}
	}
	return result
}

// parsePortsField extracts host port numbers from the `Ports` field returned
// by `docker ps --format=json`. The field is a comma-separated list of
// entries like:
//
//	"0.0.0.0:5432->5432/tcp"            — bound on all v4 interfaces
//	"127.0.0.1:5432->5432/tcp"          — bound on localhost only
//	":::5432->5432/tcp"                 — IPv6 wildcard
//	"5432/tcp"                          — exposed but not published (skipped)
//
// Only entries with "->" are host-published; the host port sits to the left
// of the arrow after the last colon. We dedupe — IPv4 + IPv6 lines on the
// same port count once.
func parsePortsField(s string) []int {
	if s == "" {
		return nil
	}
	seen := map[int]struct{}{}
	for entry := range strings.SplitSeq(s, ",") {
		entry = strings.TrimSpace(entry)
		left, _, ok := strings.Cut(entry, "->")
		if !ok {
			continue
		}
		// Strip optional "IP:" prefix — keep only the rightmost ":" component.
		if i := strings.LastIndex(left, ":"); i >= 0 {
			left = left[i+1:]
		}
		p, err := strconv.Atoi(left)
		if err != nil || p <= 0 || p > 65535 {
			continue
		}
		seen[p] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]int, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

// IsPortAvailable reports whether port can be bound on localhost right now.
// Returns true when no process holds it (free), false when EADDRINUSE.
// Any other error (e.g. EACCES on a privileged port < 1024) is treated as
// available because we cannot prove otherwise.
//
// This is a fast, foreground probe meant for interactive validators (e.g. the
// setup wizard's port-override form). For full conflict classification —
// including "occupied by one of our own containers" — use CollectPortConflicts.
func IsPortAvailable(port int) bool {
	return portListenFn(port) == nil
}

// given port and closes it immediately. Used as the fallback check for ports
// not bound by any docker container — if listen succeeds the port is free,
// if it fails with EADDRINUSE some non-docker process owns it. Any other error
// (e.g. EACCES on a privileged port < 1024) means we cannot probe — treat as free.
func listenTCP(port int) error {
	l, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return err
		}
		return nil // cannot probe; assume free
	}
	_ = l.Close()
	return nil
}
