package docs

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestSources(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T) string // returns projectRoot
		want      int                       // expected number of sources
		wantNames []string
	}{
		{
			name: "no project root",
			setup: func(t *testing.T) string {
				return ""
			},
			want:      1,
			wantNames: []string{"dwe"},
		},
		{
			name: "project root without docs",
			setup: func(t *testing.T) string {
				tmpdir := t.TempDir()
				return tmpdir
			},
			want:      1,
			wantNames: []string{"dwe"},
		},
		{
			name: "project root with docs",
			setup: func(t *testing.T) string {
				tmpdir := t.TempDir()
				docsDir := filepath.Join(tmpdir, "docs")
				if err := os.Mkdir(docsDir, 0755); err != nil {
					t.Fatalf("failed to create docs dir: %v", err)
				}
				return tmpdir
			},
			want:      2,
			wantNames: []string{"dwe", "project"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectRoot := tt.setup(t)
			roots := Sources(projectRoot)

			if len(roots) != tt.want {
				t.Errorf("got %d roots, want %d", len(roots), tt.want)
			}

			for i, name := range tt.wantNames {
				if i < len(roots) && roots[i].Name != name {
					t.Errorf("root %d: got name %q, want %q", i, roots[i].Name, name)
				}
			}
		})
	}
}

func TestRelPath(t *testing.T) {
	mockFS := fstest.MapFS{}
	root := DocRoot{Name: "test", FS: mockFS}

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "valid path",
			path:    "config/services.md",
			wantErr: false,
		},
		{
			name:    "single file",
			path:    "index.md",
			wantErr: false,
		},
		{
			name:    "current dir",
			path:    ".",
			wantErr: true,
		},
		{
			name:    "parent dir",
			path:    "..",
			wantErr: true,
		},
		{
			name:    "absolute path",
			path:    "/etc/passwd",
			wantErr: true,
		},
		{
			name:    "traversal attempt",
			path:    "../../etc/passwd",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RelPath(root, tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("RelPath(%q): got err %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}
