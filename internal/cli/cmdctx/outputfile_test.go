package cmdctx

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// codedError unwraps the typed error every output-file helper returns, so a
// test can assert on the code rather than on the rendered message alone.
func codedError(t *testing.T, err error) *CodedError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	var ce *CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("expected a *CodedError, got %T: %v", err, err)
	}
	return ce
}

func TestWriteOutputFile_TargetAbsent_Written(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.yml")
	if err := WriteOutputFile("eject", OutputFile{Path: path, Data: []byte("phases: []\n"), Mode: 0o644}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "phases: []\n" {
		t.Fatalf("content = %q", got)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %s, want 0644", fi.Mode().Perm())
	}
}

func TestWriteOutputFile_TargetPresent_NoForce_Refused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.yml")
	const original = "authored: true\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := WriteOutputFile("deploy_eject", OutputFile{Path: path, Data: []byte("new"), Mode: 0o644})
	ce := codedError(t, err)
	if ce.Code != "deploy_eject_output_exists" {
		t.Fatalf("code = %q, want deploy_eject_output_exists", ce.Code)
	}
	if !strings.Contains(ce.Message, path) {
		t.Fatalf("message %q does not name the path %q", ce.Message, path)
	}
	if !strings.Contains(ce.Hint, "--force") {
		t.Fatalf("hint %q does not mention --force", ce.Hint)
	}
	if ce.Details["path"] != path {
		t.Fatalf("details[path] = %v, want %q", ce.Details["path"], path)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != original {
		t.Fatalf("refused write still changed the file: %q", got)
	}
}

func TestWriteOutputFile_TargetPresent_Force_Overwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.yml")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := WriteOutputFile("eject", OutputFile{Path: path, Data: []byte("new\n"), Mode: 0o644, Force: true}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "new\n" {
		t.Fatalf("content = %q, want new", got)
	}
}

// The non-regular-file guard sits after the exists check, so a directory or a
// symlink target only reaches it with --force; without it the caller sees the
// ordinary "already exists" refusal.
func TestWriteOutputFile_NonRegularTarget(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "adir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	linkTarget := filepath.Join(dir, "real.yml")
	if err := os.WriteFile(linkTarget, []byte("real\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	link := filepath.Join(dir, "link.yml")
	if err := os.Symlink(linkTarget, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	tests := []struct {
		name  string
		path  string
		force bool
		code  string
	}{
		{name: "directory without force", path: subdir, code: "eject_output_exists"},
		{name: "directory with force", path: subdir, force: true, code: "eject_output_invalid"},
		{name: "symlink without force", path: link, code: "eject_output_exists"},
		{name: "symlink with force", path: link, force: true, code: "eject_output_invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := WriteOutputFile("eject", OutputFile{Path: tt.path, Data: []byte("x"), Mode: 0o644, Force: tt.force})
			if ce := codedError(t, err); ce.Code != tt.code {
				t.Fatalf("code = %q, want %q (%v)", ce.Code, tt.code, err)
			}
		})
	}

	// The symlink guard is what keeps a --force write from travelling through
	// the link and clobbering the file behind it.
	if got, err := os.ReadFile(linkTarget); err != nil || string(got) != "real\n" {
		t.Fatalf("symlink target changed: %q (%v)", got, err)
	}
}

func TestWriteOutputFile_UnwritableDir_ErrorSurfaced(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o555); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := WriteOutputFile("eject", OutputFile{Path: filepath.Join(dir, "out.yml"), Data: []byte("x"), Mode: 0o644})
	ce := codedError(t, err)
	if ce.Code != "eject_output_write_failed" {
		t.Fatalf("code = %q, want eject_output_write_failed", ce.Code)
	}
	if ce.Message == "" {
		t.Fatalf("the underlying write error was swallowed")
	}
}

// TightenMode is the caller's decision, not a mode comparison inside the
// helper: os.WriteFile keeps an existing file's mode, so a secret must ask for
// the chmod while an ordinary source file must not get one.
func TestWriteOutputFile_TightenMode(t *testing.T) {
	tests := []struct {
		name    string
		tighten bool
		want    os.FileMode
	}{
		{name: "tighten", tighten: true, want: 0o600},
		{name: "leave alone", tighten: false, want: 0o644},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "out")
			if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			opts := OutputFile{Path: path, Data: []byte("new"), Mode: 0o600, Force: true, TightenMode: tt.tighten}
			if err := WriteOutputFile("eject", opts); err != nil {
				t.Fatalf("write: %v", err)
			}
			fi, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if fi.Mode().Perm() != tt.want {
				t.Fatalf("mode = %s, want %s", fi.Mode().Perm(), tt.want)
			}
		})
	}
}

