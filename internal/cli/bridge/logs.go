package bridge

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	corebridge "github.com/semsemyonoff/dwe/internal/core/bridge"

	"github.com/spf13/cobra"
)

// bridgeLogsJSON is the `bridge logs --output json` payload.
type bridgeLogsJSON struct {
	Lines []string `json:"lines"`
}

// newLogsCmd builds `dwe bridge logs`: reads the daemon's append-only log
// file (.dwe/bridge/daemon.log — no rotation in V1, design D12).
func newLogsCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var tail int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show the bridge daemon log",
		Long: `Show the host bridge daemon log (.dwe/bridge/daemon.log).

The detached daemon's stdout and stderr are redirected there, so startup
errors and panics land in this file too.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(cmd, flags, tail)
		},
	}
	cmd.Flags().IntVar(&tail, "tail", 50, "number of trailing log lines to show (0 = all)")
	return cmd
}

func runLogs(cmd *cobra.Command, flags *cmdctx.RootFlags, tail int) error {
	if tail < 0 {
		return cmdctx.Err("invalid_tail", fmt.Sprintf("--tail must be >= 0, got %d", tail)).
			WithDetail("value", tail)
	}
	logPath := corebridge.LogPath(corebridge.DefaultBridgeDir(flags.ProjectRoot()))
	lines, err := readTailLines(logPath, tail)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return cmdctx.ErrWrap("bridge_logs_failed", err)
		}
		// No log = the daemon never ran here. Text mode mirrors the
		// "no snapshots found" idiom (stderr notice, empty stdout); JSON
		// still emits a valid empty payload.
		if flags.Output != "json" {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "no bridge daemon log at %s\n", logPath)
			return nil
		}
		return cmdctx.WriteData(flags, cmd, bridgeLogsJSON{Lines: []string{}}, renderLogsText)
	}
	return cmdctx.WriteData(flags, cmd, bridgeLogsJSON{Lines: lines}, renderLogsText)
}

// readTailLines streams the file and returns its last n lines (all lines
// when n == 0). The append-only daemon log has no rotation, so with n > 0
// memory stays bounded by a ring of n lines instead of the whole file.
func readTailLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	if n <= 0 {
		lines := []string{}
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		return lines, sc.Err()
	}
	ring := make([]string, n)
	total := 0
	for sc.Scan() {
		ring[total%n] = sc.Text()
		total++
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if total <= n {
		return ring[:total], nil
	}
	lines := make([]string, 0, n)
	for i := total - n; i < total; i++ {
		lines = append(lines, ring[i%n])
	}
	return lines, nil
}

func renderLogsText(d bridgeLogsJSON) string {
	return strings.Join(d.Lines, "\n")
}
