package keygate

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// TestInventory_MarkersFilesAndSymlink pins the whole scan in one shape: a
// readable marker, a readable .age source, and a symlinked .age that must be
// REPORTED rather than skipped — a symlinked "secret" is exactly the thing a
// status report may not stay silent about.
func TestInventory_MarkersFilesAndSymlink(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	id := installIdentity(t)

	marker := encrypt(t, "s3cret", id.Recipient())
	layers := layersWithMarker(t, root, id.Recipient(), marker)

	real := writeAgeFile(t, root, "app/creds.json.age", id.Recipient(), `{"ok":true}`)
	link := filepath.Join(root, "workspace", "templates", "config", "app", "link.age")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	inv, err := Inventory(root, layers, LoadIdentitySet(id.Recipient()))
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if !inv.HasSecrets() {
		t.Fatal("HasSecrets() = false on a project with a marker and .age files")
	}
	if len(inv.Markers) != 1 || inv.Markers[0].State != StateDecrypted || inv.Markers[0].Reason != "" {
		t.Fatalf("markers = %+v, want one decrypted row", inv.Markers)
	}
	if inv.Markers[0].Layer != filepath.Join("workspace", "defaults.yml") {
		t.Errorf("marker layer = %q, want a project-relative path", inv.Markers[0].Layer)
	}
	if len(inv.Files) != 2 {
		t.Fatalf("files = %+v, want 2 (the real file and the refused symlink)", inv.Files)
	}
	var linkRow FileRow
	for _, f := range inv.Files {
		if filepath.Base(f.File) == "link.age" {
			linkRow = f
		}
	}
	// The reason is the closed-vocabulary token; the refusal itself — which
	// carries an absolute path — travels in Detail, so a consumer switching on
	// `reason` is never handed free-form text.
	if linkRow.State != StateNotDecryptable || linkRow.Reason != ReasonUnreadable ||
		!strings.Contains(linkRow.Detail, "symlink") {
		t.Errorf("symlink row = %+v, want a not-decryptable/unreadable row detailing the symlink", linkRow)
	}

	// The report's two counters: the symlink is not readable, so it is not counted.
	markers, files := inv.Readable()
	if markers != 1 || files != 1 {
		t.Errorf("Readable() = (%d, %d), want (1, 1)", markers, files)
	}
}

// TestInventory_NoIdentity pins the keyless machine: every row is unresolved
// for the same, single actionable reason.
func TestInventory_NoIdentity(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	layers := layersWithMarker(t, root, id.Recipient(), encrypt(t, "token", id.Recipient()))
	writeAgeFile(t, root, "app/creds.age", id.Recipient(), "hello")

	inv, err := Inventory(root, layers, LoadIdentitySet(id.Recipient()))
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if inv.IdentityErr == nil {
		t.Fatal("IdentityErr = nil with no keyfile present")
	}
	if len(inv.Markers) != 1 || inv.Markers[0].Reason != config.ReasonNoIdentity {
		t.Errorf("markers = %+v, want one no_identity row", inv.Markers)
	}
	if len(inv.Files) != 1 || inv.Files[0].State != StateNotDecryptable ||
		inv.Files[0].Reason != config.ReasonNoIdentity {
		t.Errorf("files = %+v, want one not-decryptable no_identity row", inv.Files)
	}
	if m, f := inv.Readable(); m != 0 || f != 0 {
		t.Errorf("Readable() = (%d, %d), want (0, 0)", m, f)
	}
}

// TestInventory_CorruptMarkerNeedsNoKey pins that a damaged payload is named as
// such without any identity — a keyless developer is never sent hunting for a
// key that would not have helped.
func TestInventory_CorruptMarkerNeedsNoKey(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	id := installIdentity(t)

	corrupt := secrets.MarkerPrefix + base64.StdEncoding.EncodeToString([]byte("garbage")) + "]"
	layers := layersWithMarker(t, root, id.Recipient(), corrupt)

	inv, err := Inventory(root, layers, LoadIdentitySet(id.Recipient()))
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if len(inv.Markers) != 1 || inv.Markers[0].Reason != config.ReasonCorrupt {
		t.Errorf("markers = %+v, want one corrupt row", inv.Markers)
	}
}

// TestInventory_CorruptAgeFileNeedsNoKey is the marker rule for a native pack
// source: a truncated `.age` is damage, not a key problem, and a machine with
// NO identity must still say so — otherwise the one keyless developer who could
// not have opened it anyway is sent looking for a key.
func TestInventory_CorruptAgeFileNeedsNoKey(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()

	path := filepath.Join(root, "workspace", "templates", "config", "app", "creds.age")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating pack dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("this is not an age file"), 0o644); err != nil {
		t.Fatalf("writing pack source: %v", err)
	}

	// No identity anywhere: the reason must still be the damage, not the key.
	inv, err := Inventory(root, nil, LoadIdentitySet("age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3fgh2p"))
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if len(inv.Files) != 1 || inv.Files[0].Reason != config.ReasonCorrupt {
		t.Errorf("files = %+v, want one corrupt row", inv.Files)
	}
}

