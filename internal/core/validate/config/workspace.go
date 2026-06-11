package config

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"
	"github.com/semsemyonoff/dwe/internal/core/workflow/reset"
)

var errNotExist = os.ErrNotExist

// relPath returns the relative path from projectRoot to path, or the path as-is if relative resolution fails.
func relPath(projectRoot, path string) string {
	rel, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return path
	}
	return rel
}

type workspaceValidator struct{}

func (v *workspaceValidator) ID() string {
	return "workspace"
}

func (v *workspaceValidator) Domain() string {
	return "config"
}

func (v *workspaceValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	configPath := ctx.ConfigPath
	if configPath == "" {
		configPath = filepath.Join(ctx.ProjectRoot, "workspace.yml")
	}

	// Config loading and validation
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.workspace",
			File:     relPath(ctx.ProjectRoot, configPath),
			Message:  err.Error(),
		})
	} else {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   "config",
			Target:   "config.workspace",
			File:     relPath(ctx.ProjectRoot, configPath),
		})

		// Validate docs configuration
		if cfg.Docs.Mermaid != "" && cfg.Docs.Mermaid != "auto" && cfg.Docs.Mermaid != "mmdc" && cfg.Docs.Mermaid != "off" {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.workspace.docs.mermaid",
				File:     relPath(ctx.ProjectRoot, configPath),
				Message:  fmt.Sprintf("docs.mermaid: %q is invalid; must be one of auto, mmdc, off", cfg.Docs.Mermaid),
			})
		}
		if cfg.Docs.CacheSizeMB < 0 {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.workspace.docs.cache_size_mb",
				File:     relPath(ctx.ProjectRoot, configPath),
				Message:  fmt.Sprintf("docs.cache_size_mb: %d is invalid; must be non-negative", cfg.Docs.CacheSizeMB),
			})
		}

		// Validate bridge.vars_writable entries are vars.* patterns. The
		// container-write gate fails closed on a malformed entry (it matches
		// nothing), so a stray pattern silently denies rather than load-fails —
		// surface it as a diagnostic so the author notices a typo.
		for _, pat := range config.BridgeVarsWritable(cfg) {
			if !varsWritablePatternValid(pat) {
				diags = append(diags, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "config",
					Target:   "config.workspace.bridge.vars_writable",
					File:     relPath(ctx.ProjectRoot, configPath),
					Message:  fmt.Sprintf("bridge.vars_writable: %q is invalid; must be a vars.* path (e.g. vars.db.host) or wildcard (vars.db.*)", pat),
				})
			}
		}
	}

	return diags
}

// varsWritablePatternValid reports whether a bridge.vars_writable entry is a
// well-formed, vars-namespaced pattern that config.VarsWritableAllows can match.
// It mirrors the matcher's structural rules so a typo that would silently
// fail-closed (e.g. an interior wildcard `vars.*.host`, or a non-vars path) is
// surfaced as a diagnostic instead of quietly denying every container write.
func varsWritablePatternValid(pat string) bool {
	if base, ok := strings.CutSuffix(pat, ".*"); ok {
		// Trailing wildcard: base must be non-empty, vars-namespaced, and carry
		// no interior '*' (only the single trailing `.*` is supported).
		if base == "" || strings.Contains(base, "*") {
			return false
		}
		return base == "vars" || strings.HasPrefix(base, "vars.")
	}
	// Exact pattern: no '*' anywhere, vars-namespaced, not the bare prefix.
	if strings.Contains(pat, "*") {
		return false
	}
	return strings.HasPrefix(pat, "vars.") && pat != "vars."
}

type servicesValidator struct{}

// Compile-time interface check.
var _ validate.Validator = (*servicesValidator)(nil)

func (v *servicesValidator) ID() string {
	return "services"
}

func (v *servicesValidator) Domain() string {
	return "config"
}

