package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/semsemyonoff/dwe/internal/core/workflow/snapshot/meta"
)

func TestStripPreservedKeys(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		paths    []string
		want     string
		wantErr  string
		wantSame bool
	}{
		{
			name:  "removes leaf in nested mapping",
			input: "services:\n  main:\n    ports:\n      - 8080\n    enabled: true\n",
			paths: []string{"services.main.ports"},
			want:  "services:\n    main:\n        enabled: true\n",
		},
		{
			name:  "removes entire subtree",
			input: "services:\n  main:\n    ports: [8080]\nhost:\n  shell: zsh\n",
			paths: []string{"services.main"},
			want:  "services: {}\nhost:\n    shell: zsh\n",
		},
		{
			name:  "missing path is a no-op",
			input: "host:\n  shell: zsh\n",
			paths: []string{"services.main.ports"},
			want:  "host:\n    shell: zsh\n",
		},
		{
			name:    "structural conflict at intermediate segment errors",
			input:   "services: scalar\n",
			paths:   []string{"services.main"},
			wantErr: `preserve_keys "services.main"`,
		},
		{
			name:     "empty input passes through",
			input:    "",
			paths:    []string{"a.b"},
			wantSame: true,
		},
		{
			name:     "empty paths passes through",
			input:    "a: 1\n",
			paths:    nil,
			wantSame: true,
		},
		{
			name:  "preserves sibling key order",
			input: "a: 1\nb: 2\nc: 3\n",
			paths: []string{"b"},
			want:  "a: 1\nc: 3\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stripPreservedKeys([]byte(tc.input), tc.paths)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want err containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if tc.wantSame {
				if string(got) != tc.input {
					t.Fatalf("expected passthrough, got %q", string(got))
				}
				return
			}
			if string(got) != tc.want {
				t.Fatalf("strip mismatch\nwant: %q\ngot:  %q", tc.want, string(got))
			}
		})
	}
}

func TestStripPreservedKeys_oversize(t *testing.T) {
	big := make([]byte, localYMLMaxBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if _, err := stripPreservedKeys(big, []string{"a"}); err == nil {
		t.Fatalf("expected oversize error, got nil")
	}
}

func TestMergePreservedKeys(t *testing.T) {
	tests := []struct {
		name     string
		snapshot string
		current  string
		paths    []string
		want     string
		wantErr  string
	}{
		{
			name:     "splices leaf into snapshot",
			snapshot: "services:\n  main:\n    enabled: true\n",
			current:  "services:\n  main:\n    ports:\n      - 9090\n",
			paths:    []string{"services.main.ports"},
			want:     "services:\n    main:\n        enabled: true\n        ports:\n            - 9090\n",
		},
		{
			name:     "creates intermediate mappings when missing on snapshot",
			snapshot: "other: 1\n",
			current:  "host:\n  shell: zsh\n",
			paths:    []string{"host.shell"},
			want:     "other: 1\nhost:\n    shell: zsh\n",
		},
		{
			name:     "path missing in current is no-op",
			snapshot: "a: 1\n",
			current:  "b: 2\n",
			paths:    []string{"host.shell"},
			want:     "a: 1\n",
		},
		{
			name:     "empty current preserves snapshot",
			snapshot: "a: 1\n",
			current:  "",
			paths:    []string{"a"},
			want:     "a: 1\n",
		},
		{
			name:     "empty snapshot with current value yields current path only",
			snapshot: "",
			current:  "host:\n  shell: zsh\n",
			paths:    []string{"host.shell"},
			want:     "host:\n    shell: zsh\n",
		},
		{
			name:     "type conflict at leaf returns error",
			snapshot: "services:\n  main:\n    ports:\n      - 9090\n",
			current:  "services:\n  main:\n    ports: scalar\n",
			paths:    []string{"services.main.ports"},
			wantErr:  "type conflict",
		},
		{
			name:     "type conflict at intermediate returns error",
			snapshot: "services: 1\n",
			current:  "services:\n  main:\n    ports: [9090]\n",
			paths:    []string{"services.main"},
			wantErr:  "type conflict",
		},
		{
			name:     "no paths passes snapshot through",
			snapshot: "a: 1\n",
			current:  "a: 2\n",
			paths:    nil,
			want:     "a: 1\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mergePreservedKeys([]byte(tc.snapshot), []byte(tc.current), tc.paths)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want err containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("merge mismatch\nwant: %q\ngot:  %q", tc.want, string(got))
			}
		})
	}
}