// TestInventory_UnwalkableTemplatesDir pins that the walk keeps its error
// channel: an unreadable templates tree is a failure of the scan, not an empty
// inventory that would read as "nothing encrypted here".
func TestInventory_UnwalkableTemplatesDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	isolateHome(t)
	root := t.TempDir()
	id := installIdentity(t)
	writeAgeFile(t, root, "app/creds.age", id.Recipient(), "hello")

	dir := filepath.Join(root, "workspace", "templates", "config", "app")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if _, err := Inventory(root, nil, LoadIdentitySet(id.Recipient())); err == nil {
		t.Fatal("Inventory succeeded over an unreadable templates dir")
	}
}

// TestHasEncryptedSurface pins the cheap probe. The marker case deliberately
// uses ciphertext no key can open: the probe must answer from the SHAPE, never
// by decrypting — that is what keeps a healthy `dwe run` free of a per-run
// decrypt scan.
func TestHasEncryptedSurface(t *testing.T) {
	isolateHome(t)

	t.Run("marker with garbage ciphertext", func(t *testing.T) {
		root := t.TempDir()
		corrupt := secrets.MarkerPrefix + base64.StdEncoding.EncodeToString([]byte("garbage")) + "]"
		layers := layersWithMarker(t, root, "age1unused", corrupt)
		if !HasEncryptedSurface(root, layers) {
			t.Error("HasEncryptedSurface = false with a marker present")
		}
	})

	t.Run("age file only", func(t *testing.T) {
		root := t.TempDir()
		id, err := secrets.Keygen()
		if err != nil {
			t.Fatalf("keygen: %v", err)
		}
		writeAgeFile(t, root, "app/creds.age", id.Recipient(), "hello")
		if !HasEncryptedSurface(root, nil) {
			t.Error("HasEncryptedSurface = false with an .age source present")
		}
	})

	t.Run("nothing encrypted", func(t *testing.T) {
		root := t.TempDir()
		if HasEncryptedSurface(root, nil) {
			t.Error("HasEncryptedSurface = true on a project with no markers and no .age files")
		}
	})

	t.Run("no project root", func(t *testing.T) {
		if HasEncryptedSurface("", nil) {
			t.Error("HasEncryptedSurface = true without a project root")
		}
	})
}

// TestRelToRoot pins the display rule: relative inside the project, verbatim
// when the path cannot be related to it.
func TestRelToRoot(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "p")
	if got := RelToRoot(root, filepath.Join(root, "workspace", "a.yml")); got != filepath.Join("workspace", "a.yml") {
		t.Errorf("RelToRoot inside the root = %q", got)
	}
	if got := RelToRoot("", "/x/y"); got != "/x/y" {
		t.Errorf("RelToRoot with no root = %q, want the path as-is", got)
	}
}

// A marker a plaintext value in local.yml overrides still DECRYPTS, so state and
// reason cannot carry the finding: the row has to say both "this machine opens
// it" and "the project does not read it".
func TestInventory_ShadowedMarkerRow(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	id := installIdentity(t)

	marker := encrypt(t, "s3cret", id.Recipient())
	layersWithMarker(t, root, id.Recipient(), marker)
	if err := os.WriteFile(filepath.Join(root, "workspace", "local.yml"),
		[]byte("vars:\n  token: s3cret\n"), 0o644); err != nil {
		t.Fatalf("writing local.yml: %v", err)
	}
	layers := rawLayers(t, filepath.Join(root, "workspace.yml"))

	inv, err := Inventory(root, layers, LoadIdentitySet(id.Recipient()))
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if len(inv.Markers) != 1 {
		t.Fatalf("markers = %+v, want one row", inv.Markers)
	}
	row := inv.Markers[0]
	if row.State != StateDecrypted || row.Reason != "" {
		t.Errorf("row = %+v, want the decryptability verdict unchanged", row)
	}
	if row.ShadowedBy != filepath.Join("workspace", "local.yml") {
		t.Errorf("ShadowedBy = %q, want a project-relative workspace/local.yml", row.ShadowedBy)
	}
	if row.ShadowMatch != config.ShadowIdentical {
		t.Errorf("ShadowMatch = %q, want %q", row.ShadowMatch, config.ShadowIdentical)
	}
}
