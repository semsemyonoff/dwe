package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// IsolationKind classifies a compose-isolation finding.
type IsolationKind string

// Isolation finding kinds.
const (
	KindContainerName   IsolationKind = "container_name"
	KindRawHostPort     IsolationKind = "raw_host_port"
	KindNamedVolume     IsolationKind = "named_volume"
	KindNamedNetwork    IsolationKind = "named_network"
	KindExternalVolume  IsolationKind = "external_volume"
	KindExternalNetwork IsolationKind = "external_network"
)

// IsolationFinding is one construct in a project's raw compose files that
// bypasses Docker-Compose project-name scoping (see ScanComposeIsolation).
type IsolationFinding struct {
	Kind     IsolationKind
	Resource string // service name or volume/network name
	HostPort int    // raw_host_port only, else 0
	Blocking bool   // intrinsic: causes a hard collision with the working env
	Message  string
	File     string // compose file the finding came from
}

// composeScanDoc is the narrow shape ScanComposeIsolation needs from a compose
// file. Anything else in the file is ignored.
type composeScanDoc struct {
	Services map[string]composeScanService     `yaml:"services"`
	Volumes  map[string]composeScanNamedEntity `yaml:"volumes"`
	Networks map[string]composeScanNamedEntity `yaml:"networks"`
}

type composeScanService struct {
	ContainerName string      `yaml:"container_name"`
	Ports         []yaml.Node `yaml:"ports"`
}

type composeScanNamedEntity struct {
	Name     string    `yaml:"name"`
	External yaml.Node `yaml:"external"`
}

// composeScanLongPort is the long compose port syntax:
// `{ target: 80, published: 8080 }`. `published` may decode as either an int
// or a string depending on how the author quoted it.
type composeScanLongPort struct {
	Published yaml.Node `yaml:"published"`
}

// hostPortLiteralRe matches a literal host-port token: a single port or a
// literal range (`8080-8090`). Anything else (an env-var/${...} token, or no
// match at all) is not a literal and is ignored.
var hostPortLiteralRe = regexp.MustCompile(`^\d+(-\d+)?$`)

// ScanComposeIsolation parses the project's raw compose files (cfg.ComposeFiles())
// for constructs that bypass Docker-Compose project-name scoping:
// container_name:, literal host ports not modelled in dwe ports, and
// external:/explicitly-named volumes & networks.
//
// This is a leaf function (no diag/validate import) — Blocking is intrinsic
// to the finding kind; callers decide their own severity policy.
//
// An unreadable or malformed compose file is skipped silently: the copy's
// `dwe validate` subprocess / `docker compose` itself surface real parse
// errors, so this scanner stays advisory.
func ScanComposeIsolation(cfg *DweConfig, projectRoot string) []IsolationFinding {
	var findings []IsolationFinding

	for _, rel := range cfg.ComposeFiles() {
		abs := rel
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(projectRoot, rel)
		}

		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		var doc composeScanDoc
		if err := yaml.Unmarshal(data, &doc); err != nil {
			continue
		}

		findings = append(findings, scanComposeDoc(doc, abs)...)
	}

	return findings
}

func scanComposeDoc(doc composeScanDoc, file string) []IsolationFinding {
	var findings []IsolationFinding

	for name, svc := range doc.Services {
		if svc.ContainerName != "" {
			findings = append(findings, IsolationFinding{
				Kind:     KindContainerName,
				Resource: name,
				Blocking: true,
				Message: "service " + name + " sets container_name: " + svc.ContainerName +
					" — bypasses compose project-name scoping and collides with any other" +
					" project/run using the same fixed name",
				File: file,
			})
		}

		for _, portNode := range svc.Ports {
			if f, ok := scanPortNode(name, portNode, file); ok {
				findings = append(findings, f)
			}
		}
	}

	for name, vol := range doc.Volumes {
		findings = append(findings, scanNamedEntity(vol, name, file, KindExternalVolume, KindNamedVolume)...)
	}
	for name, net := range doc.Networks {
		findings = append(findings, scanNamedEntity(net, name, file, KindExternalNetwork, KindNamedNetwork)...)
	}

	return findings
}

