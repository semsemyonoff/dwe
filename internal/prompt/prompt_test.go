package prompt

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestRunFromDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setup      func(t *testing.T) (cwd string)
		args       []string
		wantCode   int
		wantStdout string
	}{
		{
			name: "in_project_name_from_config",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: my-proj\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} my-proj\n",
		},
		{
			name: "in_subdirectory_walk_up",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: walked\n")
				sub := filepath.Join(root, "a", "b", "c")
				if err := os.MkdirAll(sub, 0o755); err != nil {
					t.Fatal(err)
				}
				return sub
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} walked\n",
		},
		{
			name: "name_fallback_to_dir_basename",
			setup: func(t *testing.T) string {
				root := filepath.Join(t.TempDir(), "fallback-dir")
				if err := os.Mkdir(root, 0o755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(root, "devbox.yml"), "project: {}\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} fallback-dir\n",
		},
		{
			name: "outside_any_project",
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			args:       nil,
			wantCode:   1,
			wantStdout: "",
		},
		{
			name: "check_inside_project",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: chk\n")
				return root
			},
			args:       []string{"--check"},
			wantCode:   0,
			wantStdout: "",
		},
		{
			name: "check_outside_any_project",
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			args:       []string{"--check"},
			wantCode:   1,
			wantStdout: "",
		},
		{
			name: "unknown_arg",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: x\n")
				return root
			},
			args:       []string{"foo"},
			wantCode:   1,
			wantStdout: "",
		},
		{
			name: "status_state_file_absent",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p\n",
		},
		{
			name: "status_pending_only",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "pending: {}\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ⟳\n",
		},
		{
			name: "status_deployed",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: deployed\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ✓\n",
		},
		{
			name: "status_partial",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: partial\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ⚠\n",
		},
		{
			name: "status_failed",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: failed\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ✗\n",
		},
		{
			name: "status_deployed_plus_pending_pending_wins",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: deployed\npending: {}\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ⟳\n",
		},
		{
			name: "status_partial_plus_pending_partial_wins",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: partial\npending: {}\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ⚠\n",
		},
		{
			name: "status_failed_plus_pending_failed_wins",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: failed\npending: {}\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ✗\n",
		},
		{
			name: "status_not_deployed_no_icon",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: not_deployed\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p\n",
		},
		{
			name: "status_corrupted_state_yml_no_icon",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project: [this is: not valid\n  - bad\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p\n",
		},
		{
			name: "status_state_yml_with_unknown_fields_ignored",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: deployed\n  extra: stuff\nservices:\n  db:\n    status: deployed\nunknown_top_level: 42\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ✓\n",
		},
		{
			name: "corrupted_devbox_yml",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project: [this is: not valid yaml\n  - bad\n")
				return root
			},
			args:       nil,
			wantCode:   1,
			wantStdout: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cwd := tt.setup(t)
			var buf bytes.Buffer
			code := runFromDir(&buf, tt.args, cwd)
			if code != tt.wantCode {
				t.Errorf("exit code: got %d, want %d (stdout=%q)", code, tt.wantCode, buf.String())
			}
			if got := buf.String(); got != tt.wantStdout {
				t.Errorf("stdout: got %q, want %q", got, tt.wantStdout)
			}
		})
	}
}
