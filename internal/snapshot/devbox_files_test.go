package snapshot

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
