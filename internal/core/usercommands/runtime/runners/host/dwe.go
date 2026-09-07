package host

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/internal/runio"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
)

// DweRunner executes type=dwe commands by invoking the current dwe
// executable with the run: string as its arguments.
type DweRunner struct{}

// Run executes the dwe subcommand described by rc.Cmd.
func (r *DweRunner) Run(ctx context.Context, rc spec.RunContext) error {
	bin, err := os.Executable()
	if err != nil {
		bin = config.DweBin(rc.Config)
	}

	script, positional, err := runio.RenderShellCommand(rc.Cmd.Cmd, rc.Render)
	if err != nil {
		return err
	}

	// positional is nil unless the template has a ${args} slot; the shell then
	// binds them to "$@" without them ever entering the program text.
	shellArgs := append([]string{"-c", shellQuote(bin) + " " + script}, positional...)
	cmd := exec.CommandContext(ctx, config.ShellBin(rc.Config), shellArgs...) //nolint:gosec
	runio.BindCancel(cmd)
	if rc.ProjectRoot != "" {
		cmd.Dir = rc.ProjectRoot
	}

	envMap, err := runio.BuildRenderedEnv(rc.Cmd, rc)
	if err != nil {
		return err
	}
	colorEnv := runio.ColorForceEnv(rc, false) // host-side child: no container TTY to suppress
	if len(envMap) > 0 || len(colorEnv) > 0 {
		cmd.Env = os.Environ()
		for k, v := range envMap {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		for _, kv := range colorEnv {
			if eq := strings.IndexByte(kv, '='); eq > 0 {
				if _, exists := envMap[kv[:eq]]; !exists {
					cmd.Env = append(cmd.Env, kv)
				}
			}
		}
	}

	defer runio.WireChildIO(rc, cmd)()
	return cmd.Run()
}

// shellQuote wraps a path in single quotes for safe inclusion in a sh -c string.
// An embedded single quote is escaped the standard sh way: close the quoted
// run, emit a backslash-escaped quote, reopen. The literal cannot be spelled
// out here — gofmt rewrites a doubled quote in a doc comment into a typographic
// one.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
