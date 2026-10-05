package workspace_test

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/templates/workspace"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

const ralphexStub = `#!/bin/sh
set -eu
case "$1" in
  --version) printf '%s\n' "${RALPHEX_STUB_VERSION:-ralphex v1.7.0}" ;;
  --dump-defaults=*)
    dest=${1#--dump-defaults=}
    mkdir -p "$dest/prompts" "$dest/agents"
    printf 'dumped config %s\n' "${RALPHEX_STUB_CONFIG:-original}" > "$dest/config"
    for name in task review_first review_second codex codex_review finalize custom_eval custom_review make_plan; do
      [ "$name" != "${RALPHEX_STUB_MISSING:-}" ] || continue
      {
        printf '# %s prompt\n# variables: {{PLAN_FILE}} {{DEFAULT_BRANCH}}\n#\n\n' "$name"
        printf 'DEFAULT %s %s\n\n' "$name" "${RALPHEX_STUB_DEFAULTS:-original}"
        printf 'SIGNAL RULES STAY LAST\n'
      } > "$dest/prompts/$name.txt"
    done
    for name in documentation implementation quality simplification testing; do
      {
        if [ "$name" = documentation ]; then
          printf '# agent comments\n# preserved\n---\nname: documentation\ndescription: Review docs\n'
          [ "${RALPHEX_STUB_FRONTMATTER:-closed}" != closed ] || printf '%s\n' '---'
          printf '\n'
        fi
        printf 'DEFAULT %s %s\n\n' "$name" "${RALPHEX_STUB_DEFAULTS:-original}"
        printf 'SIGNAL RULES STAY LAST\n'
      } > "$dest/agents/$name.txt"
    done
    ;;
  *) printf 'unexpected stub argument: %s\n' "$1" >&2; exit 1 ;;
esac
`

func TestExampleRalphexPrompts_Generate(t *testing.T) {
	t.Parallel()
	f := newPromptsFixture(t)
	for _, policy := range []string{"task", "review", "review_first", "agent", "documentation"} {
		writeFile(t, filepath.Join(f.root, ".ralphex/policy", policy+".md"), "POLICY "+policy+"\n")
	}
	configBefore := readExampleFile(t, filepath.Join(f.root, ".ralphex/config"))
	f.run(false, true, nil)
	owned := ownedRalphexFiles()
	for file, phase := range owned {
		content := string(readExampleFile(t, filepath.Join(f.root, ".ralphex", file)))
		block := string(readExampleFile(t, filepath.Join(f.root, ".ralphex/blocks", phase+".md")))
		name := strings.TrimSuffix(filepath.Base(file), ".txt")
		body := content
		switch {
		case strings.HasPrefix(file, "prompts/"):
			body = stripRalphexPromptHeader(content)
			if !strings.HasPrefix(body, block) {
				t.Errorf("%s body must begin with the complete block after comment stripping: %s", file, body)
			}
		case file == "agents/documentation.txt":
			preamble := "# agent comments\n# preserved\n---\nname: documentation\ndescription: Review docs\n---\n\n"
			if !strings.HasPrefix(content, preamble+block) {
				t.Errorf("block must follow intact frontmatter: %s", content)
			}
		case !strings.HasPrefix(strings.TrimLeft(content, "\n"), block):
			t.Errorf("%s block must lead the body: %s", file, content)
		}
		blockIndex := strings.Index(body, block)
		defaultIndex := strings.Index(body, "DEFAULT "+name+" original")
		phasePolicy := "POLICY " + phase + "\n"
		filePolicy := "POLICY " + name + "\n"
		if phase == "task" || phase == "review" || phase == "agent" {
			phaseIndex := strings.Index(body, phasePolicy)
			if phaseIndex <= blockIndex || phaseIndex >= defaultIndex || strings.Count(body, phasePolicy) != 1 {
				t.Errorf("%s phase policy order/count: %s", file, body)
			}
			if name == "review_first" || name == "documentation" {
				fileIndex := strings.Index(body, filePolicy)
				if fileIndex <= phaseIndex || fileIndex >= defaultIndex || strings.Count(body, filePolicy) != 1 {
					t.Errorf("%s file policy order/count: %s", file, body)
				}
			}
		}
		if defaultIndex < blockIndex || !strings.HasSuffix(content, "SIGNAL RULES STAY LAST\n") {
			t.Errorf("default body or signal rules lost: %s", content)
		}
	}
	if got := countOverrideFiles(t, f.root); got != 10 {
		t.Fatalf("generated %d overrides, want 10", got)
	}
	if got := readExampleFile(t, filepath.Join(f.root, ".ralphex/config")); !bytes.Equal(got, configBefore) {
		t.Fatalf("generator replaced pack config: %s", got)
	}
	stamp := string(readExampleFile(t, filepath.Join(f.root, ".ralphex/defaults.stamp")))
	if !strings.HasPrefix(stamp, "version: ralphex v1.7.0\ndefaults-sha256: ") ||
		len(strings.TrimSpace(strings.Split(stamp, "defaults-sha256: ")[1])) != 64 {
		t.Fatalf("invalid defaults stamp: %s", stamp)
	}
	before := snapshotExampleTree(t, filepath.Join(f.root, ".ralphex"))
	f.run(true, true, nil)
	assertExampleTreeUnchanged(t, filepath.Join(f.root, ".ralphex"), before)
}

