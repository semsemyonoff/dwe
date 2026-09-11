package tests

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/loader"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/core/workflow/envtest"
	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"
)

// maxScannedFileSize caps a script file the validator reads; a larger file is
// skipped whole rather than scanned in part.
const maxScannedFileSize = 1 << 20

// posixShells are the interpreters whose text the scanner understands. A
// script run by anything else (python, node, …) is never scanned.
var posixShells = map[string]bool{"sh": true, "bash": true, "dash": true, "ksh": true, "zsh": true}

// hostProjectNameValidator warns when a host script or shell step hands docker
// compose a project name not derived from $COMPOSE_PROJECT_NAME: inside
// `dwe test` such a name addresses the live stack instead of the copy.
//
// Command sources are parsed from the command files, not taken from the
// registry: the registry keeps no source file per command and, inside a
// container, drops BridgeHidden commands — `dwe validate` would then scan a
// different set depending on where it runs.
type hostProjectNameValidator struct{}

var _ validate.Validator = (*hostProjectNameValidator)(nil)

func (v *hostProjectNameValidator) ID() string     { return "host_project_name" }
func (v *hostProjectNameValidator) Domain() string { return "tests" }

// Run gates only on workspace/tests/: the warning is about `dwe test`, so a
// project without scenarios never sees it. A nil ctx.Cfg skips per-service
// deploy.yml files only (their discovery needs Cfg.Services); every other
// source loads standalone. A source that fails to parse or load is skipped —
// the validators owning that file report it.
func (v *hostProjectNameValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if fi, err := os.Stat(envtest.TestsDir(ctx.ProjectRoot)); err != nil || !fi.IsDir() {
		return nil
	}
	s := newHostProjectScan(ctx.ProjectRoot)
	s.scanCommands()
	s.scanPipelines(ctx.Cfg)
	return s.diags
}

type hostProjectScan struct {
	root     string
	realRoot string          // root with symlinks resolved; "" disables file reads
	scanned  map[string]bool // real paths of files already scanned
	reported map[string]bool // file, location, line
	diags    []validate.Diagnostic
}

func newHostProjectScan(root string) *hostProjectScan {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	// On macOS a temp root sits under the symlinked /var, so both sides of the
	// containment check must be resolved.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = ""
	}
	return &hostProjectScan{
		root:     root,
		realRoot: realRoot,
		scanned:  map[string]bool{},
		reported: map[string]bool{},
	}
}

// scanCommands scans every `type: shell` and `type: script` user command,
// hidden ones included.
func (s *hostProjectScan) scanCommands() {
	dir := filepath.Join(s.root, "workspace", "commands")
	paths, err := loader.DiscoverCommandFiles(dir)
	if err != nil {
		return
	}
	for _, p := range paths {
		cf, err := loader.ParseCommandFile(p, dir)
		if err != nil {
			continue
		}
		file := relPath(s.root, p)
		for _, name := range slices.Sorted(maps.Keys(cf.Commands)) {
			def := cf.Commands[name]
			switch def.Type {
			case model.CommandTypeShell:
				location := "command " + def.ID
				if def.Cmd != "" {
					s.scanInline(file, location, def.Cmd)
				} else if payload, ok := shellArgvPayload(def.Argv); ok {
					s.scanInline(file, location, payload)
				}
			case model.CommandTypeScript:
				if def.Script == nil {
					continue
				}
				shell := def.Script.Shell
				if shell == "" {
					shell = "sh" // the script runner's default
				}
				if !posixShells[path.Base(shell)] {
					continue
				}
				for _, f := range []string{def.Script.Path, def.Script.Plan, def.Script.Run, def.Script.Cleanup} {
					s.scanFile(f, false)
				}
			}
		}
	}
}

