package snapshot

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/workflow/snapshot/meta"
)

func writeStringFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func newRegistryWith(t *testing.T, id, run string) *registry.Registry {
	t.Helper()
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID:    id,
		Type:  model.CommandTypeShell,
		Files: map[string]model.FileSpec{},
		Cmd:   run,
	})
	return reg
}

func newSnapCfgWithCreate(steps ...model.WorkflowStep) *config.SnapshotConfig {
	return &config.SnapshotConfig{
		Create: &config.SnapshotWorkflow{Steps: steps},
	}
}

func TestCreate_EndToEndWritesMarkerAndManifest(t *testing.T) {
	tmp := t.TempDir()
	reg := newRegistryWith(t, "fake.marker",
		`mkdir -p "${snapshot.path}/data" && printf hello > "${snapshot.path}/data/marker"`)

	snapCfg := newSnapCfgWithCreate(model.WorkflowStep{Command: "fake.marker"})
	fixed := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)

	var out, errBuf bytes.Buffer
	res, err := Create(context.Background(), CreateParams{
		Cfg:           testCfg(),
		SnapCfg:       snapCfg,
		Registry:      reg,
		BaseDir:       tmp,
		Name:          "snap1",
		Description:   "hello world",
		DevboxVersion: "0.42.0",
		Now:           func() time.Time { return fixed },
		Stdout:        &out,
		Stderr:        &errBuf,
	})
	if err != nil {
		t.Fatalf("Create: %v (stderr=%s)", err, errBuf.String())
	}
	if res.Status != meta.StatusOk {
		t.Fatalf("status = %q want ok", res.Status)
	}

	// Marker file must exist with the expected content.
	markerPath := filepath.Join(res.SnapshotDir, "data", "marker")
	body, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("marker contents = %q want %q", string(body), "hello")
	}

	// Manifest must list the marker file with size + sha256.
	m, err := meta.LoadManifest(res.ManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if m.Name != "snap1" {
		t.Errorf("name = %q", m.Name)
	}
	if m.Description != "hello world" {
		t.Errorf("description = %q", m.Description)
	}
	if !m.CreatedAt.Equal(fixed) {
		t.Errorf("createdAt = %v want %v", m.CreatedAt, fixed)
	}
	if m.DevboxVersion != "0.42.0" {
		t.Errorf("version = %q", m.DevboxVersion)
	}
	if len(m.Artifacts) != 1 {
		t.Fatalf("artifacts: got %d want 1: %+v", len(m.Artifacts), m.Artifacts)
	}
	a := m.Artifacts[0]
	if a.Path != "data/marker" {
		t.Errorf("artifact path = %q", a.Path)
	}
	if a.Size != int64(len("hello")) {
		t.Errorf("artifact size = %d", a.Size)
	}
	// sha256("hello")
	const wantHash = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if a.Sha256 != wantHash {
		t.Errorf("artifact sha256 = %q want %q", a.Sha256, wantHash)
	}
	if m.LastCreate == nil || m.LastCreate.Status != meta.StatusOk {
		t.Errorf("last_create = %+v", m.LastCreate)
	}

	// Current pointer points at the snapshot.
	cur, err := meta.ReadCurrent(tmp)
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	if cur != "snap1" {
		t.Errorf("current = %q want snap1", cur)
	}
}

