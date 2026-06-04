package render

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packcommon"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// validateExplicitRenderArg validates the explicit service argument for a
// `dwe render <kind> <service>` command. Checks in priority order:
// not-found → disabled → no-dir → render policy. kind is the render-config key
// (ai/ide/git); noDirArtifact and participates supply the kind-specific message
// fragments; enabledExplicit reads the per-kind render.<kind>.enabled flag.
// Returns nil when the service is valid and renderable.
func validateExplicitRenderArg(
	name string,
	services map[string]config.ServiceConfig,
	kind, noDirArtifact, participates string,
	enabledExplicit func(config.ServiceConfig) (bool, bool),
) error {
	svc, ok := services[name]
	if !ok {
		return fmt.Errorf("service %q not found in config", name)
	}
	if !svc.Enabled {
		return fmt.Errorf("service %q is disabled at the project level", name)
	}
	if strings.TrimSpace(svc.Dir) == "" || filepath.Clean(svc.Dir) == "." {
		return fmt.Errorf("service %q has no dir; cannot render %s", name, noDirArtifact)
	}
	enabled, explicit := enabledExplicit(svc)
	if !enabled {
		if explicit {
			return fmt.Errorf("service %q has render.%s.enabled: false", name, kind)
		}
		return fmt.Errorf("service %q (type: %s) does not participate in %s by default; set render.%s.enabled: true to opt in", name, svc.Type, participates, kind)
	}
	return nil
}

// warnSelectionSkips emits warnings for the actionable skip reasons (empty-dir,
// lost-collision); policy-based skips fall through silently. kind is the render
// prefix (ai/ide/git). fields extracts (reason, name, dir, winner) from each
// kind's SkippedService value (the per-kind types are structurally identical).
func warnSelectionSkips[T any](w *render.Writer, kind string, skipped []T, fields func(T) (reason, name, dir, winner string)) {
	for _, s := range skipped {
		reason, name, dir, winner := fields(s)
		switch reason {
		case "empty-dir":
			w.Warning(fmt.Sprintf("%s [%s] — skipped (service has no dir or dir is project root)", kind, name))
		case "lost-collision":
			w.Warning(fmt.Sprintf("%s [%s] — skipped (dir %s rendered by %s)", kind, name, dir, winner))
		}
	}
}

// warnNoPack emits the "no template pack found" skip warning for a service,
// listing the implicit-chain candidates that were tried (or "default" when
// none). kind is the render prefix (ai/ide/git).
func warnNoPack(w *render.Writer, kind string, services map[string]config.ServiceConfig, name string) {
	tried := strings.Join(packcommon.ImplicitPackCandidates(services, name), ", ")
	if tried == "" {
		tried = "default"
	}
	w.Warning(fmt.Sprintf("%s [%s] — skipped (no template pack found; tried %s)", kind, name, tried))
}
