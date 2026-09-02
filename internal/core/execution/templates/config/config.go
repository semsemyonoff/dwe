// Package config provides service config-file template pack resolution and
// rendering. Unlike the ide/ai/git renderers (which use the raw text/template
// `{{ }}` substrate via packcommon.TemplateData), config templates use the
// `${...}` shorthand resolved through tpl.CompileVarSyntax over a per-service
// tpl.RenderContext. This gives config authors the same ${APP_*}/${DB_*}
// ergonomics they already expect, plus the ${generated.<name>} namespace that
// replays harvested service-minted secrets from the generated-value store.
//
// Config packs are manifest-driven (same schema as ide/ai/git): each pack has a
// manifest.yml declaring `render:` entries (from→to). Authors target the app
// tree by writing `to: src/...` — `src/` is a usage convention, not a hardcoded
// join: `to` is interpreted relative to the service hub dir (svc.Dir).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/execution/templates/manifest"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packcommon"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packroot"
	projectconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/generatedstore"
	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// kind is the template-pack kind for config rendering. Packs live under
// workspace/templates/config/<name>/ (with the .local override sibling).
const kind = "config"

// RenderEntry describes a template file to render. Alias to the shared schema.
type RenderEntry = manifest.RenderEntry

// Manifest defines the config template pack manifest. Alias to the shared schema.
type Manifest = manifest.File

// ImplicitPackCandidates returns the implicit-chain pack name candidates for a
// service. See packcommon.ImplicitPackCandidates.
var ImplicitPackCandidates = packcommon.ImplicitPackCandidates

// RenderedFile records a single file produced by a config render pass.
type RenderedFile struct {
	// To is the manifest destination, relative to the service hub dir.
	To string
	// Rel is the project-root-relative path (svc.Dir/To) for display.
	Rel string
	// FromOverride is true when the sibling <pack>.local/ override supplied
	// the template source.
	FromOverride bool
}

// Result describes the outcome of rendering one service's config pack.
type Result struct {
	// Service is the service name.
	Service string
	// Pack is the resolved pack name (empty when no pack was found).
	Pack string
	// Found reports whether a config pack resolved for the service.
	Found bool
	// Rendered lists the files written (empty when Found is false).
	Rendered []RenderedFile
}

// ResolveTemplatePack resolves a config template pack directory for a service.
// Returns (packDir, packName, found, err). An explicit svc.Render.Config.Template
// pin is strict (any failure, including not-found, is a hard error). The
// implicit chain is service-name → ancestors via Extends → default; it returns
// found=false when exhausted. Invalid pack names in the implicit chain are
// skipped silently. Semantics: err != nil means hard failure; err == nil &&
// !found means the implicit chain is exhausted.
func ResolveTemplatePack(svc projectconfig.ServiceConfig, services map[string]projectconfig.ServiceConfig, projectRoot, serviceName string) (string, string, bool, error) {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", "", false, fmt.Errorf("resolve project root: %w", err)
	}

	if svc.Render.Config != nil && svc.Render.Config.Template != "" {
		pin := svc.Render.Config.Template
		if err := manifest.ValidatePackName(pin); err != nil {
			return "", "", false, fmt.Errorf("invalid render.config.template %q: %w", pin, err)
		}
		candidate := filepath.Join(absRoot, "workspace", "templates", kind, pin)
		found, err := statPackDir(candidate, absRoot, pin)
		if err != nil {
			return "", "", false, err
		}
		if found {
			return candidate, pin, true, nil
		}
		return "", "", false, fmt.Errorf("config template pack %q not found (required by explicit render.config.template setting)", pin)
	}

	for _, name := range ImplicitPackCandidates(services, serviceName) {
		candidate := filepath.Join(absRoot, "workspace", "templates", kind, name)
		found, err := statPackDir(candidate, absRoot, name)
		if err != nil {
			return "", "", false, err
		}
		if found {
			return candidate, name, true, nil
		}
	}

	return "", "", false, nil
}

// statPackDir reports whether candidate is a usable (non-symlink, contained)
// pack directory. Returns (false, nil) when it does not exist; (false, err) for
// any malformed condition (symlink, non-directory, escaping path).
func statPackDir(candidate, absRoot, name string) (bool, error) {
	fi, err := os.Lstat(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat config template pack %q: %w", name, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("config template pack %q is a symlink; symlinked packs are not supported", name)
	}
	if !fi.IsDir() {
		return false, fmt.Errorf("config template pack %q is not a directory", name)
	}
	if err := pathsafe.CheckNoSymlinks(absRoot, candidate, "config template pack"); err != nil {
		return false, err
	}
	return true, nil
}

