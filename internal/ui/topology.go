package ui

import (
	"sort"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/tree"
	"gopkg.in/yaml.v3"
)

// NodeStatus represents the display status of a topology node.
type NodeStatus int

// NodeStatus constants describe the running state of a topology node.
const (
	NodeUnknown  NodeStatus = iota // status unavailable (docker not running)
	NodeRunning                    // container is running
	NodeStopped                    // container is stopped
	NodeDisabled                   // service/tool is disabled in config
)

// NodeCategory represents the role of a topology node.
type NodeCategory int

// NodeCategory constants describe the category of a compose service.
const (
	CatInfra   NodeCategory = iota // infrastructure (nginx, db, redis, …)
	CatService                     // application service
	CatTool                        // optional dev tool
)

// ParseComposeTopology parses the YAML output of `docker compose config`
// and returns a map of service name → sorted list of services it depends on.
// The depends_on field may be a YAML map (expanded form) or sequence (short form).
func ParseComposeTopology(data []byte) (map[string][]string, error) {
	var raw struct {
		Services map[string]struct {
			DependsOn yaml.Node `yaml:"depends_on"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	result := make(map[string][]string, len(raw.Services))
	for name, svc := range raw.Services {
		deps := extractDependsOn(&svc.DependsOn)
		sort.Strings(deps)
		result[name] = deps
	}
	return result, nil
}

// extractDependsOn extracts service names from a depends_on YAML node.
// Supports both the map form (depends_on: {svc: {condition: ...}}) and the
// sequence form (depends_on: [svc, ...]).
func extractDependsOn(n *yaml.Node) []string {
	if n == nil || n.Kind == 0 {
		return nil
	}
	switch n.Kind {
	case yaml.MappingNode:
		// Keys are service names; values are condition objects.
		deps := make([]string, 0, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			deps = append(deps, n.Content[i].Value)
		}
		return deps
	case yaml.SequenceNode:
		deps := make([]string, 0, len(n.Content))
		for _, child := range n.Content {
			if child.Value != "" {
				deps = append(deps, child.Value)
			}
		}
		return deps
	}
	return nil
}

// RenderTopology renders the compose service dependency graph as an indented tree.
// deps maps service name → services it depends on (from ParseComposeTopology).
// status maps service name → NodeStatus for node coloring.
// categories maps service name → NodeCategory for category labels.
// Services absent from status are treated as NodeUnknown (no color annotation).
// Services absent from categories default to CatInfra.
// Returns an empty string when deps is empty.
func RenderTopology(deps map[string][]string, status map[string]NodeStatus, categories map[string]NodeCategory) string {
	if len(deps) == 0 {
		return ""
	}

	// Find root nodes: services not listed as a dependency of any other service.
	isDep := make(map[string]bool, len(deps))
	for _, children := range deps {
		for _, child := range children {
			isDep[child] = true
		}
	}

	roots := make([]string, 0, len(deps))
	for name := range deps {
		if !isDep[name] {
			roots = append(roots, name)
		}
	}
	sort.Strings(roots)

	// If everything is a dependency of something else (e.g. a standalone leaf
	// graph without a clear root), fall back to showing all services sorted.
	if len(roots) == 0 {
		for name := range deps {
			roots = append(roots, name)
		}
		sort.Strings(roots)
	}

	root := tree.Root(styleAccent.Bold(true).Render("⁜ Devbox"))
	for _, name := range roots {
		root.Child(buildTopoNode(name, deps, status, categories, nil))
	}

	return root.String()
}

// buildTopoNode recursively builds a *tree.Tree for the given service.
// ancestors tracks the current path to detect cycles and prevent infinite loops.
func buildTopoNode(name string, deps map[string][]string, status map[string]NodeStatus, categories map[string]NodeCategory, ancestors map[string]bool) *tree.Tree {
	node := tree.Root(topoNodeLabel(name, status, categories))

	// Guard against cycles (compose shouldn't have them, but be safe).
	if ancestors[name] {
		return node
	}

	// Copy ancestors for this level to allow the same node in sibling subtrees.
	next := make(map[string]bool, len(ancestors)+1)
	for k := range ancestors {
		next[k] = true
	}
	next[name] = true

	for _, child := range deps[name] {
		node.Child(buildTopoNode(child, deps, status, categories, next))
	}
	return node
}

// categoryLabel returns the display string for a node category. Labels match
// the headings used by `devbox status apps/tools/infra` and feed into
// serviceTypeStyle so the topology palette stays in sync with the rest of the
// CLI.
func categoryLabel(cat NodeCategory) string {
	switch cat {
	case CatService:
		return "app"
	case CatTool:
		return "tool"
	default:
		return "infra"
	}
}

// categoryStyle returns the Lipgloss style for a node category tag, delegating
// to the shared service-type palette so app/tool/infra colors are defined in
// one place (styles.go).
func categoryStyle(cat NodeCategory) lipgloss.Style {
	return serviceTypeStyle(categoryLabel(cat))
}

// topoNodeLabel formats a service name with a category tag and status annotation.
// Category tag uses a per-category color; disabled nodes render the entire label in gray.
func topoNodeLabel(name string, status map[string]NodeStatus, categories map[string]NodeCategory) string {
	cat := CatInfra
	if categories != nil {
		if c, ok := categories[name]; ok {
			cat = c
		}
	}

	// Check if the node is disabled — render everything in muted gray.
	st, hasStatus := status[name]
	if hasStatus && st == NodeDisabled {
		return styleMuted.Render(name) +
			" " + styleMuted.Render("["+categoryLabel(cat)+"]") +
			" " + styleMuted.Render("(disabled)")
	}

	// Normal node: name in default color, category tag in its own color.
	sb := name + " " + categoryStyle(cat).Render("["+categoryLabel(cat)+"]")

	if !hasStatus {
		return sb
	}

	var label string
	var style lipgloss.Style
	switch st {
	case NodeRunning:
		label = "running"
		style = styleSuccess
	case NodeStopped:
		label = "stopped"
		style = styleWarning
	default:
		return sb
	}
	return sb + " " + style.Render("("+label+")")
}
