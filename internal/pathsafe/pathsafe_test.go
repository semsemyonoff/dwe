package pathsafe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckNoSymlinks(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test structure:
	// tmpDir/
	//   nolink/dir/file
	//   link_parent -> /tmp/somewhere
	//   link_file (a file that IS a symlink)
	safeDir := filepath.Join(tmpDir, "nolink", "dir")
	if err := os.MkdirAll(safeDir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	linkParentDir := filepath.Join(tmpDir, "link_parent")
	targetDir := filepath.Join(tmpDir, "target_outside")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("setup target: %v", err)
	}
	if err := os.Symlink(targetDir, linkParentDir); err != nil {
		t.Fatalf("setup symlink: %v", err)
	}

	linkFile := filepath.Join(tmpDir, "link_file")
	if err := os.Symlink(filepath.Join(tmpDir, "nonexistent"), linkFile); err != nil {
		t.Fatalf("setup link file: %v", err)
	}

	tests := []struct {
		name   string
		root   string
		absDir string
		label  string
		ok     bool
	}{
		{
			name:   "no symlinks in path",
			root:   tmpDir,
			absDir: safeDir,
			label:  "test path",
			ok:     true,
		},
		{
			name:   "symlink in middle of path",
			root:   tmpDir,
			absDir: filepath.Join(linkParentDir, "subdir"),
			label:  "test path",
			ok:     false,
		},
		{
			name:   "symlink at leaf (direct file)",
			root:   tmpDir,
			absDir: linkFile,
			label:  "test file",
			ok:     false,
		},
		{
			name:   "nonexistent component (should pass)",
			root:   tmpDir,
			absDir: filepath.Join(safeDir, "nonexistent", "subdir"),
			label:  "test path",
			ok:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckNoSymlinks(tt.root, tt.absDir, tt.label)
			if tt.ok && err != nil {
				t.Errorf("expected success, got error: %v", err)
			}
			if !tt.ok && err == nil {
				t.Errorf("expected error, got success")
			}
		})
	}
}

func TestContainedRel(t *testing.T) {
	tmpDir := t.TempDir()
	root := tmpDir

	tests := []struct {
		name      string
		child     string
		wantRel   string
		wantError bool
	}{
		{
			name:    "child is root",
			child:   root,
			wantRel: ".",
			// . is rejected (we use this for files, which can't equal their directory)
			wantError: true,
		},
		{
			name:      "simple nested path",
			child:     filepath.Join(root, "a", "b"),
			wantRel:   filepath.Join("a", "b"),
			wantError: false,
		},
		{
			name:      "path with dot components cleaned out",
			child:     filepath.Join(root, "a", ".", "b"),
			wantRel:   filepath.Join("a", "b"),
			wantError: false,
		},
		{
			name:      "escaping path (..) is rejected",
			child:     filepath.Join(root, "a", "..", "..", "etc", "passwd"),
			wantError: true,
		},
		{
			name:      "bare .. is rejected",
			child:     filepath.Join(root, ".."),
			wantError: true,
		},
		{
			name:      "leading ../ is rejected",
			child:     filepath.Join(root, "..", "etc"),
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rel, err := ContainedRel(root, tt.child)
			if tt.wantError {
				if err == nil {
					t.Errorf("expected error, got rel=%q", rel)
				}
			} else {
				if err != nil {
					t.Errorf("expected success, got error: %v", err)
				}
				if rel != tt.wantRel {
					t.Errorf("expected rel=%q, got %q", tt.wantRel, rel)
				}
			}
		})
	}
}