func TestExampleRalphexPrompts_Check(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		edit   string
		env    []string
		want   string
		passes bool
	}{
		{name: "clean", passes: true},
		{name: "edited block", edit: "blocks/task.md", want: "changed"},
		{name: "edited policy", edit: "policy/review.md", want: "changed"},
		{name: "edited prompt", edit: "prompts/task.txt", want: "changed"},
		{name: "edited agent", edit: "agents/quality.txt", want: "changed"},
		{name: "changed defaults", env: []string{"RALPHEX_STUB_DEFAULTS=new"}, want: "defaults stamp drift"},
		{name: "changed dump config", env: []string{"RALPHEX_STUB_CONFIG=new"}, want: "defaults stamp drift"},
		{name: "changed version", env: []string{"RALPHEX_STUB_VERSION=ralphex v2"}, want: "defaults stamp drift"},
		{name: "unowned prompt", edit: "prompts/custom_eval.txt", want: "warning: unowned override", passes: true},
		{name: "unowned agent", edit: "agents/custom.txt", want: "warning: unowned override", passes: true},
		{name: "missing stamp", edit: "defaults.stamp", want: "defaults stamp drift"},
		{name: "missing prompt", edit: "prompts/task.txt", want: "missing override"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newPromptsFixture(t)
			f.run(false, true, nil)
			if tt.edit != "" {
				path := filepath.Join(f.root, ".ralphex", tt.edit)
				if strings.HasPrefix(tt.name, "missing") {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else {
					writeFile(t, path, "changed\n")
				}
			}
			before := snapshotExampleTree(t, filepath.Join(f.root, ".ralphex"))
			out := f.run(true, tt.passes, tt.env)
			if tt.want != "" && !strings.Contains(out, tt.want) {
				t.Errorf("missing diagnostic %q: %s", tt.want, out)
			}
			assertExampleTreeUnchanged(t, filepath.Join(f.root, ".ralphex"), before)
		})
	}
}

func TestExampleRalphexPrompts_RejectInvalidInputWithoutWrites(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		fragment string
		env      []string
		want     string
	}{
		{name: "block heading", fragment: "blocks/task.md", want: "fragment must not start with #"},
		{name: "phase policy heading", fragment: "policy/review.md", want: "fragment must not start with #"},
		{name: "file policy heading", fragment: "policy/documentation.md", want: "fragment must not start with #"},
		{name: "missing default", env: []string{"RALPHEX_STUB_MISSING=codex_review"}, want: "missing default"},
		{name: "unclosed frontmatter", env: []string{"RALPHEX_STUB_FRONTMATTER=open"}, want: "unclosed agent frontmatter"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newPromptsFixture(t)
			f.run(false, true, nil)
			if tt.fragment != "" {
				writeFile(t, filepath.Join(f.root, ".ralphex", tt.fragment), "## swallowed heading\npolicy\n")
			}
			before := snapshotExampleTree(t, filepath.Join(f.root, ".ralphex"))
			out := f.run(false, false, tt.env)
			if !strings.Contains(out, tt.want) {
				t.Errorf("missing diagnostic %q: %s", tt.want, out)
			}
			assertExampleTreeUnchanged(t, filepath.Join(f.root, ".ralphex"), before)
		})
	}
}