// servicesAllowedFields mirrors the per-type field allowlist owned by the
// loader (config.allowedFieldsFor). Duplicated here intentionally — the loader
// helper stays unexported per project Go conventions, and the table is small
// and stable. Any change in the loader allowlist must be mirrored here (the
// services_loader tests guard the loader side; servicesValidator tests guard
// this side).
var servicesAllowedFields = map[config.ServiceType]map[string]bool{
	config.ServiceTypeApp: {
		"type": true, "container": true, "required": true, "compose": true,
		"ports": true, "hosts": true, "icon": true, "info": true, "status": true,
		"on_enable": true, "on_disable": true, "notes": true, "bridge": true,
		"depends_on": true,
		"dir":        true, "dir_internal": true, "work_dir_internal": true,
		"configs": true, "dirs": true, "extends": true, "cli": true, "render": true,
		"generated": true,
	},
	config.ServiceTypeInfra: {
		"type": true, "container": true, "required": true, "compose": true,
		"ports": true, "hosts": true, "icon": true, "info": true, "status": true, "depends_on": true,
		"on_enable": true, "on_disable": true, "notes": true, "bridge": true,
	},
	config.ServiceTypeTool: {
		"type": true, "container": true, "required": true, "compose": true,
		"ports": true, "hosts": true, "icon": true, "info": true, "status": true,
		"on_enable": true, "on_disable": true, "notes": true, "bridge": true,
	},
}

