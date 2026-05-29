// Package stack — daemons collector.
//
// Enumerates running daemon containers for the current project via
// `docker ps --format=json` filtered on the standard devbox.project and
// devbox.daemon.id labels. The output is NDJSON — one JSON object per line —
// parsed with bufio.Scanner + per-line json.Unmarshal (never as a JSON
// array). The collector is consumed by the `status daemons` section and the
// default `status` orchestrator.
//
// docker ps is authoritative — there is no on-disk daemon state file. A
// `docker stop <container>` issued outside devbox is reflected immediately on
// the next status read.
package stack

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"
	"unicode"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/ui/render"
	"devbox-cli/internal/core/ui/statusview"
	"devbox-cli/internal/shared/daemon"
	"devbox-cli/internal/shared/docker"
)

// daemonsShellOutFn is the seam used by CollectDaemons to invoke docker ps.
// Tests override it to inject canned NDJSON without spawning a process.
var daemonsShellOutFn = runDaemonsPS

// runDaemonsPS shells out to `<bin> ps --format=json` with the standard
// project + daemon.id label filters and returns captured stdout. The compose
// argument is used for bin name and process env only — `docker ps` is not a
// compose subcommand.
func runDaemonsPS(ctx context.Context, compose *docker.Compose, projectFullName string) ([]byte, error) {
	args := []string{"ps", "--format=json"}
	args = append(args, daemon.FilterArgsByLabels(projectFullName, "")...)
	cmd := exec.CommandContext(ctx, compose.BinName(), args...) //nolint:gosec
	cmd.Env = compose.BuildEnv()
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return out, nil
}

// CollectDaemons returns one DaemonRow per running daemon container for the
// current project. dockerCfg MUST be non-nil — callers normalise os.ErrNotExist
// to &config.DockerConfig{} so that BinName() (e.g. podman) and BuildEnv()
// (DOCKER_HOST / DOCKER_CONTEXT propagation) apply uniformly.
//
// Returns rows sorted by daemon ID then container name and a slice of errors
// for per-row parse failures. A docker shellout failure surfaces as a single
// error entry and an empty row slice (best-effort: status renders the rest of
// the project without aborting).
func CollectDaemons(ctx context.Context, cfg *config.DevboxConfig, dockerCfg *config.DockerConfig) ([]statusview.DaemonRow, []error) {
	if cfg == nil {
		return nil, nil
	}
	if dockerCfg == nil {
		dockerCfg = &config.DockerConfig{}
	}
	compose := docker.NewCompose(cfg, dockerCfg)
	projectFull := cfg.Project.FullName()
	out, err := daemonsShellOutFn(ctx, compose, projectFull)
	if err != nil {
		return nil, []error{fmt.Errorf("docker ps: %w", err)}
	}
	if len(out) == 0 {
		return nil, nil
	}
	rows, perrs := parseDaemonRows(bytes.NewReader(out))
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ID != rows[j].ID {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].Container < rows[j].Container
	})
	return rows, perrs
}

// psRecord is the minimal shape of a `docker ps --format=json` line that
// CollectDaemons needs. Labels has two on-the-wire encodings:
//   - modern: map[string]string (object)
//   - legacy: "k=v,k=v" (string)
//
// The parser tolerates both via json.RawMessage + type switch.
type psRecord struct {
	Names      string          `json:"Names"`
	Name       string          `json:"Name"`
	Labels     json.RawMessage `json:"Labels"`
	CreatedAt  string          `json:"CreatedAt"`
	RunningFor string          `json:"RunningFor"`
}

// parseDaemonRows turns NDJSON `docker ps --format=json` output into
// DaemonRows. Invalid lines are skipped with a per-line error appended.
func parseDaemonRows(r io.Reader) ([]statusview.DaemonRow, []error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var rows []statusview.DaemonRow
	var errs []error
	now := time.Now()
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec psRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			errs = append(errs, fmt.Errorf("parse docker ps line: %w", err))
			continue
		}
		labels := decodeLabels(rec.Labels)
		id := labels[daemon.LabelDaemonID]
		if id == "" {
			// Defensive: docker ps was filtered by daemon.id label presence,
			// but a container without the label slipping in is non-fatal.
			continue
		}
		container := rec.Names
		if container == "" {
			container = rec.Name
		}
		if i := strings.IndexByte(container, ','); i >= 0 {
			container = container[:i]
		}
		container = strings.TrimSpace(container)
		started := parseDockerTime(rec.CreatedAt)
		var uptime time.Duration
		if !started.IsZero() {
			uptime = max(now.Sub(started), 0)
		}
		rows = append(rows, statusview.DaemonRow{
			ID:        sanitiseDisplay(id),
			Params:    sanitiseDisplay(prettyParams(labels[daemon.LabelDaemonParams])),
			Container: sanitiseDisplay(container),
			Uptime:    uptime,
			StartedAt: started,
		})
	}
	if err := scanner.Err(); err != nil {
		errs = append(errs, err)
	}
	return rows, errs
}

// decodeLabels delegates to daemon.DecodeLabels, which handles both label
// encodings docker has used: modern map[string]string and legacy "k=v,k=v".
func decodeLabels(raw json.RawMessage) map[string]string {
	return daemon.DecodeLabels(raw)
}

// prettyParams renders a devbox.daemon.params JSON object as a stable
// "k=v, k=v" string for display. Falls back to the raw value on parse error.
func prettyParams(s string) string {
	if s == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return s
	}
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

// parseDockerTime parses the timestamp shape docker emits in `CreatedAt`.
// Returns the zero time on parse failure (uptime then renders empty).
func parseDockerTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	formats := []string{
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05 -0700",
		time.RFC3339,
		time.RFC3339Nano,
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// sanitiseDisplay strips control characters so an external actor (or an older
// devbox version) can't disrupt the terminal via crafted labels or container
// names. Standard whitespace (space, tab) is preserved.
func sanitiseDisplay(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\t' || r == ' ' {
			b.WriteRune(r)
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// RenderDaemons returns the Daemons section as a single string (title + table)
// and any parse errors collected while building the rows. Empty rows → empty
// string so the orchestrator can hide the section entirely.
func RenderDaemons(rows []statusview.DaemonRow) (string, []error) {
	if len(rows) == 0 {
		return "", nil
	}
	tableRows := make([]render.DaemonTableRow, len(rows))
	for i, r := range rows {
		tableRows[i] = render.DaemonTableRow{
			ID:        r.ID,
			Params:    r.Params,
			Container: r.Container,
			Uptime:    formatUptime(r.Uptime),
		}
	}
	body := render.DaemonTable(tableRows)
	if body == "" {
		return "", nil
	}
	var b strings.Builder
	b.WriteString(render.SectionTitle("Daemons"))
	b.WriteByte('\n')
	b.WriteString(body)
	b.WriteByte('\n')
	return b.String(), nil
}

// formatUptime renders a Duration in a compact human-friendly form.
// Sub-second values render as "<1s". Empty when zero.
func formatUptime(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Second {
		return "<1s"
	}
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		return fmt.Sprintf("%dh%dm", h, m)
	default:
		days := int(d.Hours()) / 24
		h := int(d.Hours()) % 24
		return fmt.Sprintf("%dd%dh", days, h)
	}
}
