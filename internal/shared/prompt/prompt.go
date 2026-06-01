// Package prompt renders a compact, shell-prompt-ready segment for the current
// dwe project. Optimised for per-prompt invocation: avoids cobra, lipgloss,
// and config validation. Bypassed from cmd/dwe/main.go before cobra is
// constructed.
package prompt

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	configFilename = "workspace.yml"
	stateRelPath   = ".dwe/deploy/state.yml"
	stylesRelPath  = "workspace/styles.yml"

	defaultAccent  = "#2EC3EB"
	defaultSuccess = "#22C55E"
	defaultWarning = "#F59E0B"
	defaultDanger  = "#EF4444"

	sgrReset = "\x1b[39m"
)

type workspaceStub struct {
	Project struct {
		Name string `yaml:"name"`
	} `yaml:"project"`
}

type stateStub struct {
	Project struct {
		Status string `yaml:"status"`
	} `yaml:"project"`
	Pending *struct{} `yaml:"pending"`
}

type stylesStub struct {
	Colors struct {
		Accent  string `yaml:"accent"`
		Success string `yaml:"success"`
		Warning string `yaml:"warning"`
		Danger  string `yaml:"danger"`
	} `yaml:"colors"`
}

type statusKind int

const (
	statusNone statusKind = iota
	statusDeployed
	statusPending
	statusPartial
	statusFailed
)

func (s statusKind) icon() string {
	switch s {
	case statusDeployed:
		return "✓"
	case statusPending:
		return "⟳"
	case statusPartial:
		return "⚠"
	case statusFailed:
		return "✗"
	}
	return ""
}

// color holds a resolved RGB triple. enabled=false means the source token was
// malformed; the corresponding glyph renders plain (no SGR wrapping) even when
// global color is enabled.
type color struct {
	r, g, b uint8
	enabled bool
}

type palette struct {
	accent  color
	success color
	warning color
	danger  color
}

// Run resolves the current working directory and dispatches to runFromDir.
// Returns process exit code: 0 inside a project, 1 outside or on silent failure.
func Run(stdout io.Writer, args []string) int {
	cwd, err := os.Getwd()
	if err != nil {
		return 1
	}
	_, noColor := os.LookupEnv("NO_COLOR")
	termDumb := os.Getenv("TERM") == "dumb"
	return runFromDir(stdout, args, cwd, !noColor && !termDumb)
}

func runFromDir(stdout io.Writer, args []string, cwd string, useColor bool) int {
	check, ok := parseArgs(args)
	if !ok {
		return 1
	}

	root, found := findRoot(cwd)
	if !found {
		return 1
	}

	if check {
		return 0
	}

	name, ok := readProjectName(root)
	if !ok {
		return 1
	}

	status := readStatus(root)
	pal := readPalette(root)

	out := render(name, status, pal, useColor)
	if _, err := io.WriteString(stdout, out); err != nil {
		return 1
	}
	return 0
}

// sanitizeName strips control and Bidi-override characters from s so that a
// crafted project.name cannot inject ANSI/OSC escape sequences, newlines, or
// Unicode Bidi overrides into the shell prompt. Strips ASCII controls (< 0x20,
// 0x7F), C1 controls (0x80–0x9F), LTR/RTL marks (U+200E–U+200F), Bidi
// embeddings and overrides (U+202A–U+202E), and Bidi isolates (U+2066–U+2069).
// Starship runs dwe prompt automatically, so this must be safe for untrusted
// workspace.yml files in cloned repos.
func sanitizeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F || (r >= 0x80 && r <= 0x9F) ||
			r == 0x200E || r == 0x200F ||
			(r >= 0x202A && r <= 0x202E) ||
			(r >= 0x2066 && r <= 0x2069) {
			return -1
		}
		return r
	}, s)
}

func render(name string, status statusKind, pal palette, useColor bool) string {
	var sb strings.Builder
	sb.Grow(96)
	sb.WriteByte('{')
	writeGlyph(&sb, "▪", pal.accent, useColor)
	sb.WriteString("} ")
	sb.WriteString(sanitizeName(name))
	if icon := status.icon(); icon != "" {
		sb.WriteByte(' ')
		writeGlyph(&sb, icon, statusColor(status, pal), useColor)
	}
	sb.WriteByte('\n')
	return sb.String()
}