func (v *servicesValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	servicesDir := filepath.Join(ctx.ProjectRoot, "workspace", "services")

	entries, err := os.ReadDir(servicesDir)
	if err != nil {
		if errors.Is(err, errNotExist) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.services",
				File:     relPath(ctx.ProjectRoot, servicesDir),
				Message:  "no workspace/services/ directory; no services defined",
			})
			return diags
		}
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.services",
			File:     relPath(ctx.ProjectRoot, servicesDir),
			Message:  err.Error(),
		})
		return diags
	}

	// Build name → type index from raw folder data so depends_on can
	// cross-reference targets. Merged map from ctx.Cfg wins when set.
	typesByName := make(map[string]config.ServiceType)

	perServiceDiags := []validate.Diagnostic{}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		svcFile := filepath.Join(servicesDir, name, "service.yml")
		file := relPath(ctx.ProjectRoot, svcFile)
		target := "config.services:" + name

		emit := func(d validate.Diagnostic) {
			d.Domain = "config"
			d.File = file
			perServiceDiags = append(perServiceDiags, d)
		}

		data, err := os.ReadFile(svcFile)
		if err != nil {
			if errors.Is(err, errNotExist) {
				emit(validate.Diagnostic{
					Severity: validate.SeverityError,
					Target:   target,
					Message:  fmt.Sprintf("service directory %q has no service.yml", name),
				})
			} else {
				emit(validate.Diagnostic{
					Severity: validate.SeverityError,
					Target:   target,
					Message:  err.Error(),
				})
			}
			continue
		}

		var rawEntry map[string]any
		if err := yaml.Unmarshal(data, &rawEntry); err != nil {
			emit(validate.Diagnostic{
				Severity: validate.SeverityError,
				Target:   target,
				Message:  fmt.Sprintf("parse service.yml: %v", err),
			})
			continue
		}
		if rawEntry == nil {
			rawEntry = map[string]any{}
		}

		// type required + valid.
		typeRaw, hasType := rawEntry["type"]
		if !hasType {
			emit(validate.Diagnostic{
				Severity: validate.SeverityError,
				Target:   target,
				Message:  fmt.Sprintf("service %q: missing type", name),
				Hint:     "add type: app | tool | infra",
			})
			continue
		}
		typeStr, _ := typeRaw.(string)
		svcType := config.ServiceType(typeStr)
		if err := svcType.Validate(); err != nil {
			emit(validate.Diagnostic{
				Severity: validate.SeverityError,
				Target:   target,
				Message:  fmt.Sprintf("service %q: %v", name, err),
				Hint:     "type must be one of: app, tool, infra",
			})
			continue
		}

		typesByName[name] = svcType

		// extends only for app.
		if extRaw, ok := rawEntry["extends"]; ok && extRaw != nil && !svcType.IsApp() {
			emit(validate.Diagnostic{
				Severity: validate.SeverityError,
				Target:   target,
				Message:  fmt.Sprintf("service %q (type %s): extends only permitted for type app", name, svcType),
			})
		}

		// Per-type field allowlist.
		allowed := servicesAllowedFields[svcType]
		for _, key := range slices.Sorted(maps.Keys(rawEntry)) {
			if key == "extends" && !svcType.IsApp() {
				continue
			}
			if !allowed[key] {
				emit(validate.Diagnostic{
					Severity: validate.SeverityError,
					Target:   target,
					Message:  fmt.Sprintf("service %q (type %s): field %q not allowed", name, svcType, key),
				})
			}
		}

		// ports shape + range. Accepts bare int or mapping {port, scheme}.
		if vv, ok := rawEntry["ports"]; ok && vv != nil {
			m, isMap := vv.(map[string]any)
			if !isMap {
				emit(validate.Diagnostic{
					Severity: validate.SeverityError,
					Target:   target,
					Message:  fmt.Sprintf("service %q: ports must be a map of name to port number (int) or {port, scheme}", name),
				})
			} else {
				for _, p := range slices.Sorted(maps.Keys(m)) {
					switch pv := m[p].(type) {
					case int:
						if pv < 1 || pv > 65535 {
							emit(validate.Diagnostic{
								Severity: validate.SeverityError,
								Target:   target,
								Message:  fmt.Sprintf("service %q port %q = %d: out of range 1..65535", name, p, pv),
							})
						}
					case map[string]any:
						for k := range pv {
							if k != "port" && k != "scheme" {
								emit(validate.Diagnostic{
									Severity: validate.SeverityError,
									Target:   target,
									Message:  fmt.Sprintf("service %q port %q: unknown field %q", name, p, k),
									Hint:     "allowed fields: port, scheme",
								})
							}
						}
						portRaw, hasPort := pv["port"]
						if !hasPort {
							emit(validate.Diagnostic{
								Severity: validate.SeverityError,
								Target:   target,
								Message:  fmt.Sprintf("service %q port %q: missing port", name, p),
							})
						} else {
							n, ok := portRaw.(int)
							if !ok {
								emit(validate.Diagnostic{
									Severity: validate.SeverityError,
									Target:   target,
									Message:  fmt.Sprintf("service %q port %q: port is not an integer", name, p),
								})
							} else if n < 1 || n > 65535 {
								emit(validate.Diagnostic{
									Severity: validate.SeverityError,
									Target:   target,
									Message:  fmt.Sprintf("service %q port %q = %d: out of range 1..65535", name, p, n),
								})
							}
						}
						if schRaw, ok := pv["scheme"]; ok && schRaw != nil {
							s, isStr := schRaw.(string)
							if !isStr {
								emit(validate.Diagnostic{
									Severity: validate.SeverityError,
									Target:   target,
									Message:  fmt.Sprintf("service %q port %q: scheme is not a string", name, p),
								})
							} else if s != "" && s != "http" && s != "https" {
								emit(validate.Diagnostic{
									Severity: validate.SeverityError,
									Target:   target,
									Message:  fmt.Sprintf("service %q port %q: scheme %q is not allowed", name, p, s),
									Hint:     "scheme must be one of: http, https",
								})
							}
						}
					default:
						emit(validate.Diagnostic{
							Severity: validate.SeverityError,
							Target:   target,
							Message:  fmt.Sprintf("service %q port %q: not an integer or a mapping {port, scheme}", name, p),
						})
					}
				}
			}
		}
		// hosts shape.
		if vv, ok := rawEntry["hosts"]; ok && vv != nil {
			if _, isMap := vv.(map[string]any); !isMap {
				emit(validate.Diagnostic{
					Severity: validate.SeverityError,
					Target:   target,
					Message:  fmt.Sprintf("service %q: hosts must be a map of name to hostname", name),
				})
			}
		}

		// service.info validation.
		if infoRaw, ok := rawEntry["info"]; ok && infoRaw != nil {
			info, isMap := infoRaw.(map[string]any)
			if !isMap {
				emit(validate.Diagnostic{
					Severity: validate.SeverityError,
					Target:   target,
					Message:  fmt.Sprintf("service %q: info must be a map", name),
				})
			} else {
				// Validate scheme: allowed values are "" / "http" / "https".
				if schRaw, ok := info["scheme"]; ok && schRaw != nil {
					s, isStr := schRaw.(string)
					if !isStr {
						emit(validate.Diagnostic{
							Severity: validate.SeverityError,
							Target:   target,
							Message:  fmt.Sprintf("service %q: info.scheme must be a string", name),
						})
					} else if s != "" && s != "http" && s != "https" {
						emit(validate.Diagnostic{
							Severity: validate.SeverityError,
							Target:   target,
							Message:  fmt.Sprintf("service %q: info.scheme %q is not allowed", name, s),
							Hint:     "scheme must be one of: http, https",
						})
					}
				}

				// Validate title: check for control characters.
				if titleRaw, ok := info["title"]; ok && titleRaw != nil {
					title, isStr := titleRaw.(string)
					if !isStr {
						emit(validate.Diagnostic{
							Severity: validate.SeverityError,
							Target:   target,
							Message:  fmt.Sprintf("service %q: info.title must be a string", name),
						})
					} else {
						for _, r := range title {
							if unicode.IsControl(r) {
								emit(validate.Diagnostic{
									Severity: validate.SeverityError,
									Target:   target,
									Message:  fmt.Sprintf("service %q: info.title contains control characters", name),
								})
								break
							}
						}
					}
				}

				// Validate paths.
				if pathsRaw, ok := info["paths"]; ok && pathsRaw != nil {
					paths, isList := pathsRaw.([]any)
					if !isList {
						emit(validate.Diagnostic{
							Severity: validate.SeverityError,
							Target:   target,
							Message:  fmt.Sprintf("service %q: info.paths must be a list", name),
						})
					} else {
						seenNames := make(map[string]bool)
						for i, pathItem := range paths {
							pathMap, isMap := pathItem.(map[string]any)
							if !isMap {
								emit(validate.Diagnostic{
									Severity: validate.SeverityError,
									Target:   target,
									Message:  fmt.Sprintf("service %q: info.paths[%d] must be a map", name, i),
								})
								continue
							}

							// Validate path name.
							nameRaw, hasName := pathMap["name"]
							if !hasName {
								emit(validate.Diagnostic{
									Severity: validate.SeverityError,
									Target:   target,
									Message:  fmt.Sprintf("service %q: info.paths[%d]: name is required", name, i),
								})
								continue
							}
							pathName, isStr := nameRaw.(string)
							if !isStr {
								emit(validate.Diagnostic{
									Severity: validate.SeverityError,
									Target:   target,
									Message:  fmt.Sprintf("service %q: info.paths[%d]: name must be a string", name, i),
								})
								continue
							}
							if pathName == "" {
								emit(validate.Diagnostic{
									Severity: validate.SeverityError,
									Target:   target,
									Message:  fmt.Sprintf("service %q: info.paths[%d]: name is empty", name, i),
								})
								continue
							}

							// Check for duplicate names.
							if seenNames[pathName] {
								emit(validate.Diagnostic{
									Severity: validate.SeverityError,
									Target:   target,
									Message:  fmt.Sprintf("service %q: info.paths: duplicate name %q", name, pathName),
									Hint:     fmt.Sprintf("remove or rename the duplicate %q", pathName),
								})
							}
							seenNames[pathName] = true

							// Validate path field.
							pathRaw, hasPath := pathMap["path"]
							if !hasPath {
								emit(validate.Diagnostic{
									Severity: validate.SeverityError,
									Target:   target,
									Message:  fmt.Sprintf("service %q: info.paths[%d] (%q): path is required", name, i, pathName),
								})
								continue
							}
							pathStr, isStr := pathRaw.(string)
							if !isStr {
								emit(validate.Diagnostic{
									Severity: validate.SeverityError,
									Target:   target,
									Message:  fmt.Sprintf("service %q: info.paths[%d] (%q): path must be a string", name, i, pathName),
								})
								continue
							}
							if pathStr == "" {
								emit(validate.Diagnostic{
									Severity: validate.SeverityError,
									Target:   target,
									Message:  fmt.Sprintf("service %q: info.paths[%d] (%q): path is empty", name, i, pathName),
								})
							} else if pathStr[0] != '/' {
								emit(validate.Diagnostic{
									Severity: validate.SeverityWarning,
									Target:   target,
									Message:  fmt.Sprintf("service %q: info.paths[%d] (%q): path %q does not start with /", name, i, pathName, pathStr),
								})
							}
						}
					}
				}
			}
		}

		// App missing dir/dir_internal → warning.
		if svcType.IsApp() {
			hasDir := false
			hasDirInternal := false
			hasMergedService := false
			if ctx.Cfg != nil {
				if svc, ok := ctx.Cfg.Services[name]; ok {
					hasMergedService = true
					hasDir = svc.Dir != ""
					hasDirInternal = svc.DirInternal != ""
				}
			}
			if !hasDir && !hasDirInternal {
				_, hasDir = rawEntry["dir"]
				_, hasDirInternal = rawEntry["dir_internal"]
			}
			if !hasMergedService && !hasDir && !hasDirInternal {
				if _, extends := rawEntry["extends"]; extends {
					continue
				}
			}
			if !hasDir && !hasDirInternal {
				emit(validate.Diagnostic{
					Severity: validate.SeverityWarning,
					Target:   target,
					Message:  fmt.Sprintf("service %q (type app) has no dir or dir_internal", name),
				})
			}
		}
	}

	// Augment typesByName from ctx.Cfg (catches services not yet validated above).
	if ctx.Cfg != nil {
		for name, svc := range ctx.Cfg.Services {
			typesByName[name] = svc.Type
		}
	}

	// depends_on cross-reference check (needs complete typesByName).
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		svcFile := filepath.Join(servicesDir, name, "service.yml")
		file := relPath(ctx.ProjectRoot, svcFile)
		target := "config.services:" + name

		emit := func(d validate.Diagnostic) {
			d.Domain = "config"
			d.File = file
			perServiceDiags = append(perServiceDiags, d)
		}

		data, err := os.ReadFile(svcFile)
		if err != nil {
			continue
		}
		var rawEntry map[string]any
		if err := yaml.Unmarshal(data, &rawEntry); err != nil || rawEntry == nil {
			continue
		}
		if vv, ok := rawEntry["depends_on"]; ok && vv != nil {
			if list, isList := vv.([]any); isList {
				for _, item := range list {
					t, _ := item.(string)
					if t == "" {
						continue
					}
					if parentType, has := typesByName[t]; has && parentType.IsTool() {
						emit(validate.Diagnostic{
							Severity: validate.SeverityError,
							Target:   target,
							Message:  fmt.Sprintf("service %q: depends_on target %q is type tool", name, t),
							Hint:     "tools cannot be depends_on targets; convert target to type infra",
						})
					}
				}
			}
		}
	}

	// Emit an OK summary diagnostic only when nothing errored.
	hasError := false
	for _, d := range perServiceDiags {
		if d.Severity == validate.SeverityError {
			hasError = true
			break
		}
	}
	if !hasError {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   "config",
			Target:   "config.services",
			File:     relPath(ctx.ProjectRoot, servicesDir),
		})
	}
	diags = append(diags, perServiceDiags...)
	return diags
}

