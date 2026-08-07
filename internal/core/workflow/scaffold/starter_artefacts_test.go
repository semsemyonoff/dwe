package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/ai"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/envtest"
)

// starterArtefacts are the service-scoped artefacts a default `dwe init`
// writes: the per-service pipeline skeleton, the ai template pack (manifest +
// template), and the starter scenario.
var starterArtefacts = []string{
	"workspace/services/app/deploy.yml",
	"workspace/templates/ai/default/manifest.yml",
	"workspace/templates/ai/default/AGENTS.md.tmpl",
	"workspace/tests/smoke.yml",
}

// TestScaffold_ServiceScopedOutputsExist pins serviceScopedOutputs against the
// rendered plan. Without it, renaming a template would silently turn the
// drop-with-the-service rule into a no-op and the artefact would dangle.
func TestScaffold_ServiceScopedOutputsExist(t *testing.T) {
	plan := mustRender(t, newTestOptions())
	for _, path := range serviceScopedOutputs {
		if _, ok := plan[path]; !ok {
			t.Errorf("serviceScopedOutputs names %q, which the rendered plan does not contain; keys: %v", path, keys(plan))
		}
	}
}

// TestScaffold_StarterArtefactsWritten is the positive half: with a starter
// service every artefact lands on disk under the chosen service name.
func TestScaffold_StarterArtefactsWritten(t *testing.T) {
	dir := t.TempDir()
	opts := defaultValidityOptions(dir)
	opts.Service = "backend"
	if _, err := Scaffold(opts); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	for _, rel := range []string{
		"workspace/services/backend/deploy.yml",
		"workspace/templates/ai/default/manifest.yml",
		"workspace/templates/ai/default/AGENTS.md.tmpl",
		"workspace/tests/smoke.yml",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected %s on disk: %v", rel, err)
		}
	}
}

// TestScaffold_EmptyServiceDropsStarterArtefacts is the reason
// applyServicePlan grew beyond serviceTemplateDir: an ai pack or scenario that
// names the starter service must not survive when there is no starter service.
func TestScaffold_EmptyServiceDropsStarterArtefacts(t *testing.T) {
	dir := t.TempDir()
	opts := defaultValidityOptions(dir)
	opts.Service = ""
	if _, err := Scaffold(opts); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	for _, rel := range starterArtefacts {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s was written with no starter service; it references one and would dangle", rel)
		} else if !os.IsNotExist(err) {
			t.Errorf("stat %s: %v", rel, err)
		}
	}
	// The directories they lived in must not be left behind either.
	for _, rel := range []string{"workspace/tests", "workspace/templates"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			t.Errorf("empty directory %s survived with no starter service", rel)
		}
	}
}

// TestEmbeddedTemplates_StarterScenarioLoads covers the one scaffold file that
// cannot ship commented: the scenario loader rejects an empty document, so
// smoke.yml is active and must load through the real loader.
func TestEmbeddedTemplates_StarterScenarioLoads(t *testing.T) {
	dir := t.TempDir()
	if _, err := Scaffold(defaultValidityOptions(dir)); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	names, err := envtest.ListScenarios(dir)
	if err != nil {
		t.Fatalf("ListScenarios: %v", err)
	}
	if len(names) != 1 || names[0] != "smoke" {
		t.Fatalf("ListScenarios = %v, want [smoke]", names)
	}
	path, err := envtest.ScenarioPath(dir, "smoke")
	if err != nil {
		t.Fatalf("ScenarioPath: %v", err)
	}
	scn, err := envtest.LoadScenario(path)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	if scn.Description == "" {
		t.Error("starter scenario has no description; `dwe test list` would show a blank row")
	}
	// Assertions are deliberately absent: a fresh compose.yaml declares no
	// services, so the scenario's own deploy fails at start/up until the user
	// adds one — steps here would only add noise behind that failure.
	if len(scn.Steps) != 0 {
		t.Errorf("starter scenario declares %d steps; it must stay assertion-free until the service exists in compose", len(scn.Steps))
	}
}

