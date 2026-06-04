// Package packcommon holds infrastructure shared by the ai/ide/git template
// packs: the extends-chain walkers, the render TemplateData context (and its
// service-type accessors), and the in-memory dry-run renderer. The per-kind
// resolvers (collision policy, manifest validation) deliberately stay in their
// own packages — only the byte-identical scaffolding lives here.
package packcommon

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/semsemyonoff/dwe/internal/core/execution/templates/manifest"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packroot"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// maxDepth bounds every extends-chain walk (defense-in-depth cycle guard).
const maxDepth = 32

// ImplicitPackCandidates returns the implicit-chain pack name candidates for a
// service: the service name, then each ancestor walked via Extends (in order),
// then "default". Duplicates and names that fail manifest.ValidatePackName are
// skipped silently. The 32-hop cycle guard mirrors ExtendsDepth.
func ImplicitPackCandidates(services map[string]config.ServiceConfig, serviceName string) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		if manifest.ValidatePackName(name) != nil {
			return
		}
		out = append(out, name)
		seen[name] = true
	}

	add(serviceName)
	current := serviceName
	for range maxDepth {
		svc, ok := services[current]
		if !ok || svc.Extends == "" {
			break
		}
		current = svc.Extends
		add(current)
	}
	add("default")
	return out
}

// ExtendsDepth computes the depth of a service's extends chain.
// Returns (depth, capped): depth is the number of hops to the root;
// capped is true if depth hit the 32-hop limit (defense-in-depth cycle guard).
func ExtendsDepth(services map[string]config.ServiceConfig, name string) (int, bool) {
	depth := 0
	current := name
	for {
		if depth >= maxDepth {
			return maxDepth, true
		}
		svc, ok := services[current]
		if !ok || svc.Extends == "" {
			return depth, false
		}
		current = svc.Extends
		depth++
	}
}

// ExtendsRoot walks the extends chain from name and returns the chain root
// (first ancestor with empty Extends). Returns name itself when the service
// has no extends or is unknown. The 32-hop cycle guard mirrors ExtendsDepth.
func ExtendsRoot(services map[string]config.ServiceConfig, name string) string {
	current := name
	for range maxDepth {
		svc, ok := services[current]
		if !ok || svc.Extends == "" {
			return current
		}
		current = svc.Extends
	}
	return current
}

// TemplateData holds the context for rendering ai/ide/git templates.
//
// Service is the canonical config identity (root of the extends chain) — use
// it for raw-config lookups keyed by service name. Resolved is the actual
// rendering service (the collision-policy winner) and equals Service when the
// rendering service has no extends chain. ServiceCfg is the merged service
// block of the rendering service (Resolved).
type TemplateData struct {
	Project    config.ProjectConfig
	Service    string
	Resolved   string
	ServiceCfg config.ServiceConfig
	Runtime    config.RuntimeConfig
	Services   map[string]config.ServiceConfig
	Cfg        *config.DweConfig
}

// AppServices returns services whose Type is "app".
func (d TemplateData) AppServices() map[string]config.ServiceConfig {
	return filterServices(d.Services, config.ServiceTypeApp)
}

// ToolServices returns services whose Type is "tool".
func (d TemplateData) ToolServices() map[string]config.ServiceConfig {
	return filterServices(d.Services, config.ServiceTypeTool)
}

// InfraServices returns services whose Type is "infra".
func (d TemplateData) InfraServices() map[string]config.ServiceConfig {
	return filterServices(d.Services, config.ServiceTypeInfra)
}

func filterServices(svcs map[string]config.ServiceConfig, t config.ServiceType) map[string]config.ServiceConfig {
	out := make(map[string]config.ServiceConfig, len(svcs))
	for name, svc := range svcs {
		if svc.Type == t {
			out[name] = svc
		}
	}
	return out
}

// DryRunRender resolves, parses, and executes every render entry in m against
// data without writing to disk, resolving sources under the given pack kind
// ("ai" | "ide" | "git"). Returns a map from manifest `from` path to the first
// error encountered for that entry (parse, source-read, or execution errors —
// typically missingkey=error). On success returns nil.
func DryRunRender(kind, projectRoot, packName string, m *manifest.File, data TemplateData) map[string]error {
	if m == nil || data.Cfg == nil {
		return nil
	}
	var failures map[string]error
	for _, entry := range m.Render {
		if err := executeTemplateInMemory(kind, projectRoot, packName, entry.From, data); err != nil {
			if failures == nil {
				failures = make(map[string]error)
			}
			failures[entry.From] = err
		}
	}
	return failures
}

func executeTemplateInMemory(kind, projectRoot, packName, rel string, data TemplateData) error {
	sourcePath, _, err := packroot.Resolve(projectRoot, kind, packName, rel)
	if err != nil {
		return fmt.Errorf("resolve template %s: %w", rel, err)
	}
	tplBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read template %s: %w", sourcePath, err)
	}
	name := filepath.Base(sourcePath)
	t, err := template.New(name).Option("missingkey=error").Parse(string(tplBytes))
	if err != nil {
		return fmt.Errorf("parse template %s: %w", name, err)
	}
	if err := t.Execute(&bytes.Buffer{}, data); err != nil {
		return fmt.Errorf("render template %s: %w", name, err)
	}
	return nil
}