type dockerValidator struct{}

func (v *dockerValidator) ID() string {
	return "docker"
}

func (v *dockerValidator) Domain() string {
	return "config"
}

func (v *dockerValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	dockerPath := filepath.Join(ctx.ProjectRoot, "workspace", "docker.yml")

	// Docker validation requires successful main config load for template resolution
	if ctx.Cfg == nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityInfo,
			Domain:   "config",
			Target:   "config.docker",
			File:     relPath(ctx.ProjectRoot, dockerPath),
			Message:  "docker.yml validation requires successful main config load; skipped",
		})
		return diags
	}

	// Load and validate docker config
	dockerCfg, err := config.LoadDockerConfig(ctx.ProjectRoot, ctx.Cfg)
	if err != nil {
		if errors.Is(err, errNotExist) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.docker",
				File:     relPath(ctx.ProjectRoot, dockerPath),
				Message:  "no docker.yml",
			})
		} else {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.docker",
				File:     relPath(ctx.ProjectRoot, dockerPath),
				Message:  err.Error(),
			})
		}
		return diags
	}

	diags = append(diags, validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "config",
		Target:   "config.docker",
		File:     relPath(ctx.ProjectRoot, dockerPath),
	})

	_ = dockerCfg // Unused; just checking that it loads cleanly

	return diags
}

