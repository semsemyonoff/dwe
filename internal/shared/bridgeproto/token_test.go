package bridgeproto

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateToken_Shape(t *testing.T) {
	tok, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if len(tok) != 64 {
		t.Errorf("len = %d, want 64 hex chars", len(tok))
	}
	if _, err := hex.DecodeString(tok); err != nil {
		t.Errorf("not valid hex: %v", err)
	}
	if tok != strings.ToLower(tok) {
		t.Errorf("token not lowercase: %q", tok)
	}
}

func TestGenerateToken_Unique(t *testing.T) {
	a, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	b, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if a == b {
		t.Error("two generated tokens are identical")
	}
}

func TestTokenFile_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	tok, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if err := WriteTokenFile(path, tok); err != nil {
		t.Fatalf("WriteTokenFile: %v", err)
	}
	got, err := ReadTokenFile(path)
	if err != nil {
		t.Fatalf("ReadTokenFile: %v", err)
	}
	if got != tok {
		t.Errorf("read = %q, want %q", got, tok)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 0600", perm)
	}
}

func TestReadTokenFile_TrimsWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("  abc123\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadTokenFile(path)
	if err != nil {
		t.Fatalf("ReadTokenFile: %v", err)
	}
	if got != "abc123" {
		t.Errorf("read = %q, want %q", got, "abc123")
	}
}

func TestReadTokenFile_Missing(t *testing.T) {
	if _, err := ReadTokenFile(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestTokenEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"match", "aabbcc", "aabbcc", true},
		{"mismatch same length", "aabbcc", "aabbcd", false},
		{"different length", "aabbcc", "aabb", false},
		{"both empty never match", "", "", false},
		{"empty vs non-empty", "", "aabbcc", false},
		{"non-empty vs empty", "aabbcc", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TokenEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("TokenEqual(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
