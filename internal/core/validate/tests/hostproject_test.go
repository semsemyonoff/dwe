package tests

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// hpProject writes files (project-relative path → content) under a fresh root
// that always has a workspace/tests/ directory.
func hpProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workspace", "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root, files)
	return root
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func runHP(root string) []validate.Diagnostic {
	return (&hostProjectNameValidator{}).Run(validate.Context{ProjectRoot: root, Cfg: baseCfg()})
}

// hpLines renders each diagnostic as "file|line|message" for compact asserts.
func hpLines(diags []validate.Diagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		out = append(out, d.File+"|"+strconv.Itoa(d.Line)+"|"+d.Message)
	}
	return out
}

// requireOne asserts exactly one diagnostic and that it carries file, line
// and a message containing every want fragment.
func requireOne(t *testing.T, diags []validate.Diagnostic, file string, line int, want ...string) {
	t.Helper()
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d: %v", len(diags), hpLines(diags))
	}
	d := diags[0]
	if d.Severity != validate.SeverityWarning || d.Domain != "tests" || d.Target != "tests.host_project_name" {
		t.Errorf("diagnostic shape = %+v", d)
	}
	if d.File != file || d.Line != line {
		t.Errorf("file/line = %s:%d, want %s:%d (%s)", d.File, d.Line, file, line, d.Message)
	}
	for _, w := range want {
		if !strings.Contains(d.Message, w) {
			t.Errorf("message %q does not contain %q", d.Message, w)
		}
	}
	if !strings.Contains(d.Hint, "${COMPOSE_PROJECT_NAME:-") {
		t.Errorf("hint %q does not name the fix", d.Hint)
	}
}

const offendingCmd = `docker compose -p "${A:-dwe}-x" up -d`

func TestHostProjectName_IDAndDomain(t *testing.T) {
	v := &hostProjectNameValidator{}
	if v.ID() != "host_project_name" || v.Domain() != "tests" {
		t.Errorf("ID/Domain = %q/%q", v.ID(), v.Domain())
	}
	var found bool
	for _, val := range All() {
		if _, ok := val.(*hostProjectNameValidator); ok {
			found = true
		}
	}
	if !found {
		t.Error("All() does not register hostProjectNameValidator")
	}
}

func TestHostProjectName_NoTestsDir(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"workspace/commands/db.yml": "commands:\n  seed:\n    type: shell\n    cmd: '" + offendingCmd + "'\n",
	})
	if diags := runHP(root); len(diags) != 0 {
		t.Fatalf("want no diagnostics without workspace/tests/, got %v", hpLines(diags))
	}
}