// scanPipelines scans the shell steps and shell `check:` of every pipeline
// file and scenario, in a fixed order so the diagnostics are stable.
func (s *hostProjectScan) scanPipelines(cfg *config.DweConfig) {
	ws := filepath.Join(s.root, "workspace")

	deployPath := filepath.Join(ws, "deploy.yml")
	if c, err := config.LoadProjectDeployConfig(deployPath); err == nil {
		s.scanPhases(relPath(s.root, deployPath), "", c.Phases)
	}
	resetPath := filepath.Join(ws, "reset.yml")
	if c, err := config.LoadResetConfig(resetPath); err == nil {
		s.scanPhases(relPath(s.root, resetPath), "", c.Phases)
	}
	lifecyclePath := filepath.Join(ws, "lifecycle.yml")
	if c, err := config.LoadLifecycleConfig(lifecyclePath); err == nil {
		file := relPath(s.root, lifecyclePath)
		if c.Run != nil {
			s.scanPhases(file, "run/", c.Run.Phases)
		}
		if c.Stop != nil {
			s.scanPhases(file, "stop/", c.Stop.Phases)
		}
	}

	if cfg != nil {
		for _, name := range slices.Sorted(maps.Keys(cfg.Services)) {
			p := filepath.Join(ws, "services", name, "deploy.yml")
			if c, err := config.LoadServiceDeployConfig(p); err == nil {
				s.scanPhases(relPath(s.root, p), "", c.Phases)
			}
		}
	}
	// Loaded per folder rather than through LoadServiceResetConfigs, whose
	// joined error would drop every service's reset.yml over one broken file.
	for _, name := range dirNames(filepath.Join(ws, "services")) {
		if c, err := config.LoadServiceResetConfig(s.root, name); err == nil && c != nil {
			s.scanPhases(relPath(s.root, filepath.Join(ws, "services", name, "reset.yml")), "", c.Phases)
		}
	}

	testsDir := envtest.TestsDir(s.root)
	entries, err := os.ReadDir(testsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || (filepath.Ext(e.Name()) != ".yml" && filepath.Ext(e.Name()) != ".yaml") {
			continue
		}
		p := filepath.Join(testsDir, e.Name())
		if scn, err := envtest.LoadScenario(p); err == nil {
			s.scanSteps(relPath(s.root, p), "", scn.Steps)
		}
	}
}

func (s *hostProjectScan) scanPhases(file, prefix string, phases []config.DeployPhase) {
	for i, ph := range phases {
		label := ph.Name
		if label == "" {
			label = fmt.Sprintf("phases[%d]", i)
		}
		s.scanSteps(file, prefix+label+"/", ph.Steps)
	}
}

func (s *hostProjectScan) scanSteps(file, prefix string, steps []config.DeployStep) {
	for i, st := range steps {
		label := st.Name
		if label == "" {
			label = fmt.Sprintf("steps[%d]", i)
		}
		addr := prefix + label
		if st.Parallel != nil {
			s.scanSteps(file, addr+"/", st.Parallel.Steps)
			continue
		}
		if st.Type == "shell" {
			s.scanInline(file, "step "+addr, st.Cmd)
		}
		if st.Check != nil && st.Check.Type == "shell" {
			s.scanInline(file, "step "+addr+" check", st.Check.Cmd)
		}
	}
}

// scanInline scans YAML-embedded shell text, then every script file the text
// runs — one level, never the files those scripts run.
func (s *hostProjectScan) scanInline(file, location, text string) {
	for _, h := range scanShellText(text) {
		s.report(file, location, h)
	}
	for _, ref := range scriptReferences(text) {
		s.scanFile(ref.path, ref.checkShebang)
	}
}

// scanFile scans a project-relative script file once. checkShebang is set for
// a file executed directly (`./x`), whose interpreter only its shebang names.
func (s *hostProjectScan) scanFile(ref string, checkShebang bool) {
	if ref == "" || strings.ContainsAny(ref, "$`") {
		return
	}
	data, file, real, ok := s.readConfined(ref)
	if !ok || s.scanned[real] {
		return
	}
	if checkShebang && !posixShebang(data) {
		return
	}
	s.scanned[real] = true
	for _, h := range scanShellText(string(data)) {
		s.report(file, "", h)
	}
}

// readConfined reads a project-relative file only when it provably stays
// inside the project: not absolute, lexically contained, its real path under
// the real root, a regular file of at most maxScannedFileSize. Any failure is
// a silent skip — the validator reads the project and nothing else. rel is the
// cleaned project-relative path (the name reported), real the resolved one
// (the scan-once key).
func (s *hostProjectScan) readConfined(ref string) (data []byte, rel, real string, ok bool) {
	// Checked before joining: Join(root, "/etc/x") lands inside the root.
	if s.realRoot == "" || filepath.IsAbs(ref) {
		return nil, "", "", false
	}
	joined := filepath.Join(s.root, ref)
	rel, err := pathsafe.ContainedRel(s.root, joined)
	if err != nil {
		return nil, "", "", false
	}
	real, err = filepath.EvalSymlinks(joined)
	if err != nil || pathsafe.EnsureRealUnder(real, s.realRoot) != nil {
		return nil, "", "", false
	}
	// Stat, never Open first: opening a FIFO blocks.
	fi, err := os.Stat(real)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxScannedFileSize {
		return nil, "", "", false
	}
	data, err = os.ReadFile(real)
	if err != nil {
		return nil, "", "", false
	}
	return data, rel, real, true
}

