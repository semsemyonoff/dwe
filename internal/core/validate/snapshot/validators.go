package snapshot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	coresnap "github.com/semsemyonoff/dwe/internal/core/workflow/snapshot"
	"github.com/semsemyonoff/dwe/internal/core/workflow/snapshot/meta"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

const diagFile = "devbox/snapshot.yml"

// configLoadableValidator surfaces the outcome of LoadSnapshotConfig.
// Silent when the file is absent; error when a real parse failure happened.
// Implements DomainLevelValidator so it runs even when the user scopes to a
// specific snapshot name — a broken snapshot.yml prevents any per-snapshot
// validator from loading.
type configLoadableValidator struct{ err error }

func (v *configLoadableValidator) ID() string          { return "config_loadable" }
func (v *configLoadableValidator) Domain() string      { return "snapshot" }
func (v *configLoadableValidator) IsDomainLevel() bool { return true }
func (v *configLoadableValidator) Run(_ validate.Context) []validate.Diagnostic {
	if v.err == nil {
		return nil
	}
	if errors.Is(v.err, os.ErrNotExist) {
		return nil
	}
	return []validate.Diagnostic{{
		Severity: validate.SeverityError,
		Domain:   "snapshot",
		Target:   "config_loadable",
		File:     diagFile,
		Message:  v.err.Error(),
	}}
}

// createDefinedValidator emits an info when create: is missing — create will
// refuse to run, but the project may legitimately use snapshots for restore-
// only flows so this is not an error.
type createDefinedValidator struct{ cfg *config.SnapshotConfig }

func (v *createDefinedValidator) ID() string     { return "create_defined" }
func (v *createDefinedValidator) Domain() string { return "snapshot" }
func (v *createDefinedValidator) Run(_ validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	if v.cfg.Create != nil && len(v.cfg.Create.Steps) > 0 {
		return nil
	}
	return []validate.Diagnostic{{
		Severity: validate.SeverityInfo,
		Domain:   "snapshot",
		Target:   "create_defined",
		File:     diagFile,
		Message:  "no create: workflow defined; `devbox snapshot create` will refuse to run",
	}}
}

// restoreDefinedValidator mirrors createDefinedValidator.
type restoreDefinedValidator struct{ cfg *config.SnapshotConfig }

func (v *restoreDefinedValidator) ID() string     { return "restore_defined" }
func (v *restoreDefinedValidator) Domain() string { return "snapshot" }
func (v *restoreDefinedValidator) Run(_ validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	if v.cfg.Restore != nil && len(v.cfg.Restore.Steps) > 0 {
		return nil
	}
	// Explicitly present but empty steps block is a config error; absent block is advisory.
	if v.cfg.Restore != nil && len(v.cfg.Restore.Steps) == 0 {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "snapshot",
			Target:   "restore_defined",
			File:     diagFile,
			Message:  "restore: block has no steps; `devbox snapshot restore` will refuse to run",
		}}
	}
	return []validate.Diagnostic{{
		Severity: validate.SeverityInfo,
		Domain:   "snapshot",
		Target:   "restore_defined",
		File:     diagFile,
		Message:  "no restore: workflow defined; `devbox snapshot restore` will refuse to run",
	}}
}

// variantPairingValidator warns when a create variant has no matching restore
// variant and the default restore block is also absent — restoring such a
// snapshot would have nothing to dispatch to.
type variantPairingValidator struct{ cfg *config.SnapshotConfig }

func (v *variantPairingValidator) ID() string     { return "variant_pairing" }
func (v *variantPairingValidator) Domain() string { return "snapshot" }
func (v *variantPairingValidator) Run(_ validate.Context) []validate.Diagnostic {
	if v.cfg == nil || v.cfg.Create == nil {
		return nil
	}
	hasDefaultRestore := v.cfg.Restore != nil && len(v.cfg.Restore.Steps) > 0
	var diags []validate.Diagnostic
	for name := range v.cfg.Create.Variants {
		if hasDefaultRestore {
			continue
		}
		if v.cfg.Restore == nil {
			diags = append(diags, variantPairingDiag(name, "no restore: block defined"))
			continue
		}
		if _, ok := v.cfg.Restore.Variants[name]; !ok {
			diags = append(diags, variantPairingDiag(name, "no matching restore.variants entry and no default restore steps"))
		}
	}
	return diags
}

func variantPairingDiag(name, reason string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: validate.SeverityWarning,
		Domain:   "snapshot",
		Target:   "variant_pairing." + name,
		File:     diagFile,
		Message:  fmt.Sprintf("create variant %q cannot be restored: %s", name, reason),
		Hint:     fmt.Sprintf("add restore.variants.%s or a default restore: block", name),
	}
}

// rollbackTargetExistsValidator warns when rollback_target points to a
// snapshot that does not exist on disk.
type rollbackTargetExistsValidator struct {
	cfg     *config.SnapshotConfig
	baseDir string
}