func scanNamedEntity(e composeScanNamedEntity, name, file string, externalKind, namedKind IsolationKind) []IsolationFinding {
	var findings []IsolationFinding

	if entityTruthy(e.External) {
		findings = append(findings, IsolationFinding{
			Kind:     externalKind,
			Resource: name,
			Blocking: false,
			Message:  string(externalKind) + " " + name + " is declared external: — shared with other projects/runs, not scoped to this one",
			File:     file,
		})
	}
	if e.Name != "" {
		findings = append(findings, IsolationFinding{
			Kind:     namedKind,
			Resource: name,
			Blocking: false,
			Message:  string(namedKind) + " " + name + " sets an explicit name: " + e.Name + " — bypasses compose project-name scoping",
			File:     file,
		})
	}

	return findings
}

// entityTruthy reports whether an `external:` node is truthy: either the bool
// `true`, or a non-empty mapping (`external: { name: foo }`).
func entityTruthy(n yaml.Node) bool {
	switch n.Kind {
	case yaml.ScalarNode:
		v, err := strconv.ParseBool(n.Value)
		return err == nil && v
	case yaml.MappingNode:
		return len(n.Content) > 0
	default:
		return false
	}
}

// scanPortNode extracts a literal host port from one `ports:` entry, in
// either short (`"8080:80"`) or long (`{ target: 80, published: 8080 }`)
// compose syntax.
func scanPortNode(service string, n yaml.Node, file string) (IsolationFinding, bool) {
	switch n.Kind {
	case yaml.ScalarNode:
		return scanShortPort(service, n.Value, file)
	case yaml.MappingNode:
		var long composeScanLongPort
		if err := n.Decode(&long); err != nil {
			return IsolationFinding{}, false
		}
		return scanPublishedToken(service, publishedNodeToken(long.Published), file)
	default:
		return IsolationFinding{}, false
	}
}

func publishedNodeToken(n yaml.Node) string {
	if n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

// scanShortPort parses the compose short port syntax:
//
//	"8080:80"             host:container
//	"127.0.0.1:8080:80"   ip:host:container
//	"8080-8090:80-90"     host range : container range
//	"80"                  container only, random host port — not a finding
//
// An optional trailing "/tcp" or "/udp" protocol suffix is stripped first.
func scanShortPort(service, raw string, file string) (IsolationFinding, bool) {
	spec := raw
	if idx := strings.LastIndex(spec, "/"); idx != -1 {
		proto := spec[idx+1:]
		if proto == "tcp" || proto == "udp" {
			spec = spec[:idx]
		}
	}

	// IPv6 bracketed hosts ([::1]:8080:80) are out of scope — the naive colon
	// split below would misparse them, so bail out rather than false-positive.
	if strings.HasPrefix(spec, "[") {
		return IsolationFinding{}, false
	}

	parts := strings.Split(spec, ":")
	switch len(parts) {
	case 1:
		// container-port-only — random host port, not a finding.
		return IsolationFinding{}, false
	case 2:
		return scanPublishedToken(service, parts[0], file)
	case 3:
		return scanPublishedToken(service, parts[1], file)
	default:
		return IsolationFinding{}, false
	}
}

// scanPublishedToken decides whether a host-published token is a literal
// port/range worth flagging, ignoring ${...}/env-var interpolated tokens.
func scanPublishedToken(service, token, file string) (IsolationFinding, bool) {
	token = strings.TrimSpace(token)
	if token == "" || !hostPortLiteralRe.MatchString(token) {
		return IsolationFinding{}, false
	}

	low := token
	message := "service " + service + " publishes literal host port " + token +
		" — bypasses dwe's auto/ports_free port management and collides with any other" +
		" project/run binding the same host port"
	if before, _, ok := strings.Cut(token, "-"); ok {
		low = before
	}

	port, err := strconv.Atoi(low)
	if err != nil {
		return IsolationFinding{}, false
	}

	return IsolationFinding{
		Kind:     KindRawHostPort,
		Resource: service,
		HostPort: port,
		Blocking: true,
		Message:  message,
		File:     file,
	}, true
}