type infoValidator struct{}

func (v *infoValidator) ID() string {
	return "info"
}

func (v *infoValidator) Domain() string {
	return "config"
}

func (v *infoValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	infoPath := filepath.Join(ctx.ProjectRoot, "workspace", "info.yml")

	// Check if file exists
	fileExists := true
	if _, statErr := os.Stat(infoPath); statErr != nil {
		fileExists = false
		if errors.Is(statErr, os.ErrNotExist) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.info",
				File:     relPath(ctx.ProjectRoot, infoPath),
				Message:  "no info.yml",
			})
		} else {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.info",
				File:     relPath(ctx.ProjectRoot, infoPath),
				Message:  statErr.Error(),
			})
			return diags
		}
	}

	infoCfg, err := config.LoadInfoConfig(infoPath)
	if err != nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.info",
			File:     relPath(ctx.ProjectRoot, infoPath),
			Message:  err.Error(),
		})
		return diags
	}

	// Only emit OK when the file actually exists (missing file already emitted Info above)
	if fileExists {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   "config",
			Target:   "config.info",
			File:     relPath(ctx.ProjectRoot, infoPath),
		})
	}

	// Validate auto-block rules that need cfg.Services
	if ctx.Cfg != nil {
		diags = append(diags, validateInfoAutoBlocks(ctx, infoCfg, infoPath)...)
	}

	return diags
}