func (v *rollbackTargetExistsValidator) ID() string     { return "rollback_target_exists" }
func (v *rollbackTargetExistsValidator) Domain() string { return "snapshot" }
func (v *rollbackTargetExistsValidator) Run(_ validate.Context) []validate.Diagnostic {
	if v.cfg == nil || v.cfg.RollbackTarget == "" {
		return nil
	}
	dir := meta.SnapshotDir(v.baseDir, v.cfg, v.cfg.RollbackTarget)
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	return []validate.Diagnostic{{
		Severity: validate.SeverityWarning,
		Domain:   "snapshot",
		Target:   "rollback_target_exists",
		File:     diagFile,
		Message:  fmt.Sprintf("rollback_target %q has no snapshot at %s", v.cfg.RollbackTarget, dir),
		Hint:     "create the snapshot, or update rollback_target",
	}}
}

// templateScopeValidator walks snapshot.yml step when:/with: expressions and
// rejects ${snapshot.*} uses that the active scope forbids. The runtime
// compile-time check (tpl.RenderCommand) covers this at execution time, but a
// validate-time pass gives earlier feedback.
type templateScopeValidator struct{ cfg *config.SnapshotConfig }

func (v *templateScopeValidator) ID() string     { return "template_scope" }
func (v *templateScopeValidator) Domain() string { return "snapshot" }
func (v *templateScopeValidator) Run(_ validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	var diags []validate.Diagnostic
	check := func(kind string, w *config.SnapshotWorkflow, scope tpl.SnapshotScope) {
		if w == nil {
			return
		}
		walkSteps(w.Steps, "", func(stepPath, where, expr string) {
			if err := scopeCheck(expr, scope); err != nil {
				diags = append(diags, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "snapshot",
					Target:   fmt.Sprintf("template_scope.%s.%s.%s", kind, stepPath, where),
					File:     diagFile,
					Message:  err.Error(),
				})
			}
		})
		for vname, variant := range w.Variants {
			walkSteps(variant.Steps, "", func(stepPath, where, expr string) {
				if err := scopeCheck(expr, scope); err != nil {
					diags = append(diags, validate.Diagnostic{
						Severity: validate.SeverityError,
						Domain:   "snapshot",
						Target:   fmt.Sprintf("template_scope.%s.variants[%s].%s.%s", kind, vname, stepPath, where),
						File:     diagFile,
						Message:  err.Error(),
					})
				}
			})
		}
	}
	check("create", v.cfg.Create, tpl.SnapshotScopeCreate)
	check("restore", v.cfg.Restore, tpl.SnapshotScopeRestoreOrRemove)
	check("remove", v.cfg.Remove, tpl.SnapshotScopeRestoreOrRemove)
	return diags
}

// servicesDiffValidator emits a single info-severity diagnostic per snapshot
// whose captured service set diverges from the current project's effective
// service set. The check is silent when:
//   - the manifest is missing or unparseable (perSnapshotValidator already errors),
//   - the manifest captured no services (older format or unconfigured project),
//   - the current cfg is nil (validate ran without a loadable devbox.yml),
//   - or the diff is empty.
//
// The diagnostic shares the snapshot's ID so `devbox validate snapshot <name>`
// filters this in alongside the other per-snapshot checks.
type servicesDiffValidator struct {
	name  string
	entry coresnap.Entry
	cfg   *config.DweConfig
}

func (v *servicesDiffValidator) ID() string     { return v.name }
func (v *servicesDiffValidator) Domain() string { return "snapshot" }

func (v *servicesDiffValidator) Run(_ validate.Context) []validate.Diagnostic {
	if v.entry.Manifest == nil || len(v.entry.Manifest.Project.Services) == 0 || v.cfg == nil {
		return nil
	}
	diff := coresnap.DiffServices(v.entry.Manifest.Project.Services, v.cfg.Services)
	if diff.IsEmpty() {
		return nil
	}
	return []validate.Diagnostic{{
		Severity: validate.SeverityInfo,
		Domain:   "snapshot",
		Target:   fmt.Sprintf("%s.services_diff", v.name),
		File:     filepath.Join(v.entry.Dir, meta.ManifestFileName),
		Message:  "captured service set diverges from current project",
		Hint:     coresnap.FormatServicesDiff(diff),
	}}
}

// walkSteps invokes visit(stepPath, where, expr) for every templated string in
// each step: when:, confirm:, and each with: value. Parallel containers
// recurse into their child steps with a qualified path prefix so callers can
// distinguish outer steps[0] from steps[1].parallel.steps[0].
func walkSteps(steps []model.WorkflowStep, prefix string, visit func(stepPath, where, expr string)) {
	for i, s := range steps {
		p := fmt.Sprintf("%ssteps[%d]", prefix, i)
		if s.When != "" {
			visit(p, "when", s.When)
		}
		if s.Confirm != "" {
			visit(p, "confirm", s.Confirm)
		}
		for k, val := range s.With {
			visit(p, "with."+k, val)
		}
		if s.Parallel != nil {
			walkSteps(s.Parallel.Steps, p+".parallel.", visit)
		}
	}
}

