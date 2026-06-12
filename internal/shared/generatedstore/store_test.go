package generatedstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFileReturnsEmptyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yml")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load missing file: unexpected error %v", err)
	}
	if !s.IsEmpty() {
		t.Fatalf("Load missing file: expected empty store, got %+v", s.Services)
	}
}

func TestLoadCorruptFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "generated.yml")
	if err := os.WriteFile(path, []byte("services: [this is: not a map"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatalf("Load corrupt file: expected error, got nil")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deep", "nested", DefaultRelPath)
	s := New()
	s.SetIfAbsent("main", "app_key", "base64:Xa3==")
	s.SetIfAbsent("magento", "crypt_key", "241f4fa60be8f69638343cacc5a1a192")

	if err := Save(path, s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Get("main", "app_key") != "base64:Xa3==" {
		t.Errorf("round-trip main/app_key = %q", got.Get("main", "app_key"))
	}
	if got.Get("magento", "crypt_key") != "241f4fa60be8f69638343cacc5a1a192" {
		t.Errorf("round-trip magento/crypt_key = %q", got.Get("magento", "crypt_key"))
	}
}

func TestSaveUsesSecretFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultRelPath)
	s := New()
	s.SetIfAbsent("main", "app_key", "base64:Xa3==")

	if err := Save(path, s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// The store holds service secrets, so the file must not be world/group readable.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 0600", perm)
	}
}

func TestSaveNilStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generated.yml")
	if err := Save(path, nil); err == nil {
		t.Fatalf("Save nil: expected error, got nil")
	}
}

func TestSaveAtomicNoTempLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "generated.yml")
	s := New()
	s.SetIfAbsent("main", "app_key", "v")
	if err := Save(path, s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".generated-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestMultiLineBlockScalarRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generated.yml")
	multi := "-----BEGIN KEY-----\nline1\nline2\n-----END KEY-----"
	s := New()
	s.SetIfAbsent("svc", "pem", multi)
	if err := Save(path, s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Get("svc", "pem") != multi {
		t.Errorf("multi-line round-trip mismatch:\n got %q\nwant %q", got.Get("svc", "pem"), multi)
	}
}

func TestSetIfAbsent(t *testing.T) {
	s := New()
	if !s.SetIfAbsent("svc", "k", "first") {
		t.Fatalf("first SetIfAbsent: expected true (wrote)")
	}
	if s.SetIfAbsent("svc", "k", "second") {
		t.Fatalf("second SetIfAbsent: expected false (no overwrite)")
	}
	if got := s.Get("svc", "k"); got != "first" {
		t.Errorf("value after no-overwrite = %q, want %q", got, "first")
	}
}

func TestSetIfAbsentOnNilStoreServices(t *testing.T) {
	s := &Store{} // Services nil
	if !s.SetIfAbsent("svc", "k", "v") {
		t.Fatalf("SetIfAbsent on nil-map store: expected true")
	}
	if s.Get("svc", "k") != "v" {
		t.Errorf("Get after SetIfAbsent on nil-map store")
	}
}

func TestHasAndGet(t *testing.T) {
	s := New()
	s.SetIfAbsent("svc", "k", "v")
	if !s.Has("svc", "k") {
		t.Errorf("Has present field: expected true")
	}
	if s.Has("svc", "missing") {
		t.Errorf("Has missing field: expected false")
	}
	if s.Has("other", "k") {
		t.Errorf("Has missing service: expected false")
	}
	if got := s.Get("svc", "missing"); got != "" {
		t.Errorf("Get missing = %q, want empty", got)
	}
}

func TestClearServiceVsClearAll(t *testing.T) {
	s := New()
	s.SetIfAbsent("a", "k", "1")
	s.SetIfAbsent("b", "k", "2")

	s.ClearService("a")
	if s.Has("a", "k") {
		t.Errorf("ClearService(a): a still present")
	}
	if !s.Has("b", "k") {
		t.Errorf("ClearService(a): b should be untouched")
	}

	s.SetIfAbsent("a", "k", "1")
	s.ClearAll()
	if !s.IsEmpty() {
		t.Errorf("ClearAll: store not empty")
	}
}

func TestServiceReturnsCopy(t *testing.T) {
	s := New()
	s.SetIfAbsent("svc", "k", "v")
	m := s.Service("svc")
	m["k"] = "mutated"
	if s.Get("svc", "k") != "v" {
		t.Errorf("Service() returned a non-copy: store was mutated")
	}
	if got := s.Service("absent"); len(got) != 0 {
		t.Errorf("Service(absent) = %v, want empty map", got)
	}
}

func TestIsEmpty(t *testing.T) {
	if !New().IsEmpty() {
		t.Errorf("New().IsEmpty() = false")
	}
	if !(&Store{}).IsEmpty() {
		t.Errorf("zero-value Store.IsEmpty() = false")
	}
	var nilStore *Store
	if !nilStore.IsEmpty() {
		t.Errorf("nil store IsEmpty() = false")
	}
	s := New()
	s.SetIfAbsent("svc", "k", "v")
	if s.IsEmpty() {
		t.Errorf("populated store IsEmpty() = true")
	}
}