func TestExampleRalphexPrompts_RejectUnsafeDestinationsWithoutWrites(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		path string
		kind string
	}{
		{name: "root symlink", path: ".ralphex", kind: "parent"},
		{name: "prompts symlink", path: ".ralphex/prompts", kind: "parent"},
		{name: "agents symlink", path: ".ralphex/agents", kind: "parent"},
		{name: "prompt symlink", path: ".ralphex/prompts/task.txt", kind: "symlink"},
		{name: "agent symlink", path: ".ralphex/agents/testing.txt", kind: "symlink"},
		{name: "stamp symlink", path: ".ralphex/defaults.stamp", kind: "symlink"},
		{name: "dangling prompt symlink", path: ".ralphex/prompts/task.txt", kind: "dangling"},
		{name: "prompt directory", path: ".ralphex/prompts/task.txt", kind: "directory"},
		{name: "stamp directory", path: ".ralphex/defaults.stamp", kind: "directory"},
		{name: "prompts file", path: ".ralphex/prompts", kind: "file"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newPromptsFixture(t)
			f.run(false, true, nil)
			writeFile(t, filepath.Join(f.root, ".ralphex/blocks/task.md"), "changed block\n")
			outside := t.TempDir()
			target := filepath.Join(outside, "target")
			path := filepath.Join(f.root, tt.path)
			if tt.kind == "parent" {
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
				if tt.kind == "symlink" {
					writeFile(t, target, "external sentinel\n")
				}
			}
			switch tt.kind {
			case "parent", "symlink", "dangling":
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			case "file":
				writeFile(t, path, "ordinary file\n")
			}
			before := snapshotExampleTree(t, filepath.Join(f.root, ".ralphex"))
			externalBefore := snapshotExampleTree(t, outside)
			out := f.run(false, false, nil)
			if !strings.Contains(out, "ordinary") {
				t.Errorf("missing destination diagnostic: %s", out)
			}
			assertExampleTreeUnchanged(t, filepath.Join(f.root, ".ralphex"), before)
			assertExampleTreeUnchanged(t, outside, externalBefore)
		})
	}
}

func TestExampleRalphexScope(t *testing.T) {
	t.Parallel()
	requireSh(t)
	for _, tt := range []struct {
		name    string
		repos   string
		base    string
		branch  string
		prepare func(*testing.T, *wsGitFixture)
		passes  bool
		want    string
	}{
		{name: "valid", repos: "services/one/src services/two/src", base: "base", branch: "new-task", passes: true},
		{name: "root only", repos: ".", base: "base", branch: "task", passes: true},
		{name: "missing repo", repos: "services/missing/src", base: "base", branch: "task", want: "ordinary checkout"},
		{name: "missing base", repos: "services/one/src", base: "absent", branch: "task", want: "base does not resolve"},
		{name: "base missing in nested repo", repos: "services/one/src", base: "base", branch: "task",
			prepare: func(_ *testing.T, f *wsGitFixture) { f.git("services/one/src", "tag", "-d", "base") },
			want:    "base does not resolve"},
		{name: "base missing in root", repos: "services/one/src", base: "base", branch: "task",
			prepare: func(_ *testing.T, f *wsGitFixture) { f.git(".", "tag", "-d", "base") }, want: "base does not resolve"},
		{name: "invalid branch", repos: ".", base: "base", branch: "bad..branch", want: "invalid task branch"},
		{name: "newline base", repos: ".", base: "base\nextra", branch: "task", want: "one nonempty line"},
		{name: "missing repos", base: "base", branch: "task", want: "repos is required"},
		{name: "missing base param", repos: ".", branch: "task", want: "base is required"},
		{name: "missing branch", repos: ".", base: "base", want: "branch is required"},
		{name: "not ignored", repos: ".", base: "base", branch: "task",
			prepare: func(t *testing.T, f *wsGitFixture) { writeFile(t, filepath.Join(f.root, ".gitignore"), "/services/\n") },
			want:    "add /.ralphex/run/"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newWSGitFixture(t)
			script := installRalphexScript(t, f.root, "ralphex-scope.sh")
			writeFile(t, filepath.Join(f.root, ".ralphex/run/keep"), "previous ancillary state\n")
			if tt.prepare != nil {
				tt.prepare(t, f)
			}
			before := snapshotExampleTree(t, filepath.Join(f.root, ".ralphex/run"))
			cmd := f.command(script)
			cmd.Env = append(cmd.Env, "RALPHEX_REPOS="+tt.repos, "RALPHEX_BASE="+tt.base, "RALPHEX_BRANCH="+tt.branch)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tt.passes {
				t.Fatalf("scope success = %v, want %v: %s", err == nil, tt.passes, out)
			}
			if tt.want != "" && !strings.Contains(string(out), tt.want) {
				t.Errorf("missing diagnostic %q: %s", tt.want, out)
			}
			if tt.passes {
				for setting, want := range map[string]string{
					"repos":    strings.ReplaceAll(tt.repos, " ", "\n") + "\n",
					"base-ref": tt.base + "\n", "task-branch": tt.branch + "\n",
					"keep": "previous ancillary state\n",
				} {
					if got := string(readExampleFile(t, filepath.Join(f.root, ".ralphex/run", setting))); got != want {
						t.Errorf("%s = %q, want %q", setting, got, want)
					}
				}
			} else {
				// Rollback may recreate directory metadata, but preserves files and modes.
				after := snapshotExampleTree(t, filepath.Join(f.root, ".ralphex/run"))
				for name, file := range before {
					file.ModTime = 0
					before[name] = file
				}
				for name, file := range after {
					file.ModTime = 0
					after[name] = file
				}
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("previous scope not restored: before=%+v, after=%+v", before, after)
				}
			}
			matches, err := filepath.Glob(filepath.Join(f.root, ".ralphex/.scope.*"))
			if err != nil || len(matches) != 0 {
				t.Fatalf("scope scratch directories leaked: %v, %v", matches, err)
			}
		})
	}
}

