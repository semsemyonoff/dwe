package bridge

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	corebridge "github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/bridge/shimassets"
	"github.com/semsemyonoff/dwe/internal/core/project/config"

	"github.com/spf13/cobra"
)

// Test seams: the probe takes the bridge-private pidfile flock, the shim
// probe reads the real embed tree (empty without `make shims`), and nowFn
// pins the uptime in goldens.
var (
	probeDaemonFn = corebridge.ProbeDaemon
	shimStatusFn  = shimassets.Status
	nowFn         = time.Now
)

// bridgeStatusJSON is the `bridge status --output json` payload.
type bridgeStatusJSON struct {
	Enabled       bool             `json:"enabled"`
	Running       bool             `json:"running"`
	PID           int              `json:"pid,omitempty"`
	StartedAt     string           `json:"started_at,omitempty"`
	UptimeSeconds int64            `json:"uptime_seconds,omitempty"`
	Socket        string           `json:"socket,omitempty"`
	Port          int              `json:"port,omitempty"`
	Shims         []bridgeShimJSON `json:"shims"`
}

// bridgeShimJSON is one shim's materialization state in the status payload.
type bridgeShimJSON struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// newStatusCmd builds `dwe bridge status`: daemon liveness (pidfile-flock
// probe), transports, and shim materialization state. It is the one bridge
// subcommand allowed from inside containers (design D9).
func newStatusCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show bridge daemon liveness and transports",
		Long: `Show the host bridge state for this project: daemon liveness (pid,
uptime), transport endpoints (unix socket, TCP port), and the materialization
state of the in-container shim binaries.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd, flags)
		},
	}
}

func runStatus(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return cmdctx.ErrWrap("project_invalid_config", err)
	}
	baseDir := flags.ProjectRoot()
	bridgeDir := corebridge.DefaultBridgeDir(baseDir)

	probe, err := probeDaemonFn(bridgeDir)
	if err != nil {
		return cmdctx.ErrWrap("bridge_status_failed", err)
	}

	data := bridgeStatusJSON{
		Enabled: corebridge.AnyBridgeEnabled(cfg),
		Running: probe.Running,
		Shims:   []bridgeShimJSON{},
	}
	if probe.Running {
		data.PID = probe.PID
		data.StartedAt = probe.StartedAt.UTC().Format(time.RFC3339)
		if up := nowFn().Sub(probe.StartedAt); up > 0 {
			data.UptimeSeconds = int64(up.Seconds())
		}
	}
	if sock := corebridge.SocketPath(bridgeDir); fileExists(sock) {
		data.Socket = sock
	}
	if port, ok := readPortFile(corebridge.PortPath(bridgeDir)); ok {
		data.Port = port
	}

	shims, err := shimStatusFn(baseDir)
	if err != nil {
		return cmdctx.ErrWrap("bridge_status_failed", err)
	}
	for _, s := range shims {
		data.Shims = append(data.Shims, bridgeShimJSON{Name: s.Name, State: s.State})
	}
	return cmdctx.WriteData(flags, cmd, data, renderStatusText)
}

func renderStatusText(d bridgeStatusJSON) string {
	var lines []string
	if d.Running {
		up := time.Duration(d.UptimeSeconds) * time.Second
		lines = append(lines, fmt.Sprintf("daemon:  running (pid %d, up %s)", d.PID, up))
	} else {
		lines = append(lines, "daemon:  not running")
	}
	if !d.Enabled {
		lines = append(lines, "bridge:  no enabled service has the host bridge enabled")
	}
	if d.Socket != "" {
		lines = append(lines, "socket:  "+d.Socket)
	}
	if d.Port != 0 {
		lines = append(lines, fmt.Sprintf("port:    %d", d.Port))
	}
	if len(d.Shims) == 0 {
		lines = append(lines, "shims:   none")
	} else {
		parts := make([]string, 0, len(d.Shims))
		for _, s := range d.Shims {
			parts = append(parts, s.Name+" "+s.State)
		}
		lines = append(lines, "shims:   "+strings.Join(parts, ", "))
	}
	return strings.Join(lines, "\n")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// readPortFile parses the daemon-written port file; ok=false when the file
// is absent or holds anything but a positive port number.
func readPortFile(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || port <= 0 {
		return 0, false
	}
	return port, true
}
