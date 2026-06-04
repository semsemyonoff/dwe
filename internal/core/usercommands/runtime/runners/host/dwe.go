package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/internal/runio"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
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

	rendered, err := tpl.RenderCommand(rc.Cmd.Cmd, rc.Render)
	if err != nil {
		return fmt.Errorf("render cmd: %w", err)
	}

	cmd := exec.CommandContext(ctx, config.ShellBin(rc.Config), "-c", shellQuote(bin)+" "+rendered) //nolint:gosec
	runio.BindCancel(cmd)
	if rc.ProjectRoot != "" {
		cmd.Dir = rc.ProjectRoot
	}

	envMap, err := runio.BuildRenderedEnv(rc.Cmd, rc)
	if err != nil {
		return err
	}
	colorEnv := runio.ParallelColorForceEnv(rc)
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
// Embedded single quotes are escaped via the '\\” idiom.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