func validateInfoAutoBlocks(ctx validate.Context, infoCfg *config.InfoConfig, infoPath string) []validate.Diagnostic {
	var diags []validate.Diagnostic
	file := relPath(ctx.ProjectRoot, infoPath)

	// Build service key set for cross-reference validation
	serviceKeys := make(map[string]bool)
	for name := range ctx.Cfg.Services {
		serviceKeys[name] = true
	}

	// Recursively validate items in sections
	for _, section := range infoCfg.Sections {
		validateInfoItemsAutoBlocks(&diags, section.Items, serviceKeys, ctx.Cfg.Services, file)
	}

	return diags
}

func validateInfoItemsAutoBlocks(diags *[]validate.Diagnostic, items []config.InfoItem, serviceKeys map[string]bool, services map[string]config.ServiceConfig, file string) {
	for _, item := range items {
		target := "config.info"

		// Validate auto-urls
		if item.Type == "auto-urls" && item.SourceAutoURLsSpec != nil {
			spec := item.SourceAutoURLsSpec

			// Validate port_via exists
			if spec.PortVia != "" && !serviceKeys[spec.PortVia] {
				*diags = append(*diags, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "config",
					Target:   target,
					File:     file,
					Message:  fmt.Sprintf("auto-urls: port_via references unknown service %q", spec.PortVia),
				})
			}

			// Validate hide service keys
			for _, hideKey := range spec.Hide {
				if !serviceKeys[hideKey] {
					*diags = append(*diags, validate.Diagnostic{
						Severity: validate.SeverityWarning,
						Domain:   "config",
						Target:   target,
						File:     file,
						Message:  fmt.Sprintf("auto-urls: hide references unknown service key %q", hideKey),
					})
				}
			}

			// Validate hide_paths service keys and path names
			if spec.HidePaths != nil {
				for svcKey, pathNames := range spec.HidePaths {
					if !serviceKeys[svcKey] {
						*diags = append(*diags, validate.Diagnostic{
							Severity: validate.SeverityWarning,
							Domain:   "config",
							Target:   target,
							File:     file,
							Message:  fmt.Sprintf("auto-urls: hide_paths.%s references unknown service key", svcKey),
						})
					}
					// Validate path names for known services
					if svc, ok := services[svcKey]; ok {
						knownPaths := make(map[string]bool)
						for _, p := range svc.Info.Paths {
							knownPaths[p.Name] = true
						}
						for _, pathName := range pathNames {
							if !knownPaths[pathName] {
								*diags = append(*diags, validate.Diagnostic{
									Severity: validate.SeverityWarning,
									Domain:   "config",
									Target:   target,
									File:     file,
									Message:  fmt.Sprintf("auto-urls: hide_paths.%s references unknown path name %q", svcKey, pathName),
								})
							}
						}
					}
				}
			}
		}

		// Validate auto-hosts
		if item.Type == "auto-hosts" && item.SourceAutoHostsSpec != nil {
			spec := item.SourceAutoHostsSpec

			// Validate IP field
			if spec.IP != "" {
				if net.ParseIP(spec.IP) == nil {
					*diags = append(*diags, validate.Diagnostic{
						Severity: validate.SeverityWarning,
						Domain:   "config",
						Target:   target,
						File:     file,
						Message:  fmt.Sprintf("auto-hosts: ip %q does not parse as valid IPv4/IPv6", spec.IP),
					})
				}
			}

			// Validate hide service keys
			for _, hideKey := range spec.Hide {
				if !serviceKeys[hideKey] {
					*diags = append(*diags, validate.Diagnostic{
						Severity: validate.SeverityWarning,
						Domain:   "config",
						Target:   target,
						File:     file,
						Message:  fmt.Sprintf("auto-hosts: hide references unknown service key %q", hideKey),
					})
				}
			}
		}

		// Recurse into subgroup items
		if item.Type == "subgroup" && len(item.Items) > 0 {
			validateInfoItemsAutoBlocks(diags, item.Items, serviceKeys, services, file)
		}
	}
}

type stylesValidator struct{}

func (v *stylesValidator) ID() string {
	return "styles"
}

func (v *stylesValidator) Domain() string {
	return "config"
}