func TestEnsureRealUnder(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directory structure:
	// tmpDir/proj/
	// tmpDir/proj/a/
	// tmpDir/proj/a/b/
	projDir := filepath.Join(tmpDir, "proj")
	aDir := filepath.Join(projDir, "a")
	bDir := filepath.Join(aDir, "b")
	if err := os.MkdirAll(bDir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	otherDir := filepath.Join(tmpDir, "other")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatalf("setup other: %v", err)
	}

	// Resolve paths through symlinks (macOS /tmp is often under /private/var)
	realProj, err := filepath.EvalSymlinks(projDir)
	if err != nil {
		t.Fatalf("evalysymlinks proj: %v", err)
	}
	realA, err := filepath.EvalSymlinks(aDir)
	if err != nil {
		t.Fatalf("evalsymlinks a: %v", err)
	}
	realB, err := filepath.EvalSymlinks(bDir)
	if err != nil {
		t.Fatalf("evalsymlinks b: %v", err)
	}
	realOther, err := filepath.EvalSymlinks(otherDir)
	if err != nil {
		t.Fatalf("evalsymlinks other: %v", err)
	}

	tests := []struct {
		name      string
		realDir   string
		roots     []string
		wantError bool
		desc      string
	}{
		{
			name:      "single root pass",
			realDir:   realB,
			roots:     []string{realProj},
			wantError: false,
			desc:      "b under proj",
		},
		{
			name:      "dual root pass",
			realDir:   realB,
			roots:     []string{realProj, realA},
			wantError: false,
			desc:      "b under both proj and a",
		},
		{
			name:      "dual root fail (under one but not other)",
			realDir:   realA,
			roots:     []string{realProj, realB},
			wantError: true,
			desc:      "a under proj but not under b",
		},
		{
			name:      "equality allowed (realDir == root)",
			realDir:   realProj,
			roots:     []string{realProj},
			wantError: false,
			desc:      "dir equals root (allowed)",
		},
		{
			name:      "slightly outside (bX vs b)",
			realDir:   realProj + "X",
			roots:     []string{realProj},
			wantError: true,
			desc:      "dir slightly outside root",
		},
		{
			name:      "completely outside",
			realDir:   realOther,
			roots:     []string{realProj},
			wantError: true,
			desc:      "dir completely outside",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := EnsureRealUnder(tt.realDir, tt.roots...)
			if tt.wantError {
				if err == nil {
					t.Errorf("%s: expected error, got success", tt.desc)
				}
			} else {
				if err != nil {
					t.Errorf("%s: expected success, got error: %v", tt.desc, err)
				}
			}
		})
	}
}

// TestCheckNoSymlinksWithEvalSymlinks tests that CheckNoSymlinks works correctly
// with paths that have been run through EvalSymlinks (handling macOS /private/var).
func TestCheckNoSymlinksWithEvalSymlinks(t *testing.T) {
	tmpDir := t.TempDir()
	realTmpDir, err := filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatalf("evalsymlinks tmp: %v", err)
	}

	safeDir := filepath.Join(realTmpDir, "safe", "nested")
	if err := os.MkdirAll(safeDir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Both root and dir are resolved, so the check works on all systems
	err = CheckNoSymlinks(realTmpDir, safeDir, "test path")
	if err != nil {
		t.Errorf("expected success with evalsymlinks'd paths, got: %v", err)
	}
}

// TestEnsureRealUnderMultipleRoots tests the multi-root semantics.
func TestEnsureRealUnderMultipleRoots(t *testing.T) {
	tmpDir := t.TempDir()
	realTmpDir, err := filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatalf("evalsymlinks tmp: %v", err)
	}

	projDir := filepath.Join(realTmpDir, "proj")
	hubDir := filepath.Join(projDir, "services", "api")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	realHub, err := filepath.EvalSymlinks(hubDir)
	if err != nil {
		t.Fatalf("evalsymlinks hub: %v", err)
	}
	realProj, err := filepath.EvalSymlinks(projDir)
	if err != nil {
		t.Fatalf("evalsymlinks proj: %v", err)
	}

	// Hub must satisfy both: under proj root AND under project root
	err = EnsureRealUnder(realHub, realProj, realTmpDir)
	if err != nil {
		t.Errorf("hub under both proj and tmp should pass, got: %v", err)
	}

	// But if one boundary is violated, it fails
	badDir := filepath.Join(realTmpDir, "elsewhere")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("setup baddir: %v", err)
	}
	realBad, err := filepath.EvalSymlinks(badDir)
	if err != nil {
		t.Fatalf("evalsymlinks bad: %v", err)
	}

	// bad is under neither proj nor hub
	err = EnsureRealUnder(realBad, realProj, realHub)
	if err == nil {
		t.Errorf("bad dir under neither root should fail, got success")
	}
}