func TestExampleRalphexScope_FailedFirstRunRemovesState(t *testing.T) {
	t.Parallel()
	requireSh(t)
	f := newWSGitFixture(t)
	script := installRalphexScript(t, f.root, "ralphex-scope.sh")
	if err := os.RemoveAll(filepath.Join(f.root, ".ralphex/run")); err != nil {
		t.Fatal(err)
	}
	cmd := f.command(script)
	cmd.Env = append(cmd.Env, "RALPHEX_REPOS=missing", "RALPHEX_BASE=base", "RALPHEX_BRANCH=task")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("invalid first scope succeeded: %s", out)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".ralphex/run")); !os.IsNotExist(err) {
		t.Fatalf("failed first run left partial state: %v", err)
	}
	f.success("rev-parse", "HEAD")
}

type promptsFixture struct {
	t      *testing.T
	root   string
	script string
	env    []string
}

func newPromptsFixture(t *testing.T) *promptsFixture {
	t.Helper()
	requireSh(t)
	root := installExample(t, "ralphex", "")
	cfg, err := config.LoadConfigSanitized(filepath.Join(root, "workspace.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Render(root, cfg.Render.Workspace, exampleTemplateData(cfg)); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	stub := filepath.Join(bin, "ralphex")
	writeFile(t, stub, ralphexStub)
	if err := os.Chmod(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "RALPHEX_") {
			env = append(env, value)
		}
	}
	env = append(env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := installRalphexScript(t, root, "ralphex-prompts.sh")
	return &promptsFixture{t: t, root: root, script: script, env: env}
}

func (f *promptsFixture) run(check, passes bool, env []string) string {
	f.t.Helper()
	cmd := exec.CommandContext(f.t.Context(), f.script)
	cmd.Dir = f.root
	cmd.Env = append(append([]string{}, f.env...), env...)
	if check {
		cmd.Env = append(cmd.Env, "RALPHEX_CHECK=true")
	} else {
		cmd.Env = append(cmd.Env, "RALPHEX_CHECK=false")
	}
	out, err := cmd.CombinedOutput()
	if (err == nil) != passes {
		f.t.Fatalf("prompts check=%v: %v, want success=%v\n%s", check, err, passes, out)
	}
	return string(out)
}

func requireSh(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh binary not available on PATH")
	}
}

func installRalphexScript(t *testing.T, root, name string) string {
	t.Helper()
	source := filepath.Join(findRepoRoot(t), "examples/workspace-packs/ralphex/workspace/scripts", name)
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("%s mode = %o, want 755", name, info.Mode().Perm())
	}
	script := filepath.Join(root, "workspace/scripts", name)
	writeFile(t, script, string(readExampleFile(t, source)))
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func ownedRalphexFiles() map[string]string {
	return map[string]string{
		"prompts/task.txt": "task", "prompts/review_first.txt": "review", "prompts/review_second.txt": "review",
		"prompts/codex.txt": "review", "prompts/codex_review.txt": "codex_review",
		"agents/documentation.txt": "agent", "agents/implementation.txt": "agent", "agents/quality.txt": "agent",
		"agents/simplification.txt": "agent", "agents/testing.txt": "agent",
	}
}

func stripRalphexPromptHeader(content string) string {
	lines := strings.Split(content, "\n")
	leading := 0
	for leading < len(lines) && strings.HasPrefix(lines[leading], "#") {
		leading++
	}
	if leading < 2 {
		return content
	}
	return strings.TrimLeft(strings.Join(lines[leading:], "\n"), "\n")
}

func countOverrideFiles(t *testing.T, root string) int {
	t.Helper()
	count := 0
	for _, kind := range []string{"prompts", "agents"} {
		entries, err := os.ReadDir(filepath.Join(root, ".ralphex", kind))
		if err != nil {
			t.Fatal(err)
		}
		count += len(entries)
	}
	return count
}

type exampleFileSnapshot struct {
	Data    string
	Mode    fs.FileMode
	ModTime int64
}

func snapshotExampleTree(t *testing.T, root string) map[string]exampleFileSnapshot {
	t.Helper()
	snapshot := map[string]exampleFileSnapshot{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		file := exampleFileSnapshot{Mode: info.Mode(), ModTime: info.ModTime().UnixNano()}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			file.Data = target
		} else if info.Mode().IsRegular() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			file.Data = string(content)
		}
		snapshot[rel] = file
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertExampleTreeUnchanged(t *testing.T, root string, before map[string]exampleFileSnapshot) {
	t.Helper()
	after := snapshotExampleTree(t, root)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("script changed files: before=%+v, after=%+v", before, after)
	}
}
