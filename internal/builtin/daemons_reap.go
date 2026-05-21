package builtin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/daemon"
	"devbox-cli/internal/docker"
)

// daemonsReapBuiltin implements daemons_reap.
//
// Enumerates running daemon containers for the current project via
// `docker ps --format=json` filtered on the standard devbox.project and
// devbox.daemon.id labels, then issues `docker stop -t <default>` against
// each. Used by the auto-injected `_auto_reap_daemons` phase in lifecycle
// stop; can also be invoked directly from user pipelines.
//
// Accepts no with: parameters in v1.
type daemonsReapBuiltin struct{}

func (daemonsReapBuiltin) Validate(with map[string]any) error {
	for key := range with {
		return fmt.Errorf("daemons_reap: unknown key %q", key)
	}
	return nil
}

func (daemonsReapBuiltin) Describe(_ map[string]any) string {
	return "reap running daemons for this project"
}

func (daemonsReapBuiltin) Run(ctx context.Context, _ map[string]any, ectx ExecContext) error {
	if ectx.Config == nil {
		return fmt.Errorf("daemons_reap: config not available")
	}
	dockerCfg := ectx.DockerConfig
	if dockerCfg == nil {
		dockerCfg = &config.DockerConfig{}
	}

	projectFull := ectx.Config.Project.FullName()
	compose := docker.NewCompose(ectx.Config, dockerCfg)

	names, err := listDaemonsFn(ctx, compose, projectFull)
	if err != nil {
		// Reap is best-effort: if docker is unreachable (daemon down,
		// permission denied, remote-context unavailable) we warn and exit
		// successfully rather than failing the entire stop pipeline.
		fmt.Fprintf(ectx.Output.Writer(), "warning: daemons_reap: %v\n", err)
		return nil
	}
	if len(names) == 0 {
		fmt.Fprintln(ectx.Output.Writer(), "no daemons running")
		return nil
	}

	secs := max(int(defaultStopTimeout.Round(secondUnit).Seconds()), 1)

	var stopped []string
	for _, name := range names {
		args := []string{"stop", "-t", strconv.Itoa(secs), name}
		cmd := exec.CommandContext(ctx, compose.BinName(), args...) //nolint:gosec
		cmd.Env = compose.BuildEnv()
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			errOut := strings.TrimSpace(stderr.String())
			if strings.Contains(errOut, "No such container") {
				continue
			}
			if errOut != "" {
				fmt.Fprintf(ectx.Output.Writer(), "warning: docker stop %s: %s\n", name, errOut)
				continue
			}
			fmt.Fprintf(ectx.Output.Writer(), "warning: docker stop %s: %v\n", name, err)
			continue
		}
		stopped = append(stopped, name)
	}

	if len(stopped) == 0 {
		fmt.Fprintln(ectx.Output.Writer(), "no daemons running")
		return nil
	}
	fmt.Fprintf(ectx.Output.Writer(), "✓ reaped %d daemon(s): %s\n", len(stopped), strings.Join(stopped, ", "))
	return nil
}

// listDaemonsFn is a test seam: production calls docker, tests inject a stub.
var listDaemonsFn = listDaemons

// listDaemons runs `docker ps --format=json` filtered on the project's
// devbox.project and devbox.daemon.id labels, then parses the NDJSON
// stdout into a sorted, deduplicated slice of container names.
func listDaemons(ctx context.Context, compose *docker.Compose, projectFull string) ([]string, error) {
	args := []string{"ps", "--format=json"}
	args = append(args, daemon.FilterArgsByLabels(projectFull, "")...)
	cmd := exec.CommandContext(ctx, compose.BinName(), args...) //nolint:gosec
	cmd.Env = compose.BuildEnv()
	out, err := cmd.Output()
	if err != nil {
		// If the docker binary is not on PATH there are by definition no
		// devbox-managed daemons running on this host. Reap exits cleanly
		// rather than failing the entire lifecycle stop pipeline.
		if errors.Is(err, exec.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return parseDaemonsPSOutput(bytes.NewReader(out))
}

// parseDaemonsPSOutput parses `docker ps --format=json` NDJSON. Each line is
// an independent JSON object with at minimum a "Names" field; we extract it
// and tolerate the older comma-separated multi-name shape by taking the
// first entry. Lines that fail to parse are skipped (best-effort).
func parseDaemonsPSOutput(r io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	seen := make(map[string]bool)
	var names []string
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var obj struct {
			Names string `json:"Names"`
			Name  string `json:"Name"`
		}
		if err := json.Unmarshal(line, &obj); err != nil {
			continue
		}
		n := obj.Names
		if n == "" {
			n = obj.Name
		}
		if n == "" {
			continue
		}
		// docker ps may return comma-separated names for containers with
		// multiple aliases; the first one is the canonical name.
		if i := strings.IndexByte(n, ','); i >= 0 {
			n = n[:i]
		}
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}