// LoadManifest loads and parses manifest.yml from the pack directory. Strict
// decode: unknown fields are an error; a missing manifest wraps
// manifest.ErrManifestMissing.
func LoadManifest(packDir string) (*Manifest, error) {
	return manifest.Load(filepath.Join(packDir, "manifest.yml"))
}

// ValidateManifest validates the manifest against shape rules (pure) and then
// verifies each render source resolves via packroot.Resolve (so a `from`
// satisfied only by the sibling <pack>.local/ override is treated as valid).
// destRoot is the service hub directory. Config packs declare only render
// entries; symlink entries are rejected because rendered config files are not
// symlinked into place.
func ValidateManifest(m *Manifest, projectRoot, packName, destRoot string) error {
	label := "config pack " + packName
	if err := manifest.ValidateShape(m, destRoot, label); err != nil {
		return err
	}
	if len(m.Symlinks) > 0 {
		return fmt.Errorf("%s: symlinks are not supported for config packs", label)
	}
	resolve := func(rel string) (string, bool, error) {
		return packroot.Resolve(projectRoot, kind, packName, rel)
	}
	return manifest.ValidateSourcesWith(m, resolve, nil, label)
}

// RenderConfigs resolves the config pack for serviceName, renders every manifest
// entry through the ${...} substrate against a per-service tpl.RenderContext
// (cfg.Raw plus the service's harvested generated values from store), and writes
// each result under the service hub dir (svc.Dir), mode replace (overwrite).
//
// When no config pack resolves for the service, RenderConfigs returns a Result
// with Found=false and no error — config rendering is opt-in. A resolved pack
// for a service with no hub dir is a configuration error.
func RenderConfigs(projectRoot string, cfg *projectconfig.DweConfig, serviceName string, store *generatedstore.Store) (Result, error) {
	if cfg == nil {
		return Result{}, errors.New("config render: nil cfg")
	}
	svc, ok := cfg.Services[serviceName]
	if !ok {
		return Result{}, fmt.Errorf("config render: unknown service %q", serviceName)
	}

	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve project root: %w", err)
	}

	packDir, packName, found, err := ResolveTemplatePack(svc, cfg.Services, absRoot, serviceName)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{Service: serviceName, Found: false}, nil
	}

	if filepath.Clean(svc.Dir) == "." || svc.Dir == "" {
		return Result{}, fmt.Errorf("config render: service %q has no dir to render into", serviceName)
	}
	absHubDir := filepath.Join(absRoot, svc.Dir)
	if _, err := pathsafe.ContainedRel(absRoot, absHubDir); err != nil {
		return Result{}, fmt.Errorf("config render: service dir %q escapes project root: %w", svc.Dir, err)
	}
	if err := pathsafe.CheckNoSymlinks(absRoot, absHubDir, "service dir"); err != nil {
		return Result{}, err
	}

	m, err := LoadManifest(packDir)
	if err != nil {
		return Result{}, err
	}
	if err := ValidateManifest(m, absRoot, packName, absHubDir); err != nil {
		return Result{}, err
	}

	ctx := &tpl.RenderContext{
		Raw:       cfg.Raw,
		Generated: store.Service(serviceName),
		Host:      tpl.CurrentHostInfo(),
	}

	ident := loadPackIdentity(m, cfg)

	res := Result{Service: serviceName, Pack: packName, Found: true}
	for _, entry := range m.Render {
		fromOverride, err := renderTemplateFile(absRoot, packName, entry.From, ctx, entry.To, absHubDir, ident)
		if err != nil {
			return Result{}, err
		}
		res.Rendered = append(res.Rendered, RenderedFile{
			To:           entry.To,
			Rel:          filepath.Join(svc.Dir, entry.To),
			FromOverride: fromOverride,
		})
	}
	return res, nil
}

// packIdentity carries the age identity a pack needs for its .age sources,
// loaded once per RenderConfigs call. The load failure travels with it instead
// of aborting the pass early so the error can name the source file that
// actually needs the identity.
type packIdentity struct {
	recipient string
	id        secrets.Identity
	err       error
}

// isEncryptedSource reports whether a manifest `from:` is a native age file.
func isEncryptedSource(from string) bool { return strings.HasSuffix(from, ".age") }

// loadPackIdentity loads the project identity when — and only when — the
// manifest declares at least one .age source, so a pack without encrypted
// sources never touches ~/.config.
func loadPackIdentity(m *Manifest, cfg *projectconfig.DweConfig) packIdentity {
	encrypted := false
	for _, entry := range m.Render {
		if isEncryptedSource(entry.From) {
			encrypted = true
			break
		}
	}
	if !encrypted {
		return packIdentity{}
	}
	ident := packIdentity{recipient: projectconfig.SecretsRecipient(cfg)}
	if ident.recipient == "" {
		ident.err = errors.New("no secrets.recipient is configured")
		return ident
	}
	ident.id, _, ident.err = secrets.LoadIdentity(ident.recipient)
	return ident
}