// The refusal has to tell an inert existing file apart from an authored one —
// that is the whole point of ejecting over a file `dwe validate` already
// reports as having no effect. The note is derived from the real loader here so
// the two conditions stay the validator's two conditions.
func TestWriteOutputFile_ExistsNote_InertVsAuthored(t *testing.T) {
	authored := `phases:
  - name: build
    steps:
      - name: hello
        type: shell
        cmd: echo hi
`
	tests := []struct {
		name     string
		content  string
		wantNote string
	}{
		{name: "all comments", content: "# nothing active here\n", wantNote: "no active content"},
		{name: "log only", content: "log: false\n", wantNote: "declares no phases"},
		{name: "authored", content: authored, wantNote: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "reset.yml")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			cfg, state, err := config.LoadResetConfigWithState(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			note := InertPipelineNote(state, len(cfg.Phases), "reset")
			if tt.wantNote == "" {
				if note != "" {
					t.Fatalf("authored file got note %q", note)
				}
			} else if !strings.Contains(note, tt.wantNote) {
				t.Fatalf("note = %q, want it to mention %q", note, tt.wantNote)
			}

			err = WriteOutputFile("reset_eject", OutputFile{Path: path, Data: []byte("x"), Mode: 0o644, ExistsNote: note})
			ce := codedError(t, err)
			if !strings.Contains(ce.Message, path) {
				t.Fatalf("message %q does not name the path", ce.Message)
			}
			if tt.wantNote == "" {
				if strings.Contains(ce.Message, "built-in default") {
					t.Fatalf("authored file called inert: %q", ce.Message)
				}
				return
			}
			if !strings.Contains(ce.Message, tt.wantNote) || !strings.Contains(ce.Message, "built-in default") {
				t.Fatalf("message %q does not explain the inert file", ce.Message)
			}
		})
	}
}

func TestResolveFilePath(t *testing.T) {
	root := t.TempDir()
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("evalsymlinks: %v", err)
	}
	root = real

	regular := filepath.Join(root, "file.yml")
	if err := os.WriteFile(regular, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	link := filepath.Join(root, "link.yml")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	dir := filepath.Join(root, "sub")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "existing regular file", path: regular},
		{name: "absent file", path: filepath.Join(root, "absent.yml")},
		{name: "outside the project", path: filepath.Join(t.TempDir(), "elsewhere.yml")},
		{name: "empty", path: "   ", wantErr: true},
		{name: "symlink", path: link, wantErr: true},
		{name: "directory", path: dir, wantErr: true},
		// A path that climbs back out of the project is not an error: it is
		// simply outside it, and these are file utilities rather than pack
		// loaders. Containment only binds a path that stays inside.
		{name: "climbs out of the project", path: filepath.Join(root, "..", "outside.yml")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveFilePath("eject", root, tt.path, "output")
			if tt.wantErr {
				if ce := codedError(t, err); ce.Code != "eject_path_invalid" {
					t.Fatalf("code = %q, want eject_path_invalid", ce.Code)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if !filepath.IsAbs(got) {
				t.Fatalf("path %q is not absolute", got)
			}
		})
	}
}

// A relative path is resolved against the process working directory, the same
// rule `dwe secrets` has always applied.
func TestResolveFilePath_RelativeToCwd(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	got, err := ResolveFilePath("eject", t.TempDir(), "out.yml", "output")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := filepath.Join(cwd, "out.yml"); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

// A symlinked directory component inside the project is the case
// CheckNoSymlinks exists for: the target itself is absent, so only the walk
// over the path's components can catch it.
func TestResolveFilePath_SymlinkedComponent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need elevation on windows")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("evalsymlinks: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "via")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	_, err = ResolveFilePath("eject", root, filepath.Join(root, "via", "out.yml"), "output")
	if ce := codedError(t, err); ce.Code != "eject_path_invalid" {
		t.Fatalf("code = %q, want eject_path_invalid", ce.Code)
	}
}

func TestPathIsUnder(t *testing.T) {
	tests := []struct {
		name string
		root string
		abs  string
		want bool
	}{
		{name: "inside", root: "/a/b", abs: "/a/b/c.yml", want: true},
		{name: "the root itself", root: "/a/b", abs: "/a/b", want: true},
		{name: "sibling with a shared prefix", root: "/a/b", abs: "/a/bb/c.yml"},
		{name: "outside", root: "/a/b", abs: "/tmp/c.yml"},
		{name: "empty root", root: "", abs: "/a/b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PathIsUnder(tt.root, tt.abs); got != tt.want {
				t.Fatalf("PathIsUnder(%q, %q) = %v, want %v", tt.root, tt.abs, got, tt.want)
			}
		})
	}
}
