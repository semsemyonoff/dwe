package command

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/daemon"
	"github.com/semsemyonoff/dwe/internal/shared/docker"

	"github.com/spf13/cobra"
)

// daemonSetShellOutFn is the seam used by daemonSetCompletion to invoke
// docker ps. Tests override it to inject canned NDJSON without spawning a
// process.
var daemonSetShellOutFn = runDaemonSetPS

// buildDaemonSetPSArgs returns the exact argv passed to docker for `--set`
// value completion: filter on BOTH dwe.project AND dwe.daemon.id so
// containers from another project with a colliding daemon ID never leak into
// completions. --format=json (not the Go-template form, which historically
// returned `.Labels` as a comma-separated string on real docker).
func buildDaemonSetPSArgs(projectFull, daemonID string) []string {
	args := []string{"ps"}
	args = append(args, daemon.FilterArgsByLabels(projectFull, daemonID)...)
	args = append(args, "--format=json")
	return args
}

// runDaemonSetPS shells out to docker ps with the project+daemon.id label
// filters. Used by daemonSetCompletion's production path.
func runDaemonSetPS(ctx context.Context, compose *docker.Compose, projectFull, daemonID string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, compose.BinName(), buildDaemonSetPSArgs(projectFull, daemonID)...) //nolint:gosec
	cmd.Env = compose.BuildEnv()
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return out, nil
}

// daemonSetCompletion returns a flag-completion function for --set on
// daemon-derived commands. Behaviour:
//   - if no positional command id is present yet, return empty (we need the
//     id to find the daemon base)
//   - if the command isn't derived from a daemon, return empty
//   - parse toComplete as "<key>=<partial>"; without a key, return empty
//   - shell out to docker ps filtered on BOTH project + daemon.id labels,
//     parse NDJSON, pull dwe.daemon.params label, return sorted unique
//     "<key>=<value>" completions
//
// Failures return empty + NoFileComp silently (CLAUDE.md "Completion helpers"
// rule: completion never surfaces errors to the terminal).
func daemonSetCompletion(flags *cmdctx.RootFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cmdID := args[0]

		key, _, ok := strings.Cut(toComplete, "=")
		if !ok || key == "" {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		configPath, projectRoot, err := cmdctx.CompletionConfigPath(flags, cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		reg, err := usercommands.LoadRegistryFromConfigPath(configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		def, err := reg.Get(cmdID)
		if err != nil || def == nil || def.DerivedFromDaemon == "" {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		// Apply visibility; hidden commands should not surface --set completions.
		// ApplyVisibility is fail-open so any per-expression error is logged
		// (slog.Warn) and the command is treated as visible — the completion
		// path stays usable on a typo.
		_ = reg.ApplyVisibility(cfg, projectRoot)
		if def.Hidden {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		dockerCfg, err := config.LoadDockerConfig(projectRoot, cfg)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			dockerCfg = &config.DockerConfig{}
		}

		compose := docker.NewCompose(cfg, dockerCfg)
		out, err := daemonSetShellOutFn(cmd.Context(), compose, cfg.Project.FullName(), def.DerivedFromDaemon)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		values := parseDaemonParamValuesForKey(bytes.NewReader(out), key)
		completions := make([]string, 0, len(values))
		for _, v := range values {
			completions = append(completions, key+"="+v)
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}

// parseDaemonParamValuesForKey parses NDJSON docker-ps output, decodes the
// dwe.daemon.params label per line (tolerating both the modern object
// shape and the legacy comma-separated string shape), and returns the sorted
// unique set of values seen for the requested param key.
func parseDaemonParamValuesForKey(r io.Reader, key string) []string {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	seen := map[string]bool{}
	var values []string
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec struct {
			Labels json.RawMessage `json:"Labels"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		labels := daemon.DecodeLabels(rec.Labels)
		rawParams := labels[daemon.LabelDaemonParams]
		if rawParams == "" {
			continue
		}
		var params map[string]any
		if err := json.Unmarshal([]byte(rawParams), &params); err != nil {
			continue
		}
		v, ok := params[key]
		if !ok {
			continue
		}
		s := fmt.Sprintf("%v", v)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		values = append(values, s)
	}
	sort.Strings(values)
	return values
}
