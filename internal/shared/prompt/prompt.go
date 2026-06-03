// Package prompt renders a compact, shell-prompt-ready segment for the current
// dwe project. Optimised for per-prompt invocation: avoids cobra, lipgloss,
// and config validation. Bypassed from cmd/dwe/main.go before cobra is
// constructed.
package prompt

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	configFilename     = "workspace.yml"
	stateRelPath       = ".dwe/deploy/state.yml"
	stylesRelPath      = "workspace/styles.yml"
	promptCacheRelPath = ".dwe/prompt-cache.yml"

	defaultAccent  = "#2EC3EB"
	defaultSuccess = "#22C55E"
	defaultWarning = "#F59E0B"
	defaultDanger  = "#EF4444"
	defaultMuted   = "#6B7280"

	sgrReset = "\x1b[39m"

	cacheTTL       = 2 * time.Minute
	refreshTimeout = 150 * time.Millisecond
)

// dockerPsFunc shells out to `docker ps -q --filter label=com.docker.compose.project=<project>`.
// Package-level for test injection.
var dockerPsFunc = realDockerPs

func realDockerPs(ctx context.Context, composeProject string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", "ps", "-q",
		"--filter", "label=com.docker.compose.project="+composeProject)
	return cmd.Output()
}

type workspaceStub struct {
	Project struct {
		Name   string `yaml:"name"`
		Prefix string `yaml:"prefix"`
	} `yaml:"project"`
}

type stateStub struct {
	Project struct {
		Status string `yaml:"status"`
	} `yaml:"project"`
	Pending *struct{} `yaml:"pending"`
}

type promptCacheStub struct {
	UpdatedAt time.Time `yaml:"updated_at"`
	State     string    `yaml:"state"`
}

