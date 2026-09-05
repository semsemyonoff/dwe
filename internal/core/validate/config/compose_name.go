package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// composeProjectNameValidator warns when a compose file declares a top-level
// `name:` that differs from the project name dwe passes via `docker compose -p`.
//
// dwe always invokes compose with `-p <resolved>` (the resolved docker.yml
// project_name, else the canonical "<prefix>-<name>" from project.name). That
// `-p` silently overrides any top-level `name:` in the compose files, so a
// divergent `name:` is dead config: the container/network/volume labels and the
// effective project name are NOT what the file appears to declare. This is
// confusing when reading the compose file in isolation, and it strands whatever
// raw `docker compose` created under the declared name — since `.env` now
// carries `COMPOSE_PROJECT_NAME` (which outranks a compose-file `name:`), even
// a raw invocation without dwe's `-p` follows the resolved name.
type composeProjectNameValidator struct{}

var _ validate.Validator = (*composeProjectNameValidator)(nil)

func (v *composeProjectNameValidator) ID() string     { return "compose_project_name" }
func (v *composeProjectNameValidator) Domain() string { return "config" }

func (v *composeProjectNameValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if ctx.Cfg == nil {
		return nil
	}

	// The name dwe stamps via `-p`. Empty means dwe omits `-p` entirely (no
	// project.name and no docker.yml project_name), so compose honours the
	// file's own `name:` — there is nothing being overridden, nothing to warn.
	resolved, err := config.ResolveComposeProjectName(ctx.ProjectRoot, ctx.Cfg)
	if err != nil || resolved == "" {
		// Resolution errors (e.g. a templated project_name typo) are surfaced by
		// the docker validator; stay silent here to avoid double-reporting.
		return nil
	}

	// Determine the EFFECTIVE compose name the way compose itself would, absent
	// dwe's `-p`: scan the active `-f` chain in order and keep the LAST file that
	// declares a non-empty top-level `name:` (later `-f` overrides earlier). Only
	// that effective name is what `-p` overrides; warning per-file would falsely
	// flag a base `name:` that a later overlay already corrects. ComposeFiles()
	// (not ...All()) is the real chain — disabled overlays are not loaded, so
	// their `name:` never takes effect.
	var declared, declaredFile string
	interpolated := false
	for _, rel := range ctx.Cfg.ComposeFiles() {
		abs := rel
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(ctx.ProjectRoot, rel)
		}
		name, ok := readComposeTopLevelName(abs)
		if !ok || name == "" {
			continue
		}
		declared, declaredFile = name, abs
		// `name: ${FOO}` — this entry wins the override but we cannot resolve it,
		// so we cannot prove divergence. Remember that the effective name is
		// unknown; a later concrete `name:` would clear this again.
		interpolated = strings.Contains(name, "$")
	}

	if declared == "" || interpolated || declared == resolved {
		return nil
	}

	return []validate.Diagnostic{{
		Severity: validate.SeverityWarning,
		Domain:   "config",
		Target:   "config.compose_project_name",
		File:     relPath(ctx.ProjectRoot, declaredFile),
		Message: fmt.Sprintf(
			"compose declares name: %q but dwe runs compose with -p %q — the top-level name: is silently overridden",
			declared, resolved,
		),
		Hint: composeNameHint(declared, resolved),
	}}
}

// composeNameHint builds the divergence hint. Both routes it offers must
// actually clear the warning — recommending `project_name: <resolved>` would
// not, since resolved IS the name dwe already passes to -p, so writing it into
// docker.yml changes nothing and the divergence survives verbatim.
//
// Which route can lead depends on the declared name's casing. Compose project
// names must be lowercase (see config.normalizeComposeProjectName), and dwe
// lowercases whatever docker.yml declares, so adopting the compose-declared
// identity via `project_name:` only converges when the declared name is
// already lowercase. When it is not, the compose file has to change either
// way, so that route leads instead.
func composeNameHint(declared, resolved string) string {
	if lower := strings.ToLower(declared); lower == declared {
		return fmt.Sprintf(
			"align the two: set workspace/docker.yml `project_name: %s` to make dwe adopt the compose-declared name (recommended — leaves the compose file untouched), "+
				"or change the compose top-level to `name: %s` to match the name dwe already uses. "+
				"Until then the declared %q is dead config: .env's COMPOSE_PROJECT_NAME outranks it, so even raw `docker compose` scopes this stack as %q.",
			declared, resolved, declared, resolved,
		)
	}
	return fmt.Sprintf(
		"align the two: change the compose top-level to `name: %s` to match the name dwe already uses (recommended), "+
			"or lowercase it to `name: %s` and set workspace/docker.yml `project_name: %s` to keep that identity — "+
			"compose project names must be lowercase, so %q cannot be pinned as-is. "+
			"Until then the declared name is dead config: .env's COMPOSE_PROJECT_NAME outranks it, so even raw `docker compose` scopes this stack as %q.",
		resolved, strings.ToLower(declared), strings.ToLower(declared), declared, resolved,
	)
}

// readComposeTopLevelName reads the top-level `name:` from a compose file.
// ok is false when the file is missing/unreadable or not valid YAML — those are
// surfaced by compose itself (or are simply absent overlays) and are not this
// validator's concern. A present file with no top-level name yields ("", true).
func readComposeTopLevelName(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var doc struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", false
	}
	return strings.TrimSpace(doc.Name), true
}