// report emits one warning per (file, location, line). For YAML-embedded text
// the line is relative to the cmd string, so it goes into the message only,
// never into Diagnostic.Line.
func (s *hostProjectScan) report(file, location string, h hit) {
	key := fmt.Sprintf("%s\x00%s\x00%d", file, location, h.Line)
	if s.reported[key] {
		return
	}
	s.reported[key] = true

	d := validate.Diagnostic{
		Severity: validate.SeverityWarning,
		Domain:   "tests",
		Target:   "tests.host_project_name",
		File:     file,
		Hint:     fmt.Sprintf(`use "${COMPOSE_PROJECT_NAME:-%s}" — inside dwe test the current value addresses the live stack`, h.Value),
	}
	where := fmt.Sprintf("line %d", h.Line)
	if location != "" {
		where = fmt.Sprintf("%s, cmd line %d", location, h.Line)
	} else {
		d.Line = h.Line
	}
	d.Message = fmt.Sprintf("%s: compose project name %q is not derived from $COMPOSE_PROJECT_NAME", where, h.Value)
	s.diags = append(s.diags, d)
}

// shellArgvPayload returns the script of a POSIX-shell argv whose options
// carry `c` (`[sh, -c, …]`, `[bash, -lc, …]`, `[sh, -e, -c, …]`). Any other
// argv is exec'd without a shell, so `$…` in it never expands, or runs a
// script file, which is not followed.
func shellArgvPayload(argv []string) (string, bool) {
	if len(argv) < 2 || !posixShells[path.Base(argv[0])] {
		return "", false
	}
	operand, inline := shellInvocation(argv[1:])
	if !inline || operand >= len(argv)-1 {
		return "", false
	}
	return argv[1+operand], true
}

// shellLongOptsWithValue are bash long options whose value is the next word.
var shellLongOptsWithValue = map[string]bool{"--rcfile": true, "--init-file": true}

// shellInvocation walks the (unquoted) arguments of a POSIX shell invocation
// and returns the index of its first operand — len(args) when there is none —
// and whether a `c` among the options makes that operand inline script text
// rather than a script file. Options are parsed to the first operand, so
// `bash -c -e 'x'` is inline too; each `o`/`O` in a cluster takes the next
// word (`-eo pipefail`).
func shellInvocation(args []string) (operand int, inline bool) {
	for j := 0; j < len(args); j++ {
		a := args[j]
		switch {
		case a == "--" || a == "-":
			return j + 1, inline
		case strings.HasPrefix(a, "--"):
			if shellLongOptsWithValue[a] {
				j++
			}
		case len(a) > 1 && (a[0] == '-' || a[0] == '+'):
			if a[0] == '-' && strings.Contains(a, "c") {
				inline = true
			}
			j += strings.Count(a, "o") + strings.Count(a, "O")
		default:
			return j, inline
		}
	}
	return len(args), inline
}

type scriptRef struct {
	path         string
	checkShebang bool
}

// scriptReferences returns the files shell text runs as scripts:
// `<posix shell> [opts] <path>` and `./<path>` in command position. A path
// carrying an expansion is untraceable and skipped; so is `<shell> -c …`,
// whose operand is inline text, not a file.
func scriptReferences(text string) []scriptRef {
	var out []scriptRef
	for _, c := range splitSimpleCommands(text) {
		words := c.words
		i := skipReserved(words, 0)
		for i < len(words) {
			if _, ok := parseAssignment(words[i]); !ok {
				break
			}
			i++
		}
		i = skipWrappers(words, i, func(raw string) bool {
			_, ok := parseAssignment(raw)
			return ok
		})
		if i >= len(words) || strings.ContainsAny(words[i], "$`") {
			continue
		}
		cmd := unquoted(words[i])
		if strings.HasPrefix(cmd, "./") {
			out = append(out, scriptRef{path: cmd, checkShebang: true})
			continue
		}
		if !posixShells[path.Base(cmd)] {
			continue
		}
		if ref, ok := shellOperand(words[i+1:]); ok {
			out = append(out, scriptRef{path: ref})
		}
	}
	return out
}

// shellOperand returns the script-file operand after a shell's options, or
// false when there is none or `-c` makes the operand inline text.
func shellOperand(args []string) (string, bool) {
	plain := make([]string, len(args))
	for j, a := range args {
		plain[j] = unquoted(a)
	}
	operand, inline := shellInvocation(plain)
	if inline || operand >= len(args) || strings.ContainsAny(args[operand], "$`") {
		return "", false
	}
	return plain[operand], true
}

// posixShebang reports whether a directly executed file runs under a POSIX
// shell: no shebang (the shell runs it) or one naming a POSIX shell, directly
// or through env.
func posixShebang(data []byte) bool {
	if !bytes.HasPrefix(data, []byte("#!")) {
		return true
	}
	line, _, _ := bytes.Cut(data[2:], []byte("\n"))
	fields := strings.Fields(string(line))
	if len(fields) == 0 {
		return false
	}
	interp := path.Base(fields[0])
	if interp == "env" {
		interp = ""
		for _, f := range fields[1:] {
			if !strings.HasPrefix(f, "-") && !strings.Contains(f, "=") {
				interp = path.Base(f)
				break
			}
		}
	}
	return posixShells[interp]
}

// dirNames returns the sorted names of dir's subdirectories; nil when dir is
// unreadable.
func dirNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}