type stylesStub struct {
	Colors struct {
		Accent  string `yaml:"accent"`
		Success string `yaml:"success"`
		Warning string `yaml:"warning"`
		Danger  string `yaml:"danger"`
		Muted   string `yaml:"muted"`
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

type stackKind int

const (
	stackNone stackKind = iota
	stackRunning
	stackPartial
	stackStopped
)

func (s stackKind) icon() string {
	switch s {
	case stackRunning:
		return "●"
	case stackPartial:
		return "◐"
	case stackStopped:
		return "○"
	}
	return ""
}

func (s stackKind) color(p palette) color {
	switch s {
	case stackRunning:
		return p.success
	case stackPartial:
		return p.warning
	case stackStopped:
		return p.muted
	}
	return color{}
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
	muted   color
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
	composeName := readComposeProjectName(root, name)

	status := readStatus(root)
	pal := readPalette(root)
	service := detectService(cwd, root)
	stack := readStack(root, composeName, time.Now())

	out := render(name, service, status, stack, pal, useColor)
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

func render(name, service string, status statusKind, stack stackKind, pal palette, useColor bool) string {
	var sb strings.Builder
	sb.Grow(96)
	sb.WriteByte('{')
	writeGlyph(&sb, "▪", pal.accent, useColor)
	sb.WriteString("} ")
	sb.WriteString(sanitizeName(name))
	if service != "" {
		sb.WriteString(" [")
		sb.WriteString(sanitizeName(service))
		sb.WriteByte(']')
	}
	if icon := status.icon(); icon != "" {
		sb.WriteByte(' ')
		writeGlyph(&sb, icon, statusColor(status, pal), useColor)
	}
	if icon := stack.icon(); icon != "" {
		sb.WriteByte(' ')
		writeGlyph(&sb, icon, stack.color(pal), useColor)
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
		muted:   resolveColor(stub.Colors.Muted, defaultMuted),
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

// readCache reads <root>/.dwe/prompt-cache.yml. Returns ok=false on any I/O,
// parse, or unknown-state-string error. UpdatedAt zero with ok=true is allowed
// (caller treats a zero timestamp as immediately stale).
func readCache(path string) (state stackKind, updatedAt time.Time, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return stackNone, time.Time{}, false
	}
	var stub promptCacheStub
	if err := yaml.Unmarshal(data, &stub); err != nil {
		return stackNone, time.Time{}, false
	}
	switch stub.State {
	case "running":
		state = stackRunning
	case "partial":
		state = stackPartial
	case "stopped":
		state = stackStopped
	default:
		return stackNone, time.Time{}, false
	}
	return state, stub.UpdatedAt, true
}

// readStack returns the current stack state for the rendered prompt. When a
// fresh cache (<cacheTTL old) exists, returns its value. Otherwise it shells
// out via refreshStack and applies the no-downgrade rule: the cache is
// rewritten only when the refreshed state is stackRunning. On refresh failure
// or a zero-result (stackStopped), the cache is left untouched and the stale
// value (if any) is returned. composeProject is the Docker Compose project name
// (may differ from project.name when a prefix or docker.yml project_name is set).
func readStack(root, composeProject string, now time.Time) stackKind {
	path := filepath.Join(root, promptCacheRelPath)
	cachedState, updatedAt, cacheOK := readCache(path)
	if cacheOK {
		age := now.Sub(updatedAt)
		if age >= 0 && age <= cacheTTL {
			return cachedState
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()
	fresh, refreshOK := refreshStack(ctx, composeProject)
	if !refreshOK {
		if cacheOK {
			return cachedState
		}
		return stackNone
	}
	if fresh == stackRunning {
		_ = writeCache(path, fresh, now)
		return fresh
	}
	// Zero-result: never write stopped from prompt refresh. Return stale if any.
	if cacheOK {
		return cachedState
	}
	return stackNone
}

// refreshStack invokes dockerPsFunc with the given context. Returns
// (stackRunning, true) when at least one container matches the compose project
// label, (stackStopped, true) on zero matches, and (stackNone, false) on any
// exec or context-timeout failure. Never returns stackPartial — prompt refresh
// is binary.
func refreshStack(ctx context.Context, composeProject string) (stackKind, bool) {
	out, err := dockerPsFunc(ctx, composeProject)
	if err != nil {
		return stackNone, false
	}
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return stackStopped, true
	}
	return stackRunning, true
}

// writeCache atomically rewrites <root>/.dwe/prompt-cache.yml with the given
// state and timestamp. Best-effort: returns an error but callers ignore it.
// Skips writes when state == stackNone (never poisons the cache).
func writeCache(path string, state stackKind, now time.Time) error {
	var s string
	switch state {
	case stackRunning:
		s = "running"
	case stackPartial:
		s = "partial"
	case stackStopped:
		s = "stopped"
	default:
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := "updated_at: " + now.UTC().Format(time.RFC3339) + "\nstate: " + s + "\n"
	tmp, err := os.CreateTemp(dir, "prompt-cache-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
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

// serviceDirStub is the minimal slice of service.yml needed by detectService.
type serviceDirStub struct {
	Dir     string `yaml:"dir"`
	Extends string `yaml:"extends"`
}

// detectService returns the name of the service whose source directory
// (`dir:` field in <root>/workspace/services/<name>/service.yml, resolved
// relative to root) contains cwd. When multiple services match (nested dirs),
// the deepest match wins. Returns "" when no service matches or when
// workspace/services/ is missing/unreadable.
//
// Two-pass: read every service.yml stub once, then walk the `extends:` chain
// so a child that inherits `dir:` from a parent is still detectable. Cycles
// short-circuit safely via a per-walk seen-set.
//
// Skips silently when resolved dir would be the project root or escape it
// (`dir: .`, `dir: ..`, absolute paths outside root) — those would otherwise
// tag every cwd in (or above) the project. Does NOT resolve symlinks
// (mirrors findRoot's policy).
func detectService(cwd, root string) string {
	servicesRoot := filepath.Join(root, "workspace", "services")
	entries, err := os.ReadDir(servicesRoot)
	if err != nil {
		return ""
	}

	type stubInfo struct {
		dir     string
		extends string
	}
	stubs := make(map[string]stubInfo, len(entries))
	// maxServiceYAMLBytes guards against pathological service.yml files on the
	// prompt hot path: a generated or YAML-anchor-bomb file would otherwise
	// freeze the shell. Typical service.yml is <2 KB.
	const maxServiceYAMLBytes = 64 * 1024
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(servicesRoot, e.Name(), "service.yml")
		fi, err := os.Stat(path)
		if err != nil || fi.Size() > maxServiceYAMLBytes {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var stub serviceDirStub
		if err := yaml.Unmarshal(data, &stub); err != nil {
			continue
		}
		stubs[e.Name()] = stubInfo{dir: stub.Dir, extends: stub.Extends}
	}

	// resolveDir walks the extends chain until it finds a non-empty dir.
	// The seen map provides cycle safety; no depth cap (the main config loader
	// supports arbitrary acyclic chains via topo-sort, the prompt matches).
	resolveDir := func(name string) string {
		seen := make(map[string]struct{})
		for {
			info, ok := stubs[name]
			if !ok {
				return ""
			}
			if info.dir != "" {
				return info.dir
			}
			if info.extends == "" {
				return ""
			}
			if _, dup := seen[name]; dup {
				return ""
			}
			seen[name] = struct{}{}
			name = info.extends
		}
	}

	sep := string(filepath.Separator)
	cwdClean := filepath.Clean(cwd)
	rootClean := filepath.Clean(root)
	rootPrefix := rootClean + sep
	var bestName string
	var bestLen int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		dir := resolveDir(name)
		if dir == "" {
			continue
		}
		var resolved string
		if filepath.IsAbs(dir) {
			resolved = filepath.Clean(dir)
		} else {
			resolved = filepath.Clean(filepath.Join(rootClean, dir))
		}
		// Reject paths equal to root (e.g. `dir: .`) or outside root
		// (`dir: ..`, absolute paths outside the project) — they would
		// tag every cwd in the project or other projects.
		if resolved == rootClean || !strings.HasPrefix(resolved, rootPrefix) {
			continue
		}
		if cwdClean != resolved && !strings.HasPrefix(cwdClean, resolved+sep) {
			continue
		}
		if len(resolved) > bestLen {
			bestName = name
			bestLen = len(resolved)
		}
	}
	return bestName
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

// readComposeProjectName returns the Docker Compose project name used to label
// containers (the value Docker writes to the com.docker.compose.project label).
// It mirrors the precedence of config.ResolveComposeProjectName:
//  1. workspace/docker.yml project_name (literal only; template values fall back)
//  2. project.prefix + "-" + project.name when prefix is set
//  3. project.name (same as displayName)
//
// displayName is the already-resolved display name so workspace.yml need not be
// re-read. The return value is always non-empty.
func readComposeProjectName(root, displayName string) string {
	if dockerName := readDockerProjectNameLiteral(root); dockerName != "" {
		return dockerName
	}
	data, err := os.ReadFile(filepath.Join(root, configFilename))
	if err != nil {
		return displayName
	}
	var stub workspaceStub
	if err := yaml.Unmarshal(data, &stub); err != nil {
		return displayName
	}
	if stub.Project.Prefix != "" {
		return stub.Project.Prefix + "-" + displayName
	}
	return displayName
}

// readDockerProjectNameLiteral reads project_name from workspace/docker.yml.
// Returns empty string if the file is absent, unreadable, the field is absent,
// or the value contains template syntax (${...}) that the prompt cannot resolve
// without the full config loader.
func readDockerProjectNameLiteral(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "workspace", "docker.yml"))
	if err != nil {
		return ""
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return ""
	}
	name, _ := m["project_name"].(string)
	if strings.Contains(name, "${") {
		return ""
	}
	return name
}