func TestHostProjectName_Sources(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		file  string
		line  int
		want  []string
	}{
		{
			name: "shell command cmd",
			files: map[string]string{
				"workspace/commands/db.yml": "commands:\n  seed:\n    type: shell\n    cmd: |\n      echo start\n      " + offendingCmd + "\n",
			},
			file: "workspace/commands/db.yml",
			want: []string{"command db.seed, cmd line 2", `"${A:-dwe}-x"`},
		},
		{
			name: "shell command argv with -c payload",
			files: map[string]string{
				"workspace/commands/db.yml": "commands:\n  seed:\n    type: shell\n    argv: [sh, -c, 'docker compose -p \"${A}-x\" up']\n",
			},
			file: "workspace/commands/db.yml",
			want: []string{"command db.seed, cmd line 1"},
		},
		{
			name: "hidden command",
			files: map[string]string{
				"workspace/commands/db.yml": "commands:\n  seed:\n    type: shell\n    hide: \"true\"\n    cmd: '" + offendingCmd + "'\n",
			},
			file: "workspace/commands/db.yml",
			want: []string{"command db.seed"},
		},
		{
			name: "script path",
			files: map[string]string{
				"workspace/commands/db.yml": "commands:\n  seed:\n    type: script\n    script:\n      path: scripts/seed.sh\n",
				"scripts/seed.sh":           "#!/bin/sh\nset -e\n" + offendingCmd + "\n",
			},
			file: "scripts/seed.sh",
			line: 3,
			want: []string{"line 3:"},
		},
		{
			name: "script phased run",
			files: map[string]string{
				"workspace/commands/db.yml": "commands:\n  seed:\n    type: script\n    script:\n      shell: bash\n      plan: scripts/plan.sh\n      run: scripts/run.sh\n",
				"scripts/plan.sh":           "echo plan\n",
				"scripts/run.sh":            offendingCmd + "\n",
			},
			file: "scripts/run.sh",
			line: 1,
		},
		{
			name: "project deploy step",
			files: map[string]string{
				"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: '" + offendingCmd + "'\n",
			},
			file: "workspace/deploy.yml",
			want: []string{"step deploy/seed, cmd line 1"},
		},
		{
			name: "shell check",
			files: map[string]string{
				"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: 'true'\n        check:\n          type: shell\n          cmd: '" + offendingCmd + "'\n",
			},
			file: "workspace/deploy.yml",
			want: []string{"step deploy/seed check, cmd line 1"},
		},
		{
			name: "parallel sub-step",
			files: map[string]string{
				"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: grp\n        parallel:\n          steps:\n            - name: a\n              type: shell\n              cmd: 'true'\n            - name: b\n              type: shell\n              cmd: '" + offendingCmd + "'\n",
			},
			file: "workspace/deploy.yml",
			want: []string{"step deploy/grp/b, cmd line 1"},
		},
		{
			name: "per-service deploy",
			files: map[string]string{
				"workspace/services/web/deploy.yml": "phases:\n  - name: build\n    steps:\n      - name: up\n        type: shell\n        cmd: '" + offendingCmd + "'\n",
			},
			file: "workspace/services/web/deploy.yml",
			want: []string{"step build/up"},
		},
		{
			name: "per-service reset",
			files: map[string]string{
				"workspace/services/web/reset.yml": "phases:\n  - name: wipe\n    steps:\n      - name: down\n        type: shell\n        cmd: '" + offendingCmd + "'\n",
			},
			file: "workspace/services/web/reset.yml",
			want: []string{"step wipe/down"},
		},
		{
			name: "project reset",
			files: map[string]string{
				"workspace/reset.yml": "phases:\n  - name: wipe\n    steps:\n      - name: down\n        type: shell\n        cmd: '" + offendingCmd + "'\n",
			},
			file: "workspace/reset.yml",
			want: []string{"step wipe/down"},
		},
		{
			name: "lifecycle run",
			files: map[string]string{
				"workspace/lifecycle.yml": "run:\n  phases:\n    - name: start\n      steps:\n        - name: up\n          type: shell\n          cmd: '" + offendingCmd + "'\n",
			},
			file: "workspace/lifecycle.yml",
			want: []string{"step run/start/up"},
		},
		{
			name: "lifecycle stop",
			files: map[string]string{
				"workspace/lifecycle.yml": "stop:\n  phases:\n    - name: halt\n      steps:\n        - name: down\n          type: shell\n          cmd: '" + offendingCmd + "'\n",
			},
			file: "workspace/lifecycle.yml",
			want: []string{"step stop/halt/down"},
		},
		{
			name: "scenario step",
			files: map[string]string{
				"workspace/tests/smoke.yml": "steps:\n  - name: probe\n    type: shell\n    cmd: '" + offendingCmd + "'\n",
			},
			file: "workspace/tests/smoke.yml",
			want: []string{"step probe, cmd line 1"},
		},
		{
			name: "file referenced from a step cmd",
			files: map[string]string{
				"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: 'bash -e scripts/seed.sh --force'\n",
				"scripts/seed.sh":      "echo seeding\n" + offendingCmd + "\n",
			},
			file: "scripts/seed.sh",
			line: 2,
		},
		{
			name: "directly executed file with an env bash shebang",
			files: map[string]string{
				"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: 'cd . && ./bin/seed'\n",
				"bin/seed":             "#!/usr/bin/env bash\n" + offendingCmd + "\n",
			},
			file: "bin/seed",
			line: 2,
		},
		{
			name: "motivating shape through a variable",
			files: map[string]string{
				"scripts/db.sh":             "PROJECT=\"${PROJECT_PREFIX:-dwe}-${PROJECT_NAME:-myproj}\"\ndocker compose -p \"$PROJECT\" exec db psql\n",
				"workspace/commands/db.yml": "commands:\n  psql:\n    type: script\n    script:\n      path: scripts/db.sh\n",
			},
			file: "scripts/db.sh",
			line: 2,
			want: []string{`"${PROJECT_PREFIX:-dwe}-${PROJECT_NAME:-myproj}"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := hpProject(t, tt.files)
			requireOne(t, runHP(root), tt.file, tt.line, tt.want...)
		})
	}
}

func TestHostProjectName_NotScanned(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "argv exec'd without a shell",
			files: map[string]string{
				"workspace/commands/db.yml": "commands:\n  seed:\n    type: shell\n    argv: [docker, compose, -p, \"$X-x\", up]\n",
			},
		},
		{
			name: "argv with -lc is not the -c form",
			files: map[string]string{
				"workspace/commands/db.yml": "commands:\n  seed:\n    type: shell\n    argv: [bash, -lc, '" + offendingCmd + "']\n",
			},
		},
		{
			name: "container-side command",
			files: map[string]string{
				"workspace/commands/db.yml": "commands:\n  seed:\n    type: service_exec\n    service: web\n    cmd: '" + offendingCmd + "'\n",
			},
		},
		{
			name: "non-POSIX script shell",
			files: map[string]string{
				"workspace/commands/db.yml": "commands:\n  seed:\n    type: script\n    script:\n      shell: python3\n      path: scripts/seed.sh\n",
				"scripts/seed.sh":           offendingCmd + "\n",
			},
		},
		{
			name: "directly executed file with a python shebang",
			files: map[string]string{
				"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: './bin/seed.py'\n",
				"bin/seed.py":          "#!/usr/bin/env python3\n" + offendingCmd + "\n",
			},
		},
		{
			name: "missing referenced file",
			files: map[string]string{
				"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: 'sh scripts/missing.sh'\n",
			},
		},
		{
			name: "referenced path with an expansion",
			files: map[string]string{
				"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: 'sh ${DIR}/seed.sh'\n",
				"seed.sh":              offendingCmd + "\n",
			},
		},
		{
			name: "sh -c operand is inline text, not a file",
			files: map[string]string{
				"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: 'sh -c seed.sh'\n",
				"seed.sh":              offendingCmd + "\n",
			},
		},
		{
			name: "nested reference is deeper than one level",
			files: map[string]string{
				"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: 'sh scripts/outer.sh'\n",
				"scripts/outer.sh":     "sh scripts/inner.sh\n",
				"scripts/inner.sh":     offendingCmd + "\n",
			},
		},
		{
			name: "non-shell step types",
			files: map[string]string{
				"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: dwe\n        cmd: 'cmd db.seed'\n",
			},
		},
		{
			name: "broken pipeline file is skipped",
			files: map[string]string{
				"workspace/deploy.yml": "phases: [oops\n",
			},
		},
		{
			name: "clean project",
			files: map[string]string{
				"workspace/commands/db.yml": "commands:\n  seed:\n    type: shell\n    cmd: 'docker compose -p \"${COMPOSE_PROJECT_NAME:-dwe-x}\" up'\n",
				"scripts/db.sh":             "PROJECT=\"${COMPOSE_PROJECT_NAME:-${PROJECT_PREFIX:-dwe}-${PROJECT_NAME:-myproj}}\"\ndocker compose -p \"$PROJECT\" exec db psql\n",
				"workspace/deploy.yml":      "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: 'sh scripts/db.sh'\n",
				"workspace/tests/smoke.yml": "steps:\n  - name: probe\n    type: shell\n    cmd: 'docker compose -p \"$COMPOSE_PROJECT_NAME\" ps'\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := hpProject(t, tt.files)
			if diags := runHP(root); len(diags) != 0 {
				t.Fatalf("want no diagnostics, got %v", hpLines(diags))
			}
		})
	}
}

func TestHostProjectName_TwoStepsInOneFile(t *testing.T) {
	root := hpProject(t, map[string]string{
		"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: a\n        type: shell\n        cmd: '" + offendingCmd + "'\n      - name: b\n        type: shell\n        cmd: '" + offendingCmd + "'\n",
	})
	diags := runHP(root)
	if len(diags) != 2 {
		t.Fatalf("want 2 diagnostics, got %v", hpLines(diags))
	}
	if !strings.Contains(diags[0].Message, "step deploy/a,") || !strings.Contains(diags[1].Message, "step deploy/b,") {
		t.Errorf("messages = %v", hpLines(diags))
	}
}

func TestHostProjectName_ScriptReachedTwiceReportedOnce(t *testing.T) {
	root := hpProject(t, map[string]string{
		"workspace/commands/db.yml": "commands:\n  seed:\n    type: script\n    script:\n      path: scripts/seed.sh\n  again:\n    type: shell\n    cmd: 'bash ./scripts/seed.sh'\n",
		"workspace/deploy.yml":      "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: 'sh scripts/seed.sh'\n",
		"scripts/seed.sh":           offendingCmd + "\n",
	})
	requireOne(t, runHP(root), "scripts/seed.sh", 1)
}

func TestHostProjectName_NilRegistryStillScansCommands(t *testing.T) {
	root := hpProject(t, map[string]string{
		"workspace/commands/db.yml": "commands:\n  seed:\n    type: shell\n    cmd: '" + offendingCmd + "'\n",
	})
	diags := (&hostProjectNameValidator{}).Run(validate.Context{ProjectRoot: root, Cfg: baseCfg(), CommandRegistry: nil})
	requireOne(t, diags, "workspace/commands/db.yml", 0, "command db.seed")
}

func TestHostProjectName_NilCfg(t *testing.T) {
	root := hpProject(t, map[string]string{
		"workspace/commands/db.yml":         "commands:\n  seed:\n    type: shell\n    cmd: '" + offendingCmd + "'\n",
		"workspace/deploy.yml":              "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: '" + offendingCmd + "'\n",
		"workspace/services/web/deploy.yml": "phases:\n  - name: build\n    steps:\n      - name: up\n        type: shell\n        cmd: '" + offendingCmd + "'\n",
	})
	diags := (&hostProjectNameValidator{}).Run(validate.Context{ProjectRoot: root})
	files := map[string]bool{}
	for _, d := range diags {
		files[d.File] = true
	}
	if !files["workspace/commands/db.yml"] || !files["workspace/deploy.yml"] {
		t.Errorf("command file and project deploy.yml must still be scanned, got %v", hpLines(diags))
	}
	if files["workspace/services/web/deploy.yml"] {
		t.Errorf("per-service deploy.yml needs Cfg.Services and must be skipped, got %v", hpLines(diags))
	}
}

func TestHostProjectName_CleanProjectKeepsScenarioOKRows(t *testing.T) {
	root := hpProject(t, map[string]string{
		"workspace/tests/smoke.yml": "steps:\n  - name: probe\n    type: shell\n    cmd: 'true'\n",
	})
	var ok, other int
	for _, v := range All() {
		for _, d := range v.Run(validate.Context{ProjectRoot: root, Cfg: baseCfg()}) {
			if d.Severity == validate.SeverityOK && d.Target == "tests.smoke" {
				ok++
			} else {
				other++
			}
		}
	}
	if ok != 1 || other != 0 {
		t.Errorf("want exactly the scenario's OK row, got ok=%d other=%d", ok, other)
	}
}

// TestHostProjectName_FileConfinement pins that the validator reads nothing
// outside the project and nothing that is not a small regular file.
func TestHostProjectName_FileConfinement(t *testing.T) {
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "evil.sh")
	if err := os.WriteFile(outsideFile, []byte(offendingCmd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("# padding\n", (maxScannedFileSize/10)+1) + offendingCmd + "\n"

	tests := []struct {
		name  string
		cmd   string
		setup func(t *testing.T, root string)
	}{
		{name: "absolute path", cmd: "sh " + outsideFile},
		{name: "parent escape", cmd: "sh ../escape-target/evil.sh", setup: func(t *testing.T, root string) {
			// Place the outside file next to root so the ../ path resolves.
			sibling := filepath.Join(filepath.Dir(root), "escape-target")
			if err := os.MkdirAll(sibling, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sibling, "evil.sh"), []byte(offendingCmd+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink pointing outside", cmd: "sh scripts/link.sh", setup: func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outsideFile, filepath.Join(root, "scripts", "link.sh")); err != nil {
				t.Skipf("symlink: %v", err)
			}
		}},
		{name: "directory", cmd: "sh scripts", setup: func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "file over 1 MiB", cmd: "sh big.sh", setup: func(t *testing.T, root string) {
			writeFiles(t, root, map[string]string{"big.sh": big})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := hpProject(t, map[string]string{
				"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: '" + tt.cmd + "'\n",
			})
			if tt.setup != nil {
				tt.setup(t, root)
			}
			if diags := runHP(root); len(diags) != 0 {
				t.Fatalf("want no diagnostics, got %v", hpLines(diags))
			}
		})
	}
}

func TestHostProjectName_SymlinkInsideRootIsScanned(t *testing.T) {
	root := hpProject(t, map[string]string{
		"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: 'sh scripts/link.sh'\n",
		"scripts/real.sh":      offendingCmd + "\n",
	})
	if err := os.Symlink("real.sh", filepath.Join(root, "scripts", "link.sh")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	requireOne(t, runHP(root), "scripts/link.sh", 1)
}

func TestHostProjectName_UnreadableFileIgnored(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	root := hpProject(t, map[string]string{
		"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: 'sh scripts/seed.sh'\n",
		"scripts/seed.sh":      offendingCmd + "\n",
	})
	p := filepath.Join(root, "scripts", "seed.sh")
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	if diags := runHP(root); len(diags) != 0 {
		t.Fatalf("want no diagnostics, got %v", hpLines(diags))
	}
}

func TestScriptReferences(t *testing.T) {
	tests := []struct {
		text string
		want []scriptRef
	}{
		{"sh scripts/a.sh", []scriptRef{{path: "scripts/a.sh"}}},
		{"bash -e -o pipefail scripts/a.sh arg", []scriptRef{{path: "scripts/a.sh"}}},
		{"/bin/bash -- scripts/a.sh", []scriptRef{{path: "scripts/a.sh"}}},
		{`X=1 sudo -E bash "scripts/a b.sh"`, []scriptRef{{path: "scripts/a b.sh"}}},
		{"./bin/run --flag", []scriptRef{{path: "./bin/run", checkShebang: true}}},
		{"cd x && ./run; sh y.sh", []scriptRef{{path: "./run", checkShebang: true}, {path: "y.sh"}}},
		{"bash -c 'x.sh'", nil},
		{"bash -ec x.sh", nil},
		{"sh $DIR/a.sh", nil},
		{"$SHELL a.sh", nil},
		{"python3 a.py", nil},
		{"echo sh a.sh", nil},
		{"sh", nil},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := scriptReferences(tt.text)
			if len(got) != len(tt.want) {
				t.Fatalf("scriptReferences(%q) = %+v, want %+v", tt.text, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("scriptReferences(%q)[%d] = %+v, want %+v", tt.text, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestPosixShebang(t *testing.T) {
	tests := []struct {
		data string
		want bool
	}{
		{"echo hi\n", true},
		{"#!/bin/sh\n", true},
		{"#!/bin/bash -e\n", true},
		{"#! /usr/bin/env bash\n", true},
		{"#!/usr/bin/env -S zsh -f\n", true},
		{"#!/usr/bin/env python3\n", false},
		{"#!/usr/bin/node\n", false},
		{"#!\n", false},
	}
	for _, tt := range tests {
		if got := posixShebang([]byte(tt.data)); got != tt.want {
			t.Errorf("posixShebang(%q) = %v, want %v", tt.data, got, tt.want)
		}
	}
}

func TestShellArgvPayload(t *testing.T) {
	tests := []struct {
		argv []string
		want string
		ok   bool
	}{
		{[]string{"sh", "-c", "x"}, "x", true},
		{[]string{"/bin/bash", "-c", "x", "arg0"}, "x", true},
		{[]string{"bash", "-lc", "x"}, "", false},
		{[]string{"docker", "compose", "-p", "x"}, "", false},
		{[]string{"python3", "-c", "x"}, "", false},
		{[]string{"sh", "-c"}, "", false},
	}
	for _, tt := range tests {
		got, ok := shellArgvPayload(tt.argv)
		if got != tt.want || ok != tt.ok {
			t.Errorf("shellArgvPayload(%v) = %q, %v; want %q, %v", tt.argv, got, ok, tt.want, tt.ok)
		}
	}
}