// renderTemplateFile resolves rel via packroot (override first, canonical
// fallback), renders it with the ${...} substrate against ctx, and writes the
// result to dest under absHubDir (mode replace). It enforces that dest stays
// inside absHubDir and that absHubDir stays inside absRoot via the same
// pathsafe discipline used by the ide/ai renderers.
//
// A source whose name ends in .age is a native age file: it is decrypted with
// ident before the ${...} render, and its output is written 0600 (explicitly
// chmoded, so a pre-existing 0644 target is tightened). `to:` is never
// auto-stripped — authors write `from: creds.json.age`, `to: src/creds.json`.
func renderTemplateFile(absRoot, packName, rel string, ctx *tpl.RenderContext, dest, absHubDir string, ident packIdentity) (bool, error) {
	sourcePath, fromOverride, err := packroot.Resolve(absRoot, kind, packName, rel)
	if err != nil {
		return false, fmt.Errorf("resolve template %s: %w", rel, err)
	}

	tplBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		return false, fmt.Errorf("read template %s: %w", sourcePath, err)
	}

	encryptedSource := isEncryptedSource(rel)
	if encryptedSource {
		tplBytes, err = decryptSource(rel, tplBytes, ident)
		if err != nil {
			return false, err
		}
	}

	out, err := tpl.RenderCommand(string(tplBytes), ctx)
	if err != nil {
		return false, fmt.Errorf("render template %s: %w", rel, err)
	}
	// A ${...} substitution of a value that is still an undecrypted marker
	// would write ciphertext into the hub dir, where the container would read
	// it as the credential. Refuse instead of materializing it.
	if secrets.ContainsMarker(out) {
		return false, fmt.Errorf("render template %s: %s would contain an undecrypted secret — see 'dwe secrets status'", rel, dest)
	}

	absDest, err := filepath.Abs(filepath.Join(absHubDir, dest))
	if err != nil {
		return false, fmt.Errorf("resolve destination: %w", err)
	}
	if _, err := pathsafe.ContainedRel(absHubDir, absDest); err != nil {
		return false, fmt.Errorf("dest %q escapes service dir: %w", dest, err)
	}

	destDir := filepath.Dir(absDest)
	if err := pathsafe.CheckNoSymlinks(absRoot, destDir, "destination dir"); err != nil {
		return false, err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return false, fmt.Errorf("create dir for %s: %w", dest, err)
	}

	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return false, fmt.Errorf("resolve project root: %w", err)
	}
	realHubDir, err := filepath.EvalSymlinks(absHubDir)
	if err != nil {
		return false, fmt.Errorf("resolve service dir: %w", err)
	}
	realDir, err := filepath.EvalSymlinks(destDir)
	if err != nil {
		return false, fmt.Errorf("resolve dir for %s: %w", dest, err)
	}
	if err := pathsafe.EnsureRealUnder(realDir, realRoot, realHubDir); err != nil {
		return false, fmt.Errorf("destination dir for %q resolves outside required boundaries via symlink: %w", dest, err)
	}

	if fi, err := os.Lstat(absDest); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("destination %q is a symlink; will not overwrite", dest)
	}

	mode := os.FileMode(0o644)
	if encryptedSource {
		mode = 0o600
	}
	if err := os.WriteFile(absDest, []byte(out), mode); err != nil {
		return false, fmt.Errorf("write %s: %w", dest, err)
	}
	if encryptedSource {
		// os.WriteFile keeps the mode of a pre-existing file; an output whose
		// source was encrypted must not stay world-readable.
		if err := os.Chmod(absDest, mode); err != nil {
			return false, fmt.Errorf("chmod %s: %w", dest, err)
		}
	}
	return fromOverride, nil
}

// decryptSource opens a native age pack source. The error names the source
// path and the fix, because the identity is loaded once per pack but only some
// sources need it.
func decryptSource(rel string, ciphertext []byte, ident packIdentity) ([]byte, error) {
	if ident.err != nil {
		if ident.recipient == "" {
			return nil, fmt.Errorf("render template %s: encrypted source needs a project identity, but %v — add a secrets: block to workspace.yml (see 'dwe secrets init')", rel, ident.err)
		}
		return nil, fmt.Errorf("render template %s: encrypted source needs the project identity for %s (%v) — see 'dwe secrets key import'", rel, ident.recipient, ident.err)
	}
	plain, err := secrets.DecryptBytes(ciphertext, ident.id)
	if err != nil {
		return nil, fmt.Errorf("render template %s: decrypt encrypted source: %w", rel, err)
	}
	return plain, nil
}
