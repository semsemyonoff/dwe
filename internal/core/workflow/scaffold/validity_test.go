package scaffold

import (
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	validatecfg "github.com/semsemyonoff/dwe/internal/core/validate/config"
)

// defaultValidityOptions mirrors the defaults a real `dwe init` produces: a
// named project with the standard prefix, the starter "app" service, and
// branding filled in (so the styles.yml round-trip below is meaningful).
func defaultValidityOptions(target string) Options {
	return Options{
		TargetDir: target,
		Name:      "myproj",
		Prefix:    "dwe",
		Service:   "app",
		Branding: Branding{
			Title:   "My Project",
			Tagline: "ship it",
			Accent:  "#ff8800",
		},
	}
}

// runConfigValidators runs every config-domain validator over a scaffolded
// project and returns any error-severity diagnostics. This is the load-bearing
// guard: it proves the active-minimal service.yml and the fully-commented inert
// files (deploy/lifecycle/info/docker) load and validate clean on first run.
func runConfigValidators(t *testing.T, dir string, cfg *config.DweConfig) []validate.Diagnostic {
	t.Helper()
	ctx := validate.Context{
		ProjectRoot: dir,
		ConfigPath:  filepath.Join(dir, "workspace.yml"),
		Cfg:         cfg,
	}
	var errs []validate.Diagnostic
	for _, v := range validatecfg.All() {
		for _, d := range v.Run(ctx) {
			if d.Severity == validate.SeverityError {
				errs = append(errs, d)
			}
		}
	}
	return errs
}

// TestScaffold_FreshProjectLoads is the integration guard from the plan: a
// freshly scaffolded project must load through config.LoadConfig without error.
func TestScaffold_FreshProjectLoads(t *testing.T) {
	dir := t.TempDir()
	if _, err := Scaffold(defaultValidityOptions(dir)); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	cfg, err := config.LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig on fresh scaffold: %v", err)
	}
	if cfg.Project.Name != "myproj" {
		t.Errorf("Project.Name = %q, want myproj", cfg.Project.Name)
	}
	if cfg.Project.Prefix != "dwe" {
		t.Errorf("Project.Prefix = %q, want dwe", cfg.Project.Prefix)
	}
	if _, ok := cfg.Services["app"]; !ok {
		t.Errorf("starter service \"app\" missing from loaded config; services=%v", cfg.Services)
	}
}

// TestScaffold_FreshProjectValidatesClean asserts zero error-severity
// diagnostics across the config validators — guarding the active-minimal
// service.yml and the inert commented files against breaking validation.
func TestScaffold_FreshProjectValidatesClean(t *testing.T) {
	dir := t.TempDir()
	if _, err := Scaffold(defaultValidityOptions(dir)); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	cfg, err := config.LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	for _, d := range runConfigValidators(t, dir, cfg) {
		t.Errorf("unexpected error diagnostic: domain=%s target=%s file=%s msg=%s", d.Domain, d.Target, d.File, d.Message)
	}
}

// TestScaffold_StylesRoundTrip guards against the flat-`title:` regression: the
// branding the form collects (title/tagline/accent) must survive a round-trip
// through LoadStylesConfig via the nested header.lines / header.tagline /
// colors.accent shape — a stray top-level `title:` would silently vanish.
func TestScaffold_StylesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	opts := defaultValidityOptions(dir)
	if _, err := Scaffold(opts); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	styles, err := config.LoadStylesConfig(filepath.Join(dir, "workspace", "styles.yml"))
	if err != nil {
		t.Fatalf("LoadStylesConfig: %v", err)
	}
	if len(styles.Header.Lines) != 1 || styles.Header.Lines[0] != opts.Branding.Title {
		t.Errorf("header.lines = %v, want [%q]", styles.Header.Lines, opts.Branding.Title)
	}
	if styles.Header.Tagline != opts.Branding.Tagline {
		t.Errorf("header.tagline = %q, want %q", styles.Header.Tagline, opts.Branding.Tagline)
	}
	if styles.Colors.Accent != opts.Branding.Accent {
		t.Errorf("colors.accent = %q, want %q", styles.Colors.Accent, opts.Branding.Accent)
	}
}

// TestScaffold_EmptyServiceLoadsClean covers the valid-but-empty variant: with
// no starter service the project still loads and carries no services, and the
// config validators emit no error-severity diagnostics.
func TestScaffold_EmptyServiceLoadsClean(t *testing.T) {
	dir := t.TempDir()
	opts := defaultValidityOptions(dir)
	opts.Service = ""
	if _, err := Scaffold(opts); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	cfg, err := config.LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig (no service): %v", err)
	}
	if len(cfg.Services) != 0 {
		t.Errorf("expected no services with empty Service, got %v", cfg.Services)
	}

	for _, d := range runConfigValidators(t, dir, cfg) {
		t.Errorf("unexpected error diagnostic (empty service): domain=%s target=%s file=%s msg=%s", d.Domain, d.Target, d.File, d.Message)
	}
}