func TestMergePreservedKeys_oversize(t *testing.T) {
	big := make([]byte, localYMLMaxBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if _, err := mergePreservedKeys(big, []byte("a: 1\n"), []string{"a"}); err == nil {
		t.Fatalf("expected snapshot oversize error, got nil")
	}
	if _, err := mergePreservedKeys([]byte("a: 1\n"), big, []string{"a"}); err == nil {
		t.Fatalf("expected current oversize error, got nil")
	}
}

func TestStripAndMerge_keyOrderRoundTrip(t *testing.T) {
	// Strip a key, then merge it back, and assert the resulting top-level key
	// order matches the original input. yaml.v3 normalizes indentation/flow
	// style but should preserve key sequencing at untouched levels.
	original := []byte("alpha: 1\nbeta:\n  inner: 2\n  preserved:\n    - x\ngamma: 3\n")
	stripped, err := stripPreservedKeys(original, []string{"beta.preserved"})
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	merged, err := mergePreservedKeys(stripped, original, []string{"beta.preserved"})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	gotKeys := topLevelKeys(t, merged)
	wantKeys := []string{"alpha", "beta", "gamma"}
	if len(gotKeys) != len(wantKeys) {
		t.Fatalf("top-level key count: want %d, got %d (%v)", len(wantKeys), len(gotKeys), gotKeys)
	}
	for i := range wantKeys {
		if gotKeys[i] != wantKeys[i] {
			t.Fatalf("top-level key order mismatch: want %v, got %v", wantKeys, gotKeys)
		}
	}

	// beta sub-tree must round-trip with both inner and preserved present.
	innerKeys := nestedMappingKeys(t, merged, "beta")
	if !containsAll(innerKeys, []string{"inner", "preserved"}) {
		t.Fatalf("beta sub-tree missing preserved key: got %v", innerKeys)
	}
}

func topLevelKeys(t *testing.T, b []byte) []string {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		t.Fatalf("not a mapping document")
	}
	root := doc.Content[0]
	var out []string
	for i := 0; i < len(root.Content); i += 2 {
		out = append(out, root.Content[i].Value)
	}
	return out
}

func nestedMappingKeys(t *testing.T, b []byte, key string) []string {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	root := doc.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			sub := root.Content[i+1]
			var keys []string
			for j := 0; j < len(sub.Content); j += 2 {
				keys = append(keys, sub.Content[j].Value)
			}
			return keys
		}
	}
	return nil
}