func writeGlyph(sb *strings.Builder, glyph string, c color, useColor bool) {
	if useColor && c.enabled {
		writeSGR(sb, c.r, c.g, c.b)
		sb.WriteString(glyph)
		sb.WriteString(sgrReset)
		return
	}
	sb.WriteString(glyph)
}

func writeSGR(sb *strings.Builder, r, g, b uint8) {
	sb.WriteString("\x1b[38;2;")
	var tmp [3]byte
	sb.Write(strconv.AppendUint(tmp[:0], uint64(r), 10))
	sb.WriteByte(';')
	sb.Write(strconv.AppendUint(tmp[:0], uint64(g), 10))
	sb.WriteByte(';')
	sb.Write(strconv.AppendUint(tmp[:0], uint64(b), 10))
	sb.WriteByte('m')
}

func statusColor(s statusKind, p palette) color {
	switch s {
	case statusDeployed:
		return p.success
	case statusPending, statusPartial:
		return p.warning
	case statusFailed:
		return p.danger
	}
	return color{}
}

func readPalette(root string) palette {
	var stub stylesStub
	if data, err := os.ReadFile(filepath.Join(root, stylesRelPath)); err == nil {
		_ = yaml.Unmarshal(data, &stub)
	}
	return palette{
		accent:  resolveColor(stub.Colors.Accent, defaultAccent),
		success: resolveColor(stub.Colors.Success, defaultSuccess),
		warning: resolveColor(stub.Colors.Warning, defaultWarning),
		danger:  resolveColor(stub.Colors.Danger, defaultDanger),
	}
}

// resolveColor picks token if non-empty, else def. Returns enabled=false when
// the chosen string is malformed — the glyph renders plain in that case.
func resolveColor(token, def string) color {
	hex := token
	if hex == "" {
		hex = def
	}
	r, g, b, ok := parseHex(hex)
	if !ok {
		return color{}
	}
	return color{r: r, g: g, b: b, enabled: true}
}

func parseHex(hex string) (r, g, b uint8, ok bool) {
	s := strings.TrimPrefix(hex, "#")
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return uint8(v >> 16), uint8(v >> 8), uint8(v), true
}

// readStatus reads the deploy journal state file and maps it to a statusKind
// using the precedence: failed > partial > pending > deployed > none.
// Any IO or YAML parse error returns statusNone silently.
func readStatus(root string) statusKind {
	data, err := os.ReadFile(filepath.Join(root, stateRelPath))
	if err != nil {
		return statusNone
	}
	var stub stateStub
	if err := yaml.Unmarshal(data, &stub); err != nil {
		return statusNone
	}
	switch stub.Project.Status {
	case "failed":
		return statusFailed
	case "in_progress":
		// pipeline crashed before completing — journal semantics treat this as failure.
		return statusFailed
	case "partial":
		return statusPartial
	}
	if stub.Pending != nil {
		return statusPending
	}
	if stub.Project.Status == "deployed" {
		return statusDeployed
	}
	return statusNone
}

// parseArgs returns (checkMode, ok). ok=false means args are malformed and the
// caller should exit silently with code 1.
func parseArgs(args []string) (check bool, ok bool) {
	switch len(args) {
	case 0:
		return false, true
	case 1:
		if args[0] == "--check" {
			return true, true
		}
	}
	return false, false
}

// findRoot walks up from start looking for workspace.yml. Returns the directory
// containing it. Does NOT resolve symlinks (intentional: prompt does not care
// about canonical paths, and skipping EvalSymlinks saves syscalls).
func findRoot(start string) (string, bool) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, configFilename)); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// readProjectName returns the project name and ok=true on success. ok=false
// signals a hard read/parse failure (corrupted workspace.yml) and the caller
// should exit silently with code 1.
func readProjectName(root string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(root, configFilename))
	if err != nil {
		return "", false
	}
	var stub workspaceStub
	if err := yaml.Unmarshal(data, &stub); err != nil {
		return "", false
	}
	if stub.Project.Name == "" {
		return filepath.Base(root), true
	}
	return stub.Project.Name, true
}
