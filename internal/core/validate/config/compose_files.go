package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// composeFilesValidator warns when a file listed under a service's compose: or
// compose_after: does not exist. Without it a typo only surfaces as a docker
// compose error on the next `dwe run`, far from the service.yml that caused it.
//
// Every service is checked regardless of its enabled state: the path is
// git-tracked config, `--all` compose operations (ComposeFilesAll) load it
// today, and enabling the service later would break the stack over a typo
// `dwe validate` could have named up front.
//
// Warning, not error: a compose file may legitimately live inside a source tree
// that deploy clones (or a file a deploy step renders), so a fresh checkout can
// be missing it until the first `dwe deploy run`. A warning keeps that case
// from failing `dwe validate` outright while `--strict` still gates on it.
type composeFilesValidator struct{}

var _ validate.Validator = (*composeFilesValidator)(nil)

func (v *composeFilesValidator) ID() string     { return "compose_files" }
func (v *composeFilesValidator) Domain() string { return "config" }

func (v *composeFilesValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	// compose.base heads every -f chain, so a missing one breaks the stack
	// whatever is enabled.
	if ctx.Cfg != nil && ctx.Cfg.Compose.Base != "" {
		base := ctx.Cfg.Compose.Base
		if problem, _ := composePathProblem(ctx.ProjectRoot, base); problem != "" {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Domain:   "config",
				Target:   "config.compose_files",
				File:     "workspace.yml",
				Message:  fmt.Sprintf("compose.base file %q %s", base, problem),
				Hint:     "docker compose will fail on `dwe run`; the path resolves against the project root",
			})
		}
	}

	services, ok := resolveServices(ctx)
	if !ok {
		return diags
	}

	type ref struct{ field, path string }
	// A child inheriting its parent's list through extends: carries the same
	// path; report it once, naming every service that lists it.
	owners := make(map[ref][]string)
	var order []ref
	add := func(field, svc string, paths []string) {
		for _, p := range paths {
			r := ref{field, p}
			if _, seen := owners[r]; !seen {
				order = append(order, r)
			}
			owners[r] = append(owners[r], svc)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(services)) {
		svc := services[name]
		add("compose", name, svc.Compose)
		add("compose_after", name, svc.ComposeAfter)
	}

	for _, r := range order {
		svcs := owners[r]
		problem, missing := composePathProblem(ctx.ProjectRoot, r.path)
		if problem == "" {
			continue
		}
		var hint string
		if missing {
			hint = "docker compose will fail on `dwe run` while this service is enabled; paths resolve against the project root, not the service folder"
			if alt := filepath.Join(ctx.ProjectRoot, "workspace", "services", svcs[0], r.path); !filepath.IsAbs(r.path) && fileExists(alt) {
				hint = fmt.Sprintf("paths resolve against the project root, not the service folder — did you mean %q?", relPath(ctx.ProjectRoot, alt))
			}
		}

		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityWarning,
			Domain:   "config",
			Target:   "config.compose_files",
			File:     relPath(ctx.ProjectRoot, filepath.Join(ctx.ProjectRoot, "workspace", "services", svcs[0], "service.yml")),
			Message: fmt.Sprintf("%s %s file %q, which %s",
				ownersLabel(svcs), r.field, r.path, problem),
			Hint: hint,
		})
	}
	return diags
}

// composePathProblem stats a compose path resolved like parseComposeFiles and
// docker compose itself (dwe runs compose with cmd.Dir = project root and no
// --project-directory). It returns "" when the path is a readable file;
// missing reports the not-exist case.
func composePathProblem(root, p string) (problem string, missing bool) {
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, p)
	}
	info, err := os.Stat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "does not exist", true
	case err != nil:
		return "cannot be read: " + err.Error(), false
	case info.IsDir():
		return "is a directory, not a compose file", false
	default:
		return "", false
	}
}

// ownersLabel renders "service a lists" / "services a, b list".
func ownersLabel(svcs []string) string {
	if len(svcs) == 1 {
		return "service " + svcs[0] + " lists"
	}
	return "services " + strings.Join(svcs, ", ") + " list"
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