func TestRestoreLocalYML_EdgeCases(t *testing.T) {
	const preserved = "services.main.ports"

	tests := []struct {
		name         string
		snap         string // "" means no snapshot/local.yml
		current      string // "" means no working-copy local.yml
		preserveKeys []string
		// wantExists==false means dst must NOT exist after restore.
		wantExists bool
		wantBody   string
	}{
		{
			name:         "yes/yes merges preserved keys from current into snapshot",
			snap:         "services:\n  main:\n    enabled: true\n",
			current:      "services:\n  main:\n    ports:\n      - 9090\n",
			preserveKeys: []string{preserved},
			wantExists:   true,
			wantBody:     "services:\n    main:\n        enabled: true\n        ports:\n            - 9090\n",
		},
		{
			name:         "yes/no writes snapshot as-is",
			snap:         "host:\n  shell: zsh\n",
			current:      "",
			preserveKeys: []string{preserved},
			wantExists:   true,
			wantBody:     "host:\n  shell: zsh\n",
		},
		{
			name:         "no/yes writes minimal local.yml containing only preserved keys",
			snap:         "",
			current:      "services:\n  main:\n    ports:\n      - 9090\n    enabled: true\nhost:\n  shell: zsh\n",
			preserveKeys: []string{preserved},
			wantExists:   true,
			wantBody:     "services:\n    main:\n        ports:\n            - 9090\n",
		},
		{
			name:         "no/yes with no preserve_keys removes the working-copy file",
			snap:         "",
			current:      "services:\n  main:\n    enabled: true\n",
			preserveKeys: nil,
			wantExists:   false,
		},
		{
			name:         "no/no is a no-op",
			snap:         "",
			current:      "",
			preserveKeys: []string{preserved},
			wantExists:   false,
		},
		{
			name:         "no/yes with preserve_keys not present in current removes the file",
			snap:         "",
			current:      "host:\n  shell: zsh\n",
			preserveKeys: []string{preserved},
			wantExists:   false,
		},
		{
			name:         "yes/yes with no preserve_keys writes snapshot verbatim",
			snap:         "host:\n  shell: bash\n",
			current:      "host:\n  shell: zsh\n",
			preserveKeys: nil,
			wantExists:   true,
			wantBody:     "host:\n  shell: bash\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			snapDir := filepath.Join(tmp, "snap")
			baseDir := filepath.Join(tmp, "base")
			if tc.snap != "" {
				p := filepath.Join(snapDir, meta.DevboxSubdir, "local.yml")
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatalf("mkdir snap: %v", err)
				}
				if err := os.WriteFile(p, []byte(tc.snap), 0o644); err != nil {
					t.Fatalf("write snap local.yml: %v", err)
				}
			}
			if tc.current != "" {
				p := filepath.Join(baseDir, "devbox", "local.yml")
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatalf("mkdir base: %v", err)
				}
				if err := os.WriteFile(p, []byte(tc.current), 0o644); err != nil {
					t.Fatalf("write current local.yml: %v", err)
				}
			}

			err := restoreLocalYML(
				filepath.Join(snapDir, meta.DevboxSubdir, "local.yml"),
				filepath.Join(baseDir, "devbox", "local.yml"),
				tc.preserveKeys,
			)
			if err != nil {
				t.Fatalf("restoreLocalYML: %v", err)
			}

			dst := filepath.Join(baseDir, "devbox", "local.yml")
			body, statErr := os.ReadFile(dst)
			if !tc.wantExists {
				if statErr == nil {
					t.Fatalf("dst %s should not exist, body=%q", dst, string(body))
				}
				if !os.IsNotExist(statErr) {
					t.Fatalf("stat dst: %v", statErr)
				}
				return
			}
			if statErr != nil {
				t.Fatalf("read dst: %v", statErr)
			}
			if string(body) != tc.wantBody {
				t.Fatalf("dst body mismatch\nwant: %q\ngot:  %q", tc.wantBody, string(body))
			}
		})
	}
}

func TestCaptureLocalYML_StripsPreservedKeys(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "local.yml")
	dst := filepath.Join(tmp, "snap", "local.yml")
	body := "services:\n  main:\n    ports:\n      - 9090\n    enabled: true\n"
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	wrote, err := captureLocalYML(src, dst, []string{"services.main.ports"})
	if err != nil {
		t.Fatalf("captureLocalYML: %v", err)
	}
	if !wrote {
		t.Fatal("wrote=false, want true")
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	want := "services:\n    main:\n        enabled: true\n"
	if string(got) != want {
		t.Fatalf("captured local.yml\nwant: %q\ngot:  %q", want, string(got))
	}
}

func TestCaptureLocalYML_MissingSource(t *testing.T) {
	tmp := t.TempDir()
	wrote, err := captureLocalYML(
		filepath.Join(tmp, "missing.yml"),
		filepath.Join(tmp, "snap", "local.yml"),
		nil,
	)
	if err != nil {
		t.Fatalf("captureLocalYML: %v", err)
	}
	if wrote {
		t.Fatal("wrote=true for missing source")
	}
}

func containsAll(s, want []string) bool {
	set := map[string]bool{}
	for _, v := range s {
		set[v] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}