func TestCreate_PropagatesSnapshotNameAndPathToShellRun(t *testing.T) {
	tmp := t.TempDir()
	reg := newRegistryWith(t, "fake.echo", `echo "PATH=${snapshot.path} NAME=${snapshot.name}" > "${snapshot.path}/out.txt"`)

	snapCfg := newSnapCfgWithCreate(model.WorkflowStep{Command: "fake.echo"})

	var out, errBuf bytes.Buffer
	res, err := Create(context.Background(), CreateParams{
		Cfg:      testCfg(),
		SnapCfg:  snapCfg,
		Registry: reg,
		BaseDir:  tmp,
		Name:     "vars",
		Stdout:   &out,
		Stderr:   &errBuf,
	})
	if err != nil {
		t.Fatalf("Create: %v (stderr=%s)", err, errBuf.String())
	}

	body, err := os.ReadFile(filepath.Join(res.SnapshotDir, "out.txt"))
	if err != nil {
		t.Fatalf("read out.txt: %v", err)
	}
	wantPath := "PATH=" + res.SnapshotDir
	if !strings.Contains(string(body), wantPath) {
		t.Errorf("out.txt = %q\nwant substring %q", string(body), wantPath)
	}
	if !strings.Contains(string(body), "NAME=vars") {
		t.Errorf("out.txt = %q\nwant NAME=vars", string(body))
	}
}

func TestCreate_VariantMissingErrorsBeforeAnyFilesystemMutation(t *testing.T) {
	tmp := t.TempDir()
	reg := newRegistryWith(t, "x", "true")

	snapCfg := &config.SnapshotConfig{
		Create: &config.SnapshotWorkflow{
			Steps: []model.WorkflowStep{{Command: "x"}},
			Variants: map[string]config.SnapshotVariant{
				"only": {Steps: []model.WorkflowStep{{Command: "x"}}},
			},
		},
	}

	_, err := Create(context.Background(), CreateParams{
		Cfg:      testCfg(),
		SnapCfg:  snapCfg,
		Registry: reg,
		BaseDir:  tmp,
		Name:     "bad",
		Variant:  "does-not-exist",
	})
	if err == nil {
		t.Fatal("expected error for missing variant")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error %q must name the variant", err)
	}
	// No snapshot directory must exist.
	if _, statErr := os.Stat(filepath.Join(tmp, "snapshots", "bad")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("snapshot dir should not exist on variant error; statErr=%v", statErr)
	}
}