func (v *stylesValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	stylesPath := filepath.Join(ctx.ProjectRoot, "workspace", "styles.yml")

	if _, statErr := os.Stat(stylesPath); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.styles",
				File:     relPath(ctx.ProjectRoot, stylesPath),
				Message:  "no styles.yml",
			})
		} else {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.styles",
				File:     relPath(ctx.ProjectRoot, stylesPath),
				Message:  statErr.Error(),
			})
		}
		return diags
	}

	stylesCfg, err := config.LoadStylesConfig(stylesPath)
	if err != nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.styles",
			File:     relPath(ctx.ProjectRoot, stylesPath),
			Message:  err.Error(),
		})
		return diags
	}

	diags = append(diags, validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "config",
		Target:   "config.styles",
		File:     relPath(ctx.ProjectRoot, stylesPath),
	})

	_ = stylesCfg // Unused; just checking that it loads cleanly

	diags = append(diags, stylesRenameDiagnostics(ctx, stylesPath)...)

	return diags
}

// stylesColorRenames maps old `colors:` keys to their new 7-token target.
// Keys present in the new schema (accent/success/warning/danger/muted/border/text)
// are not in this map and pass through silently.
var stylesColorRenames = map[string]string{
	"label":               "accent",
	"section_title":       "accent",
	"subheader":           "accent",
	"info":                "accent",
	"table_header":        "accent",
	"focus_border":        "accent",
	"filter_match":        "accent",
	"pagination_active":   "accent",
	"mandatory":           "accent",
	"enabled":             "success",
	"partial":             "warning",
	"description":         "muted",
	"tree_count":          "muted",
	"tree_arrow":          "muted",
	"pagination_inactive": "muted",
	"disabled":            "muted",
	"table_border":        "border",
}

func stylesRenameDiagnostics(ctx validate.Context, stylesPath string) []validate.Diagnostic {
	raw, err := os.ReadFile(stylesPath)
	if err != nil {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	root := &doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}

	var diags []validate.Diagnostic
	file := relPath(ctx.ProjectRoot, stylesPath)

	emit := func(target, message, hint string, line int) {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityWarning,
			Domain:   "config",
			Target:   target,
			File:     file,
			Line:     line,
			Message:  message,
			Hint:     hint,
		})
	}

	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]
		switch key.Value {
		case "colors":
			if val.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(val.Content); j += 2 {
				ck := val.Content[j]
				cv := val.Content[j+1]
				if ck.Value == "help" {
					emit(
						"config.styles",
						"colors.help is no longer supported",
						"remove colors.help — Fang help colors are derived from accent + muted",
						ck.Line,
					)
					continue
				}
				if target, ok := stylesColorRenames[ck.Value]; ok {
					emit(
						"config.styles",
						fmt.Sprintf("colors.%s is no longer supported", ck.Value),
						fmt.Sprintf("rename to colors.%s", target),
						ck.Line,
					)
				}
				_ = cv
			}
		case "header":
			if val.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(val.Content); j += 2 {
				hk := val.Content[j]
				if hk.Value == "color" {
					emit(
						"config.styles",
						"header.color is no longer supported",
						"remove header.color — the brand line is always rendered in accent",
						hk.Line,
					)
				}
			}
		}
	}
	return diags
}

type lifecycleValidator struct{}

func (v *lifecycleValidator) ID() string {
	return "lifecycle"
}

func (v *lifecycleValidator) Domain() string {
	return "config"
}

func (v *lifecycleValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	lifecyclePath := filepath.Join(ctx.ProjectRoot, "workspace", "lifecycle.yml")

	lifecycleCfg, err := config.LoadLifecycleConfig(lifecyclePath)
	if err != nil {
		if errors.Is(err, errNotExist) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.lifecycle",
				File:     relPath(ctx.ProjectRoot, lifecyclePath),
				Message:  "no lifecycle.yml",
			})
		} else {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.lifecycle",
				File:     relPath(ctx.ProjectRoot, lifecyclePath),
				Message:  err.Error(),
			})
		}
		return diags
	}

	diags = append(diags, validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "config",
		Target:   "config.lifecycle",
		File:     relPath(ctx.ProjectRoot, lifecyclePath),
	})

	_ = lifecycleCfg // Unused; just checking that it loads cleanly

	return diags
}

type deployValidator struct{}

func (v *deployValidator) ID() string {
	return "deploy"
}

func (v *deployValidator) Domain() string {
	return "config"
}

