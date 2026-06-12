package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/semsemyonoff/dwe/internal/core/validate"

	"gopkg.in/yaml.v3"
)

// deprecatedCopyBuiltins is the set of pipeline builtin names superseded by the
// render-based config mechanism. Their presence in a deploy/reset pipeline is
// surfaced as a (non-fatal) deprecation warning. Kept in sync with the runtime
// notice emitted from services.ConfigsCopy.Run.
var deprecatedCopyBuiltins = map[string]bool{
	"service_configs_copy":  true,
	"service_configs_check": true,
}

// deprecationsValidator flags the legacy copy-based config materialization that
// the render subsystem replaces:
//
//   - `configs:` / `mountpoint:` in service.yml
//   - `service_configs_copy` / `service_configs_check` in deploy.yml / reset.yml
//
// Every finding is a SeverityWarning (non-fatal — the copy mechanism keeps
// working until phase 2) carrying a File:Line anchor and a migration hint.
type deprecationsValidator struct{}

func (v *deprecationsValidator) ID() string     { return "deprecations" }
func (v *deprecationsValidator) Domain() string { return "config" }

func (v *deprecationsValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	services, ok := resolveServices(ctx)
	if !ok {
		return diags
	}

	servicesDir := filepath.Join(ctx.ProjectRoot, "workspace", "services")

	for _, name := range slices.Sorted(maps.Keys(services)) {
		svcYML := filepath.Join(servicesDir, name, "service.yml")
		diags = append(diags, scanServiceYMLDeprecations(ctx.ProjectRoot, svcYML, "config.deprecation:"+name)...)

		svcDeploy := filepath.Join(servicesDir, name, "deploy.yml")
		diags = append(diags, scanPipelineDeprecations(ctx.ProjectRoot, svcDeploy, "config.deprecation.deploy:"+name)...)

		svcReset := filepath.Join(servicesDir, name, "reset.yml")
		diags = append(diags, scanPipelineDeprecations(ctx.ProjectRoot, svcReset, "config.deprecation.reset:"+name)...)
	}

	// Project-wide pipelines.
	diags = append(diags, scanPipelineDeprecations(ctx.ProjectRoot,
		filepath.Join(ctx.ProjectRoot, "workspace", "deploy.yml"), "config.deprecation.deploy")...)
	diags = append(diags, scanPipelineDeprecations(ctx.ProjectRoot,
		filepath.Join(ctx.ProjectRoot, "workspace", "reset.yml"), "config.deprecation.reset")...)

	return diags
}

// scanServiceYMLDeprecations flags the deprecated `configs:` block (and any
// nested `mountpoint:`) in a service.yml. Missing or unparseable files are
// silent — other validators surface those.
func scanServiceYMLDeprecations(projectRoot, path, target string) []validate.Diagnostic {
	doc, ok := parseYAMLDoc(path)
	if !ok {
		return nil
	}
	file := relPath(projectRoot, path)

	var diags []validate.Diagnostic
	walkMappingPairs(doc, func(key, _ *yaml.Node) {
		switch key.Value {
		case "configs":
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Domain:   "config",
				Target:   target,
				File:     file,
				Line:     key.Line,
				Message:  "configs: is deprecated; the copy-based config mechanism is superseded by render-based configs",
				Hint:     "migrate to render.config + generated: (see docs/reference/render/config.md); configs: keeps working until the phase-2 removal",
			})
		case "mountpoint":
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Domain:   "config",
				Target:   target,
				File:     file,
				Line:     key.Line,
				Message:  "mountpoint: is deprecated; render writes directly into the already-mounted src/ tree, so the nested bind-mount is no longer needed",
				Hint:     "drop mountpoint: and render the file under src/ via render.config (see docs/reference/render/config.md)",
			})
		}
	})
	return diags
}

// scanPipelineDeprecations flags steps whose cmd: names a deprecated copy
// builtin in a deploy.yml / reset.yml pipeline.
func scanPipelineDeprecations(projectRoot, path, target string) []validate.Diagnostic {
	doc, ok := parseYAMLDoc(path)
	if !ok {
		return nil
	}
	file := relPath(projectRoot, path)

	var diags []validate.Diagnostic
	walkMappingPairs(doc, func(key, val *yaml.Node) {
		if key.Value != "cmd" || val.Kind != yaml.ScalarNode {
			return
		}
		if !deprecatedCopyBuiltins[val.Value] {
			return
		}
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityWarning,
			Domain:   "config",
			Target:   target,
			File:     file,
			Line:     val.Line,
			Message:  fmt.Sprintf("%s is a deprecated copy builtin; superseded by service_configs_render / service_configs_render_check / service_generated_harvest", val.Value),
			Hint:     "migrate the step to service_configs_render (see docs/reference/config/deploy/builtins.md); the copy builtins keep working until the phase-2 removal",
		})
	})
	return diags
}

// parseYAMLDoc reads and parses a YAML file into a document node. It returns
// ok=false for a missing or unparseable file (callers stay silent — dedicated
// loaders surface real load errors).
func parseYAMLDoc(path string) (*yaml.Node, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, false
	}
	if doc.Kind == 0 {
		return nil, false
	}
	return &doc, true
}

// walkMappingPairs walks a YAML node tree, invoking fn for every key/value pair
// of every mapping it encounters (recursing into nested mappings and sequences).
func walkMappingPairs(node *yaml.Node, fn func(key, val *yaml.Node)) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.DocumentNode:
		for _, c := range node.Content {
			walkMappingPairs(c, fn)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			fn(node.Content[i], node.Content[i+1])
			walkMappingPairs(node.Content[i+1], fn)
		}
	case yaml.SequenceNode:
		for _, c := range node.Content {
			walkMappingPairs(c, fn)
		}
	}
}
