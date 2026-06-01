package service

import (
	"fmt"
	"io"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/stack"

	"github.com/spf13/cobra"
)

// serviceListJSON is the JSON DTO emitted by `devbox services --output json`.
// One entry per service (apps, tools, and infra), including required infra.
type serviceListJSON struct {
	Services []serviceListEntryJSON `json:"services"`
}

type serviceListEntryJSON struct {
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	Container string            `json:"container_name"`
	Mandatory bool              `json:"mandatory"`
	Enabled   bool              `json:"enabled"`
	Running   bool              `json:"running"`
	Status    string            `json:"status"` // "running"|"stopped"|"disabled"
	Ports     map[string]int    `json:"ports,omitempty"`
	Hosts     map[string]string `json:"hosts,omitempty"`
}

// runServicesList renders the read-only services view used when stdin is not a
// TTY and when `--output json` is requested. Text mode emits the Apps, Tools,
// and Infra sections (same style as `devbox status`); JSON mode emits a single
// `{"services":[...]}` array covering all configured services.
func runServicesList(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	projectName, _, err := stack.ResolveProjectAndDocker(flags.ConfigPath, cfg)
	if err != nil {
		return fmt.Errorf("resolving project: %w", err)
	}
	dockerBin := config.DockerBin(cfg)
	isRunning := func(_, container string) bool {
		return stack.ContainerRunning(projectName, container, dockerBin)
	}
	in := stack.StatusInput{Cfg: cfg, IsRunning: isRunning}

	if flags.Output == "json" {
		return renderServicesListJSON(cmd, flags, in)
	}
	return renderServicesListText(cmd.OutOrStdout(), cmd.ErrOrStderr(), in)
}

func renderServicesListJSON(cmd *cobra.Command, flags *cmdctx.RootFlags, in stack.StatusInput) error {
	types := []config.ServiceType{config.ServiceTypeApp, config.ServiceTypeTool, config.ServiceTypeInfra}
	entries := []serviceListEntryJSON{}
	for _, t := range types {
		filter := t
		for _, row := range stack.CollectServiceRows(in, &filter) {
			e := serviceListEntryJSON{
				Name:      row.Name,
				Type:      string(t),
				Container: row.Container,
				Mandatory: row.Mandatory,
				Enabled:   row.Enabled,
				Running:   row.Running,
				Status:    serviceListStatus(row.Mandatory, row.Enabled, row.Running),
			}
			if len(row.Ports) > 0 {
				e.Ports = row.Ports
			}
			if len(row.Hosts) > 0 {
				e.Hosts = row.Hosts
			}
			entries = append(entries, e)
		}
	}
	return cmdctx.WriteData(flags, cmd, serviceListJSON{Services: entries}, func(serviceListJSON) string { return "" })
}

func renderServicesListText(out, errW io.Writer, in stack.StatusInput) error {
	body, errs := stack.RenderApps(in)
	writeNonEmpty(out, body)
	if len(errs) > 0 {
		_, _ = fmt.Fprintf(errW, "warning: %d custom status expression(s) failed to render\n", len(errs))
	}
	body, errs = stack.RenderTools(in)
	writeNonEmpty(out, body)
	if len(errs) > 0 {
		_, _ = fmt.Fprintf(errW, "warning: %d custom status expression(s) failed to render\n", len(errs))
	}
	body, errs = stack.RenderInfra(in)
	writeNonEmpty(out, body)
	if len(errs) > 0 {
		_, _ = fmt.Fprintf(errW, "warning: %d custom status expression(s) failed to render\n", len(errs))
	}
	return nil
}

func serviceListStatus(mandatory, enabled, running bool) string {
	switch {
	case running:
		return "running"
	case !mandatory && !enabled:
		return "disabled"
	default:
		return "stopped"
	}
}

func writeNonEmpty(w io.Writer, s string) {
	if s == "" {
		return
	}
	_, _ = fmt.Fprint(w, s)
	if len(s) == 0 || s[len(s)-1] != '\n' {
		_, _ = fmt.Fprintln(w)
	}
}