// TestEmbeddedTemplates_ServiceDeployPipelineResolves takes the commented
// skeleton literally — uncomment it, load it through the strict service-deploy
// loader, and resolve every phase exactly as `dwe deploy plan` would. This is
// what proves the skeleton teaches a shape that actually runs, including the
// `source_clone` builtin params and the `check: auto` derivation.
func TestEmbeddedTemplates_ServiceDeployPipelineResolves(t *testing.T) {
	dir := t.TempDir()
	if _, err := Scaffold(defaultValidityOptions(dir)); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	path := filepath.Join(dir, "workspace", "services", "app", "deploy.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := os.WriteFile(path, uncommentInertBody(t, data), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	dep, err := config.LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if len(dep.Phases) == 0 {
		t.Fatal("uncommented service deploy.yml has no phases")
	}

	cfg, err := config.LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	var sawAutoCheck, sawSourceClone bool
	for _, phase := range dep.Phases {
		resolved, err := pipeline.ResolvePhaseSteps(cfg, nil, phase, "app")
		if err != nil {
			t.Fatalf("ResolvePhaseSteps(%s): %v", phase.Name, err)
		}
		for _, rs := range resolved {
			if rs.Step.Type == "builtin" && rs.Step.Cmd == "source_clone" {
				sawSourceClone = true
			}
			if config.IsAutoCheck(rs.Step.Check) {
				t.Errorf("step %q still carries the raw `auto` sentinel after resolve", rs.Step.Name)
			}
			// A derived check is `{type: builtin, cmd: shell}` carrying the
			// negated when: command in with.cmd — not in Check.Cmd.
			if rs.Step.Check != nil && rs.Step.Check.Cmd == "shell" {
				if inner, _ := rs.Step.Check.With["cmd"].(string); strings.Contains(inner, "node_modules") && strings.HasPrefix(inner, "! (") {
					sawAutoCheck = true
				}
			}
		}
	}
	if !sawSourceClone {
		t.Error("skeleton no longer uses the source_clone builtin")
	}
	if !sawAutoCheck {
		t.Error("skeleton no longer demonstrates `check: auto` (no derived check found)")
	}
}

// TestEmbeddedTemplates_AIPackRenders renders the scaffolded ai pack into the
// service hub the way `dwe render ai` does, proving the manifest, the template
// and the symlink entry all work against a real project.
func TestEmbeddedTemplates_AIPackRenders(t *testing.T) {
	dir := t.TempDir()
	if _, err := Scaffold(defaultValidityOptions(dir)); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	cfg, err := config.LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	svc := cfg.Services["app"]

	absRoot, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	packDir, packName, found, err := ai.ResolveTemplatePack(svc, cfg.Services, absRoot, "app")
	if err != nil {
		t.Fatalf("ResolveTemplatePack: %v", err)
	}
	if !found {
		t.Fatal("scaffolded ai pack not resolved for the starter service")
	}
	if packName != "default" {
		t.Errorf("packName = %q, want default", packName)
	}

	m, err := ai.LoadManifest(packDir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	// The hub does not exist until the first clone — that is the normal state
	// right after `dwe init`, and the manifest must validate anyway.
	absHub := filepath.Join(absRoot, svc.Dir)
	if err := ai.ValidateManifest(m, absRoot, packName, absHub, nil); err != nil {
		t.Fatalf("ValidateManifest: %v", err)
	}

	data := ai.TemplateData{
		Project:    cfg.Project,
		Service:    "app",
		Resolved:   "app",
		ServiceCfg: svc,
		Runtime:    cfg.Runtime,
		Services:   cfg.Services,
		Cfg:        cfg,
	}
	if failures := ai.DryRunRender(absRoot, packName, m, data); len(failures) != 0 {
		t.Fatalf("DryRunRender: %v", failures)
	}

	if err := os.MkdirAll(absHub, 0o755); err != nil {
		t.Fatalf("mkdir hub: %v", err)
	}
	for _, entry := range m.Render {
		if _, err := ai.RenderTemplateFile(absRoot, packName, entry.From, data, entry.To, absHub, absRoot); err != nil {
			t.Fatalf("RenderTemplateFile(%s): %v", entry.From, err)
		}
	}
	for _, link := range m.Symlinks {
		if err := ai.EnsureRelativeSymlink(link.Link, link.To, absHub, absRoot); err != nil {
			t.Fatalf("EnsureRelativeSymlink(%s): %v", link.Link, err)
		}
	}

	rendered, err := os.ReadFile(filepath.Join(absHub, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read rendered AGENTS.md: %v", err)
	}
	for _, want := range []string{"myproj", "/workspace/src"} {
		if !strings.Contains(string(rendered), want) {
			t.Errorf("rendered hub AGENTS.md does not mention %q:\n%s", want, rendered)
		}
	}
	// The hub AGENTS.md is generated; the root one is hand-edited. The file has
	// to say so, or an agent will edit the wrong one.
	if !strings.Contains(string(rendered), "dwe render ai") {
		t.Errorf("rendered hub AGENTS.md does not say it is generated:\n%s", rendered)
	}
	target, err := os.Readlink(filepath.Join(absHub, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("readlink hub CLAUDE.md: %v", err)
	}
	if target != "AGENTS.md" {
		t.Errorf("hub CLAUDE.md -> %q, want AGENTS.md", target)
	}
}