func (v *deployValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	deployPath := filepath.Join(ctx.ProjectRoot, "workspace", "deploy.yml")

	deployCfg, err := config.ParseDeployConfigForValidation(deployPath)
	if err != nil {
		if errors.Is(err, errNotExist) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.deploy",
				File:     relPath(ctx.ProjectRoot, deployPath),
				Message:  "no deploy.yml",
			})
		} else {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.deploy",
				File:     relPath(ctx.ProjectRoot, deployPath),
				Message:  err.Error(),
			})
		}
		return diags
	}

	diags = append(diags, validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "config",
		Target:   "config.deploy",
		File:     relPath(ctx.ProjectRoot, deployPath),
	})

	_ = deployCfg

	// Cross-reference: resolve the plan to catch step-level errors.
	// Pass nil registry so files_gate validation is skipped here — the dedicated
	// deployFilesGateValidator handles that and emits structured diagnostics.
	if ctx.Cfg != nil {
		if _, err := deploy.ResolvePlan(ctx.Cfg, nil); err != nil {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.deploy",
				File:     relPath(ctx.ProjectRoot, deployPath),
				Message:  fmt.Sprintf("plan resolution failed: %v", err),
				Hint:     "check that all phases and steps reference valid services and commands",
			})
		}
	} else {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityInfo,
			Domain:   "config",
			Target:   "config.deploy",
			File:     relPath(ctx.ProjectRoot, deployPath),
			Message:  "plan resolution skipped: main config did not load",
		})
	}

	return diags
}

type resetValidator struct{}

func (v *resetValidator) ID() string {
	return "reset"
}

func (v *resetValidator) Domain() string {
	return "config"
}

func (v *resetValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	resetPath := filepath.Join(ctx.ProjectRoot, "workspace", "reset.yml")

	resetCfg, err := config.LoadResetConfig(resetPath)
	if err != nil {
		if errors.Is(err, errNotExist) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.reset",
				File:     relPath(ctx.ProjectRoot, resetPath),
				Message:  "no reset.yml",
			})
		} else {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.reset",
				File:     relPath(ctx.ProjectRoot, resetPath),
				Message:  err.Error(),
			})
		}
		return diags
	}

	diags = append(diags, validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "config",
		Target:   "config.reset",
		File:     relPath(ctx.ProjectRoot, resetPath),
	})

	_ = resetCfg

	// Cross-reference: resolve the plan to catch step-level errors.
	// Pass nil registry so files_gate validation is skipped here — the dedicated
	// resetFilesGateValidator handles that and emits structured diagnostics.
	if ctx.Cfg != nil {
		if _, err := reset.ResolvePlan(ctx.Cfg, nil); err != nil {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.reset",
				File:     relPath(ctx.ProjectRoot, resetPath),
				Message:  fmt.Sprintf("plan resolution failed: %v", err),
				Hint:     "check that all phases and steps reference valid services and commands",
			})
		}
	} else {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityInfo,
			Domain:   "config",
			Target:   "config.reset",
			File:     relPath(ctx.ProjectRoot, resetPath),
			Message:  "plan resolution skipped: main config did not load",
		})
	}

	return diags
}

type serviceDeployValidator struct{}

func (v *serviceDeployValidator) ID() string {
	return "service-deploy"
}

func (v *serviceDeployValidator) Domain() string {
	return "config"
}

func (v *serviceDeployValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	// Get services from main config if available, otherwise load them separately
	var services map[string]config.ServiceConfig
	var err error

	if ctx.Cfg != nil && ctx.Cfg.Services != nil {
		services = ctx.Cfg.Services
	} else {
		services, err = config.LoadServices(ctx.ProjectRoot)
		if err != nil {
			// Error loading services; emit a diagnostic and skip service deploy validation
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.service-deploy",
				Message:  fmt.Sprintf("failed to load services: %v", err),
			})
			return diags
		}
		if len(services) == 0 {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.service-deploy",
				File:     relPath(ctx.ProjectRoot, filepath.Join(ctx.ProjectRoot, "workspace", "services")),
				Message:  "no services defined; no service deploy configs to validate",
			})
			return diags
		}
	}

	// Load service deploy configs for all services
	serviceDeployConfigs, err := config.LoadServiceDeployConfigs(ctx.ProjectRoot, services)
	if err != nil {
		// This could be a collection of per-service errors; emit what we can.
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.service-deploy",
			Message:  err.Error(),
		})
		return diags
	}

	if len(serviceDeployConfigs) == 0 {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityInfo,
			Domain:   "config",
			Target:   "config.service-deploy",
			Message:  "no service deploy configs",
		})
		return diags
	}

	diags = append(diags, validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "config",
		Target:   "config.service-deploy",
	})

	return diags
}