// scopeCheck attempts a render under the given scope with dummy snapshot vars.
// It returns only template/scope errors — non-snapshot template errors are
// suppressed since they're noise (e.g. ${param.x} resolves to ""; an unknown
// raw dot-path also resolves to ""). The function is intentionally narrow:
// any error containing "${snapshot" in its message is treated as a scope
// violation.
func scopeCheck(expr string, scope tpl.SnapshotScope) error {
	dummy := map[string]any{
		"name":        "x",
		"path":        "/tmp/x",
		"description": "",
		"variant":     "",
		"created_at":  "2026-01-01T00:00:00Z",
	}
	rc := &tpl.RenderContext{
		Snapshot:      dummy,
		SnapshotScope: scope,
		Raw:           map[string]any{},
		Params:        map[string]any{},
		Context:       map[string]any{},
	}
	if _, err := tpl.RenderCommand(expr, rc); err != nil {
		return err
	}
	return nil
}

// perSnapshotValidator emits manifest_valid / artifacts_exist /
// last_create_failed / (optional) checksums diagnostics for one snapshot.
// All four sub-checks share a single Validator ID (the snapshot name) so that
// `devbox validate snapshot <name>` filters to a single snapshot's checks.
type perSnapshotValidator struct {
	baseDir         string
	cfg             *config.SnapshotConfig
	name            string
	entry           coresnap.Entry
	verifyChecksums bool
}

func (v *perSnapshotValidator) ID() string     { return v.name }
func (v *perSnapshotValidator) Domain() string { return "snapshot" }

func (v *perSnapshotValidator) Run(_ validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	if v.entry.Manifest == nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "snapshot",
			Target:   fmt.Sprintf("%s.manifest_valid", v.name),
			File:     filepath.Join(v.entry.Dir, meta.ManifestFileName),
			Message:  "manifest is missing or unparseable",
			Hint:     "remove the snapshot directory or restore a valid manifest.yml",
		}}
	}

	// artifacts_exist: every manifest-listed artifact must be present on disk.
	missing := 0
	for _, a := range v.entry.Manifest.Artifacts {
		p := filepath.Join(v.entry.Dir, filepath.FromSlash(a.Path))
		if _, err := os.Stat(p); err != nil {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "snapshot",
				Target:   fmt.Sprintf("%s.artifacts_exist", v.name),
				File:     p,
				Message:  fmt.Sprintf("artifact %q listed in manifest is missing", a.Path),
			})
			missing++
		}
	}
	if missing == 0 && len(v.entry.Manifest.Artifacts) > 0 {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   "snapshot",
			Target:   fmt.Sprintf("%s.artifacts_exist", v.name),
			File:     v.entry.Dir,
		})
	}

	// last_create_failed: info when the most recent create attempt was not ok.
	if lc := v.entry.Manifest.LastCreate; lc != nil && lc.Status != "" && lc.Status != meta.StatusOk {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityInfo,
			Domain:   "snapshot",
			Target:   fmt.Sprintf("%s.last_create_failed", v.name),
			File:     filepath.Join(v.entry.Dir, meta.ManifestFileName),
			Message:  fmt.Sprintf("last create attempt was %q (failed_step=%q)", lc.Status, lc.FailedStep),
		})
	}

	// checksums (gated by --verify): rehash the on-disk dir and compare to
	// manifest. Skip silently when off.
	if v.verifyChecksums && missing == 0 {
		current, err := meta.ScanArtifacts(v.entry.Dir)
		if err != nil {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "snapshot",
				Target:   fmt.Sprintf("%s.checksums", v.name),
				File:     v.entry.Dir,
				Message:  fmt.Sprintf("rescan failed: %v", err),
			})
		} else {
			byPath := make(map[string]meta.ArtifactInfo, len(current))
			for _, a := range current {
				byPath[a.Path] = a
			}
			mismatch := 0
			for _, a := range v.entry.Manifest.Artifacts {
				cur, ok := byPath[a.Path]
				if !ok {
					continue // already reported by artifacts_exist
				}
				if cur.Sha256 != a.Sha256 || cur.Size != a.Size {
					diags = append(diags, validate.Diagnostic{
						Severity: validate.SeverityWarning,
						Domain:   "snapshot",
						Target:   fmt.Sprintf("%s.checksums", v.name),
						File:     filepath.Join(v.entry.Dir, filepath.FromSlash(a.Path)),
						Message:  fmt.Sprintf("artifact %q sha256 or size mismatch (manifest sha256=%s size=%d; on-disk sha256=%s size=%d)", a.Path, a.Sha256, a.Size, cur.Sha256, cur.Size),
					})
					mismatch++
				}
			}
			if mismatch == 0 {
				diags = append(diags, validate.Diagnostic{
					Severity: validate.SeverityOK,
					Domain:   "snapshot",
					Target:   fmt.Sprintf("%s.checksums", v.name),
					File:     v.entry.Dir,
				})
			}
		}
	}

	return diags
}
