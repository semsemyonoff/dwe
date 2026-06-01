package env

import (
	"fmt"
	"os/exec"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

type gitBinValidator struct {
	cfg *config.DweConfig
}

func (v *gitBinValidator) ID() string     { return "git_bin" }
func (v *gitBinValidator) Domain() string { return "env" }

func (v *gitBinValidator) Run(_ validate.Context) []validate.Diagnostic {
	bin := config.GitBin(v.cfg)
	if _, err := exec.LookPath(bin); err != nil {
		return []validate.Diagnostic{fail(
			v.ID(),
			fmt.Sprintf("git binary not found in PATH: %s", bin),
			"install git or set binaries.git in workspace.yml",
		)}
	}
	return []validate.Diagnostic{ok(v.ID())}
}

type shellBinValidator struct {
	cfg *config.DweConfig
}

func (v *shellBinValidator) ID() string     { return "shell_bin" }
func (v *shellBinValidator) Domain() string { return "env" }

func (v *shellBinValidator) Run(_ validate.Context) []validate.Diagnostic {
	bin := config.ShellBin(v.cfg)
	if _, err := exec.LookPath(bin); err != nil {
		return []validate.Diagnostic{fail(
			v.ID(),
			fmt.Sprintf("shell binary not found in PATH: %s", bin),
			"install a POSIX shell or set binaries.shell in workspace.yml",
		)}
	}
	return []validate.Diagnostic{ok(v.ID())}
}