func TestCreate_OverwriteWithoutYesRefuses(t *testing.T) {
	tmp := t.TempDir()
	reg := newRegistryWith(t, "x", "true")
	snapCfg := newSnapCfgWithCreate(model.WorkflowStep{Command: "x"})

	// First create succeeds.
	if _, err := Create(context.Background(), CreateParams{
		Cfg: testCfg(), SnapCfg: snapCfg, Registry: reg, BaseDir: tmp, Name: "twice", SkipConfirm: true,
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Second create without -y and a nil ConfirmOverwrite refuses.
	_, err := Create(context.Background(), CreateParams{
		Cfg: testCfg(), SnapCfg: snapCfg, Registry: reg, BaseDir: tmp, Name: "twice",
		ConfirmOverwrite: nil, // explicit nil → refuse
	})
	if err == nil {
		t.Fatal("expected cancellation error on second create")
	}
	var cancelled *CreateCancelledError
	if !errors.As(err, &cancelled) {
		t.Errorf("err = %T %v, want *CreateCancelledError", err, err)
	}
}

func TestCreate_OverwriteWithYesProceeds(t *testing.T) {
	tmp := t.TempDir()
	reg := newRegistryWith(t, "x", `printf v1 > "${snapshot.path}/marker"`)
	snapCfg := newSnapCfgWithCreate(model.WorkflowStep{Command: "x"})

	if _, err := Create(context.Background(), CreateParams{
		Cfg: testCfg(), SnapCfg: snapCfg, Registry: reg, BaseDir: tmp, Name: "ow", SkipConfirm: true,
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Overwrite with a fresh marker.
	reg2 := newRegistryWith(t, "x", `printf v2 > "${snapshot.path}/marker"`)
	res, err := Create(context.Background(), CreateParams{
		Cfg: testCfg(), SnapCfg: snapCfg, Registry: reg2, BaseDir: tmp, Name: "ow", SkipConfirm: true,
	})
	if err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(res.SnapshotDir, "marker"))
	if string(body) != "v2" {
		t.Errorf("marker after overwrite = %q want v2", string(body))
	}
}

func TestCreate_OverwriteUsingConfirmCallback(t *testing.T) {
	tmp := t.TempDir()
	reg := newRegistryWith(t, "x", "true")
	snapCfg := newSnapCfgWithCreate(model.WorkflowStep{Command: "x"})

	if _, err := Create(context.Background(), CreateParams{
		Cfg: testCfg(), SnapCfg: snapCfg, Registry: reg, BaseDir: tmp, Name: "cb", SkipConfirm: true,
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	called := 0
	_, err := Create(context.Background(), CreateParams{
		Cfg: testCfg(), SnapCfg: snapCfg, Registry: reg, BaseDir: tmp, Name: "cb",
		ConfirmOverwrite: func() (bool, error) { called++; return true, nil },
	})
	if err != nil {
		t.Fatalf("overwrite via callback: %v", err)
	}
	if called != 1 {
		t.Errorf("callback called %d times, want 1", called)
	}
}

func TestCreate_WorkflowFailureRecordsFailedStatusAndKeepsDir(t *testing.T) {
	tmp := t.TempDir()
	reg := newRegistryWith(t, "boom", "false")
	snapCfg := newSnapCfgWithCreate(model.WorkflowStep{Command: "boom"})

	res, err := Create(context.Background(), CreateParams{
		Cfg: testCfg(), SnapCfg: snapCfg, Registry: reg, BaseDir: tmp, Name: "fail",
	})
	if err == nil {
		t.Fatal("expected workflow failure")
	}
	if res == nil {
		t.Fatal("res = nil; expected non-nil even on failure")
	}
	if res.Status != meta.StatusFailed {
		t.Errorf("status = %q want %q", res.Status, meta.StatusFailed)
	}
	// Snapshot directory is kept.
	if _, statErr := os.Stat(res.SnapshotDir); statErr != nil {
		t.Errorf("snapshot dir should be kept on failure: %v", statErr)
	}
	// Manifest records last_create.status = "failed".
	m, mErr := meta.LoadManifest(res.ManifestPath)
	if mErr != nil {
		t.Fatalf("load manifest: %v", mErr)
	}
	if m.LastCreate == nil || m.LastCreate.Status != meta.StatusFailed {
		t.Errorf("last_create = %+v", m.LastCreate)
	}
	// Current pointer is NOT updated.
	cur, _ := meta.ReadCurrent(tmp)
	if cur != "" {
		t.Errorf("current = %q want empty (not updated on failure)", cur)
	}
}

func TestCreate_CapturesDevboxFiles(t *testing.T) {
	tmp := t.TempDir()
	// Seed a local.yml and a deploy state file.
	writeStringFile(t, filepath.Join(tmp, "workspace", "local.yml"), "key: value\n")
	writeStringFile(t, filepath.Join(tmp, ".devbox", "deploy", "state.yml"), "project:\n  config_hash: abc\n")

	reg := newRegistryWith(t, "x", "true")
	snapCfg := newSnapCfgWithCreate(model.WorkflowStep{Command: "x"})

	res, err := Create(context.Background(), CreateParams{
		Cfg: testCfg(), SnapCfg: snapCfg, Registry: reg, BaseDir: tmp, Name: "df",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	localBody, err := os.ReadFile(filepath.Join(res.SnapshotDir, "devbox", "local.yml"))
	if err != nil {
		t.Fatalf("local.yml: %v", err)
	}
	if string(localBody) != "key: value\n" {
		t.Errorf("captured local.yml = %q", string(localBody))
	}
	deployBody, err := os.ReadFile(filepath.Join(res.SnapshotDir, "devbox", "deploy-state.yml"))
	if err != nil {
		t.Fatalf("deploy-state.yml: %v", err)
	}
	if !strings.Contains(string(deployBody), "config_hash: abc") {
		t.Errorf("captured deploy state = %q", string(deployBody))
	}

	m, _ := meta.LoadManifest(res.ManifestPath)
	if m.DevboxFiles.LocalYML != "devbox/local.yml" {
		t.Errorf("DevboxFiles.LocalYML = %q", m.DevboxFiles.LocalYML)
	}
	if m.DevboxFiles.DeployState != "devbox/deploy-state.yml" {
		t.Errorf("DevboxFiles.DeployState = %q", m.DevboxFiles.DeployState)
	}
	// Project config hash is sourced from the deploy state file.
	if m.Project.ConfigHash != "abc" {
		t.Errorf("Project.ConfigHash = %q want abc", m.Project.ConfigHash)
	}
}

func TestCreate_CapturesServicesSorted(t *testing.T) {
	tmp := t.TempDir()
	reg := newRegistryWith(t, "x", "true")
	snapCfg := newSnapCfgWithCreate(model.WorkflowStep{Command: "x"})

	cfg := testCfg()
	cfg.Services = map[string]config.ServiceConfig{
		"main": {Enabled: true},
		"cdn":  {Enabled: false},
		"db":   {Enabled: true},
	}

	res, err := Create(context.Background(), CreateParams{
		Cfg: cfg, SnapCfg: snapCfg, Registry: reg, BaseDir: tmp, Name: "svc",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	m, err := meta.LoadManifest(res.ManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	want := []meta.ServiceSnapshot{
		{Name: "cdn", Enabled: false},
		{Name: "db", Enabled: true},
		{Name: "main", Enabled: true},
	}
	if len(m.Project.Services) != len(want) {
		t.Fatalf("services: got %+v want %+v", m.Project.Services, want)
	}
	for i, s := range m.Project.Services {
		if s != want[i] {
			t.Errorf("services[%d] = %+v want %+v", i, s, want[i])
		}
	}
}

func TestCreate_StripsPreserveKeysFromLocalYML(t *testing.T) {
	tmp := t.TempDir()
	writeStringFile(t, filepath.Join(tmp, "workspace", "local.yml"),
		"services:\n  main:\n    ports:\n      - 9090\n    enabled: true\n")

	reg := newRegistryWith(t, "x", "true")
	snapCfg := newSnapCfgWithCreate(model.WorkflowStep{Command: "x"})
	snapCfg.LocalYML = config.LocalYMLPolicy{PreserveKeys: []string{"services.main.ports"}}

	res, err := Create(context.Background(), CreateParams{
		Cfg: testCfg(), SnapCfg: snapCfg, Registry: reg, BaseDir: tmp, Name: "strip",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(res.SnapshotDir, "devbox", "local.yml"))
	if err != nil {
		t.Fatalf("read snapshot local.yml: %v", err)
	}
	if strings.Contains(string(body), "ports") {
		t.Fatalf("preserved key not stripped, body=%q", string(body))
	}
	if !strings.Contains(string(body), "enabled: true") {
		t.Fatalf("non-preserved key dropped, body=%q", string(body))
	}
}

func TestCreate_InvalidNameRejectedBeforeMutation(t *testing.T) {
	tmp := t.TempDir()
	_, err := Create(context.Background(), CreateParams{
		Cfg:     testCfg(),
		SnapCfg: newSnapCfgWithCreate(model.WorkflowStep{Command: "x"}),
		BaseDir: tmp,
		Name:    "Bad Name",
	})
	if err == nil {
		t.Fatal("expected name validation error")
	}
}

func TestCreate_NoCreateBlockErrors(t *testing.T) {
	tmp := t.TempDir()
	_, err := Create(context.Background(), CreateParams{
		Cfg:     testCfg(),
		SnapCfg: &config.SnapshotConfig{}, // no Create block
		BaseDir: tmp,
		Name:    "x",
	})
	if err == nil {
		t.Fatal("expected error when create block missing")
	}
}
