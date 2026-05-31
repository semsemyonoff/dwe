package env

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/semsemyonoff/devbox/internal/core/validate"
)

type projectPermsValidator struct{}

func (v *projectPermsValidator) ID() string     { return "project_perms" }
func (v *projectPermsValidator) Domain() string { return "env" }

func (v *projectPermsValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if ctx.ProjectRoot == "" {
		return []validate.Diagnostic{fail(
			v.ID(),
			"project root not resolved",
			"run from inside a devbox project (a directory containing devbox.yml)",
		)}
	}
	devboxDir := filepath.Join(ctx.ProjectRoot, ".devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		return []validate.Diagnostic{fail(
			v.ID(),
			fmt.Sprintf("cannot create .devbox/: %v", err),
			"check filesystem permissions on the project directory",
		)}
	}
	// Try-create a temp file in .devbox/ to confirm write access.
	f, err := os.CreateTemp(devboxDir, ".perm-probe-*")
	if err != nil {
		return []validate.Diagnostic{fail(
			v.ID(),
			fmt.Sprintf(".devbox/ not writable: %v", err),
			"check filesystem permissions on .devbox/",
		)}
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)

	// Ensure the deploy subdirectory (where the lock file lives) exists.
	// MkdirAll catches the "path is a file" case.
	deployDir := filepath.Join(devboxDir, "deploy")
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		return []validate.Diagnostic{fail(
			v.ID(),
			fmt.Sprintf(".devbox/deploy/ not creatable: %v", err),
			"remove the file at .devbox/deploy or fix directory permissions",
		)}
	}

	// lock.Acquire uses os.OpenFile(path, O_CREATE|O_RDWR, 0o644).
	// When the lock file does NOT exist, O_CREATE requires directory write permission.
	// When the lock file DOES exist, O_RDWR only requires write permission on the
	// file itself — directory write permission is not needed. Probe accordingly so
	// we don't block a valid run when the directory is read-only but the file is fine.
	lockFile := filepath.Join(deployDir, "deploy.lock")
	if fi, statErr := os.Stat(lockFile); statErr == nil {
		// Lock file exists — check regularity and direct O_RDWR access.
		if !fi.Mode().IsRegular() {
			return []validate.Diagnostic{fail(
				v.ID(),
				fmt.Sprintf(".devbox/deploy/deploy.lock is not a regular file (mode: %s)", fi.Mode()),
				"remove the path at .devbox/deploy/deploy.lock or fix filesystem state",
			)}
		}
		lf, err := os.OpenFile(lockFile, os.O_RDWR, 0)
		if err != nil {
			return []validate.Diagnostic{fail(
				v.ID(),
				fmt.Sprintf(".devbox/deploy/deploy.lock not writable: %v", err),
				"check permissions on .devbox/deploy/deploy.lock or remove it",
			)}
		}
		_ = lf.Close()
	} else {
		// Lock file does not exist — lock.Acquire will need to create it, which
		// requires directory write permission. Probe with a temp file.
		g, err := os.CreateTemp(deployDir, ".perm-probe-*")
		if err != nil {
			return []validate.Diagnostic{fail(
				v.ID(),
				fmt.Sprintf(".devbox/deploy/ not writable: %v", err),
				"check filesystem permissions on .devbox/deploy/",
			)}
		}
		gname := g.Name()
		_ = g.Close()
		_ = os.Remove(gname)
	}

	return []validate.Diagnostic{ok(v.ID())}
}
