package env

import (
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/validate"
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
	// Try-create a temp file alongside the lock path to confirm write access.
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
	return []validate.Diagnostic{ok(v.ID())}
}
