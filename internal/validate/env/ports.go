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

	"devbox-cli/internal/config"
	"devbox-cli/internal/daemon"
	"devbox-cli/internal/validate"
)

// portsProbeTimeout caps the `docker ps` invocation so a hung daemon does not
// block validate forever.
const portsProbeTimeout = 5 * time.Second

// composeProjectLabel is the standard label every container created via
// `docker compose` carries. We use it to distinguish our containers (which
// are expected to bind the declared ports) from foreign processes.
const composeProjectLabel = "com.docker.compose.project"

// PortConflict describes a host port that is in use by something other than
// our own compose containers. It is returned by the exported CollectPortConflicts
// probe for use by the wizard and other callers.
type PortConflict struct {
	Service       string // service name from devbox/services/<name>/
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
func CollectPortConflicts(ctx context.Context, cfg *config.DevboxConfig, baseDir string) ([]PortConflict, error) {
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

	var conflicts []PortConflict
	for _, dp := range declared {
		owner := classifyPortForConflict(dp, bindings, ourProject, psErr != nil)
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

type portsFreeValidator struct {
	cfg *config.DevboxConfig
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
			fmt.Sprintf("port %d (%s.%s) is bound by container %s",
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
// devbox/services.yml (or overlays). Service + PortName are kept so the
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
func collectDeclaredPorts(cfg *config.DevboxConfig) []declaredPort {
	if cfg == nil {
		return nil
	}
	var out []declaredPort
	for name, svc := range cfg.Services {
		if !svc.Enabled {
			continue
		}
		for portName, hostPort := range svc.Ports {
			if hostPort <= 0 || hostPort > 65535 {
				continue
			}
			out = append(out, declaredPort{
				Service:  name,
				PortName: portName,
				HostPort: hostPort,
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

// classifyPortOwner returns "" if the port is free or held by our own compose
// containers, otherwise the name of the container holding it (possibly with
// compose project info). Called only by classifyPort; production code uses
// classifyPortForConflict which handles the docker ps failure case.
func classifyPortOwner(dp declaredPort, bindings map[int][]portOwner, ourProject string) string {
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
	// Not held by Docker — probe directly.
	if err := portListenFn(dp.HostPort); err != nil {
		return firstLine(err.Error(), "address already in use")
	}
	return ""
}

// classifyPort returns "" if the port is acceptable (free, or held by one of
// our own compose containers), otherwise a user-facing reason string.
func classifyPort(dp declaredPort, bindings map[int][]portOwner, ourProject string) string {
	owner := classifyPortOwner(dp, bindings, ourProject)
	if owner == "" {
		return ""
	}
	return fmt.Sprintf("port %d (%s.%s) is bound by container %s",
		dp.HostPort, dp.Service, dp.PortName, owner)
}

// classifyPortForConflict is like classifyPortOwner but handles the docker ps
// failure case by returning a sentinel in place of the owner name.
func classifyPortForConflict(dp declaredPort, bindings map[int][]portOwner, ourProject string, dockerPSFailed bool) string {
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
	// Not held by Docker — probe directly.
	if err := portListenFn(dp.HostPort); err != nil {
		if dockerPSFailed {
			return "unknown (docker ps failed)"
		}
		return firstLine(err.Error(), "address already in use")
	}
	return ""
}

// resolveComposeProject returns the compose project name for the current devbox
// project. It reads docker.yml first; if that is absent or has no project_name,
// it falls back to the lowercased directory basename — the same default that
// Docker Compose v2 applies when no project_name is configured. Without this
// fallback, our own containers from a previous deploy would be misidentified as
// foreign conflicts and block the next `devbox run` / `devbox deploy run`.
func resolveComposeProject(baseDir string, cfg *config.DevboxConfig) string {
	if baseDir == "" || cfg == nil {
		return ""
	}
	dockerCfg, err := config.LoadDockerConfig(baseDir, cfg)
	if err == nil && dockerCfg != nil && dockerCfg.ProjectName != "" {
		return dockerCfg.ProjectName
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

// listenTCP attempts to bind a TCP listener on the wildcard interface for the
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
