package config

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

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
	Value    string // the declared value (container_name / explicit name:), else ""
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

// composeScanService decodes every field as a yaml.Node rather than its natural
// Go type: compose's merge tags (`!reset` / `!override`) arrive as unresolved
// YAML tags, and yaml.v3 either strips them (a string field keeps the raw text,
// so `image: !reset null` reads as an image literally named "null") or fails the
// decode outright (a `!reset null` scalar into a struct/slice field is a
// TypeError, which would skip the whole file). See mergeTagOf.
type composeScanService struct {
	ContainerName yaml.Node `yaml:"container_name"`
	Ports         yaml.Node `yaml:"ports"`
	Image         yaml.Node `yaml:"image"`
	Build         yaml.Node `yaml:"build"`
	Healthcheck   yaml.Node `yaml:"healthcheck"`
}

// composeScanHealthcheck is the narrow healthcheck shape ScanComposeCost needs.
type composeScanHealthcheck struct {
	Disable     bool   `yaml:"disable"`
	StartPeriod string `yaml:"start_period"`
}

type composeScanNamedEntity struct {
	Name     yaml.Node `yaml:"name"`
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
	// Every construct scanned here is collapsed across the `-f` chain the way
	// compose's own merge resolves it, rather than reported per file: emitting
	// a base declaration an overlay has already corrected would flag a resource
	// the merged stack never creates. Two collapse shapes:
	//
	//   - scalar fields (container_name, volume/network name:/external:) are
	//     last-declaration-wins — `effective` maps the declaration to the index
	//     of its surviving finding, or -1 once a later file cleared it.
	//   - ports: is a sequence, which compose APPENDS across the chain, so every
	//     file's entries stand — until a file replaces the whole list with
	//     `!reset` / `!override`, which drops every earlier file's entries.
	effective := make(map[declKey]int)
	portIdx := make(map[string][]int)
	dropped := make(map[int]bool)

	for _, pf := range parseComposeFiles(cfg, projectRoot) {
		scan := scanComposeDoc(pf.doc, pf.file)
		for _, service := range scan.replacedPorts {
			for _, i := range portIdx[service] {
				dropped[i] = true
			}
			delete(portIdx, service)
		}
		for _, f := range scan.findings {
			findings = append(findings, f)
			switch f.Kind {
			case KindRawHostPort:
				portIdx[f.Resource] = append(portIdx[f.Resource], len(findings)-1)
			default:
				effective[declKey{f.Kind, f.Resource}] = len(findings) - 1
			}
		}
		for _, key := range scan.cleared {
			effective[key] = -1
		}
	}

	out := make([]IsolationFinding, 0, len(findings))
	for i, f := range findings {
		if dropped[i] {
			continue
		}
		if idx, tracked := effective[declKey{f.Kind, f.Resource}]; tracked && idx != i {
			continue
		}
		out = append(out, f)
	}

	return out
}

// declKey identifies one last-declaration-wins declaration across the `-f`
// chain, so at most one finding per key survives the collapse. Never used for
// KindRawHostPort — ports merge by appending, not by replacement.
type declKey struct {
	kind     IsolationKind
	resource string
}

// parsedComposeFile is one successfully parsed file of the active -f chain.
type parsedComposeFile struct {
	doc  composeScanDoc
	file string // absolute path
}

// parseComposeFiles reads and parses the project's active compose chain in
// -f order. Unreadable or malformed files are skipped silently — both
// scanners over this parser are advisory (real parse errors surface via
// `dwe validate` / `docker compose` itself).
//
// A *yaml.TypeError is the one error kept: the document parsed, only some
// field did not fit this scanner's narrow shape (an unknown merge tag on a
// mapping-typed key, say). yaml.v3 decodes everything else it can before
// returning it, and dropping the whole file over one odd field would silently
// blind the scanner to that file's blocking findings.
func parseComposeFiles(cfg *DweConfig, projectRoot string) []parsedComposeFile {
	files := cfg.ComposeFiles()
	out := make([]parsedComposeFile, 0, len(files))

	for _, rel := range files {
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
			var typeErr *yaml.TypeError
			if !errors.As(err, &typeErr) {
				continue
			}
		}

		out = append(out, parsedComposeFile{doc: doc, file: abs})
	}

	return out
}

// ComposeCostFacts summarises what bringing a project's active compose chain
// up would have to build, pull, and wait for. It is the facts-only companion
// of ScanComposeIsolation over the same narrow parser, and carries no verdict:
// callers decide what "expensive" means.
//
// Honest limit, by construction: it reports whether there IS a build, never
// what the build costs. The dominant factor — whether the Docker layer cache
// is warm, seconds versus many minutes — has no static source and is not
// modelled here.
type ComposeCostFacts struct {
	// BuildServices are the compose services declaring build:, sorted.
	BuildServices []string
	// ExternalImages are the distinct image: references of compose services
	// that do NOT declare build:, sorted. A service that builds carries a
	// local tag (image: myproject-app:dev) which is not something to pull, so
	// counting it would make the fact lie in the least helpful direction.
	ExternalImages []string
	// MaxStartPeriod is the largest healthcheck start_period across the chain.
	// Max rather than sum: `docker compose up --wait` waits in parallel, so a
	// sum over-estimates the more services a project has.
	MaxStartPeriod time.Duration
}

// ScanComposeCost parses the project's active compose files (cfg.ComposeFiles(),
// i.e. only the enabled services' overlays) and reports the cost facts above.
//
// Per compose service the chain is merged in -f order the way compose itself
// resolves it: a later file's image: wins, and a build: declared in any file
// marks the service as building.
func ScanComposeCost(cfg *DweConfig, projectRoot string) ComposeCostFacts {
	type serviceFacts struct {
		image       string
		hasBuild    bool
		startPeriod time.Duration
	}

	// Iteration order is irrelevant here — every update is keyed by service
	// name and all output ordering is established below. (The sort in
	// scanComposeDoc is load-bearing; this loop deliberately has none.)
	merged := make(map[string]*serviceFacts)
	for _, pf := range parseComposeFiles(cfg, projectRoot) {
		for name, svc := range pf.doc.Services {
			facts, ok := merged[name]
			if !ok {
				facts = &serviceFacts{}
				merged[name] = facts
			}
			// A later file CLEARING a field must clear the merged fact, not
			// leave it standing — assigning only on the positive branch would
			// keep reporting a build/pull/wait the merged stack never performs.
			// `!reset` clears; `!override` is an ordinary declaration for a
			// scalar field, which already merges by last-one-wins.
			switch {
			case mergeTagOf(svc.Image) == mergeReset:
				facts.image = ""
			case scalarValue(svc.Image) != "":
				facts.image = scalarValue(svc.Image)
			}

			switch {
			case mergeTagOf(svc.Build) == mergeReset:
				facts.hasBuild = false
			case nodePresent(svc.Build):
				facts.hasBuild = true
			}

			hc, declared := decodeHealthcheck(svc.Healthcheck)
			switch {
			case mergeTagOf(svc.Healthcheck) == mergeReset, declared && hc.Disable:
				facts.startPeriod = 0
			case declared:
				if d, ok := parseStartPeriod(hc); ok {
					facts.startPeriod = d
				}
			}
		}
	}

	out := ComposeCostFacts{
		BuildServices:  []string{},
		ExternalImages: []string{},
	}
	seenImage := make(map[string]bool)
	for _, name := range slices.Sorted(maps.Keys(merged)) {
		facts := merged[name]
		if facts.hasBuild {
			out.BuildServices = append(out.BuildServices, name)
		} else if facts.image != "" && !seenImage[facts.image] {
			seenImage[facts.image] = true
			out.ExternalImages = append(out.ExternalImages, facts.image)
		}
		if facts.startPeriod > out.MaxStartPeriod {
			out.MaxStartPeriod = facts.startPeriod
		}
	}
	slices.Sort(out.ExternalImages)

	return out
}

// maxAliasHops bounds resolveAlias. The YAML spec forbids anchoring an alias
// node, so one hop is always enough; the bound only guarantees termination on
// a hand-built node graph.
const maxAliasHops = 8

// resolveAlias follows a YAML alias node (`container_name: *fixed`) to the
// anchored node it references. yaml.v3 resolves aliases transparently when
// decoding into a typed field, but a `yaml.Node` field receives the AliasNode
// itself — while compose, like every other YAML consumer, sees the anchored
// value. Every classifier below therefore dereferences first; skipping this
// would silently drop a real finding.
func resolveAlias(n yaml.Node) yaml.Node {
	for range maxAliasHops {
		if n.Kind != yaml.AliasNode || n.Alias == nil {
			break
		}
		n = *n.Alias
	}
	return n
}

// mergeTag classifies compose's `-f` merge tags (Compose v2.24+) on a field.
type mergeTag int

const (
	// mergeNone — an untagged declaration, merged by compose's normal rules.
	mergeNone mergeTag = iota
	// mergeReset — `!reset` DROPS the key from the merged result, whatever
	// earlier files declared and whatever value carries the tag.
	mergeReset
	// mergeOverride — `!override` REPLACES the merged value instead of merging
	// into it. For a scalar field that is indistinguishable from a plain
	// declaration; for a sequence (`ports:`) it cancels every earlier entry.
	mergeOverride
)

// resetTag / overrideTag are compose's merge tags. yaml.v3 leaves an unknown
// tag unresolved, so the node arrives with the tag intact and the raw scalar
// text as Value — which is why every field carrying one is decoded as a
// yaml.Node: `container_name: !reset null` decoded straight into a string
// would read as a service literally named "null".
const (
	resetTag    = "!reset"
	overrideTag = "!override"
)

// mergeTagOf reports which merge tag a field declaration carries. The alias is
// resolved first so an anchored declaration is classified by the node compose
// itself sees.
func mergeTagOf(n yaml.Node) mergeTag {
	switch resolveAlias(n).Tag {
	case resetTag:
		return mergeReset
	case overrideTag:
		return mergeOverride
	default:
		return mergeNone
	}
}

// nodeDeclared reports whether the key was present at all, whatever its value.
// An absent key decodes to the zero Node (Kind 0) and leaves whatever an
// earlier file in the chain declared in effect.
func nodeDeclared(n yaml.Node) bool { return n.Kind != 0 }

// scalarValue returns a scalar field's raw text, or "" for an absent key, an
// explicit null, or a non-scalar shape. Callers that need to distinguish
// "cleared" from "not declared here" must ask mergeTagOf/nodeDeclared first —
// a `!reset`-tagged scalar still carries its raw text here.
func scalarValue(n yaml.Node) string {
	n = resolveAlias(n)
	if n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return ""
	}
	return n.Value
}

// decodeHealthcheck reads the narrow healthcheck shape out of its node. A
// missing, `!reset`-tagged, or otherwise unreadable healthcheck reports false
// so the caller can tell "declared nothing" from "declared a disable".
func decodeHealthcheck(n yaml.Node) (composeScanHealthcheck, bool) {
	if n = resolveAlias(n); n.Kind != yaml.MappingNode {
		return composeScanHealthcheck{}, false
	}
	var hc composeScanHealthcheck
	if err := n.Decode(&hc); err != nil {
		return composeScanHealthcheck{}, false
	}
	return hc, true
}

// nodePresent reports whether a YAML node carries an actual value. An absent
// key decodes to the zero Node (Kind 0); an explicit `build:` with no value
// decodes to a null scalar — neither declares a build.
func nodePresent(n yaml.Node) bool {
	n = resolveAlias(n)
	if n.Kind == 0 {
		return false
	}
	if n.Kind == yaml.ScalarNode && (n.Tag == "!!null" || n.Value == "") {
		return false
	}
	return true
}

// parseStartPeriod parses a healthcheck start_period. A disabled healthcheck,
// an absent value, or one docker's duration grammar shares with Go but this
// parser cannot read yields (0, false) — the scanner stays advisory and never
// errors on a compose file docker itself accepts.
func parseStartPeriod(h composeScanHealthcheck) (time.Duration, bool) {
	if h.Disable || h.StartPeriod == "" {
		return 0, false
	}
	d, err := time.ParseDuration(strings.ReplaceAll(h.StartPeriod, " ", ""))
	if err != nil || d < 0 {
		return 0, false
	}
	return d, true
}

// scanComposeDoc walks a parsed compose document. Every map is iterated over
// a sorted key list, never ranged directly: findings flow into `dwe validate`
// diagnostics whose sort keys (severity/domain/target/file/line) are identical
// across findings from one file, so a random map order surfaces as a random
// diagnostic order in --output json between runs.
//
// The returned composeDocScan also carries what this file CLEARS or REPLACES:
// declarations that produce no finding of their own but must cancel ones an
// earlier file in the `-f` chain produced. See ScanComposeIsolation.
func scanComposeDoc(doc composeScanDoc, file string) composeDocScan {
	var scan composeDocScan

	for _, name := range slices.Sorted(maps.Keys(doc.Services)) {
		svc := doc.Services[name]
		switch state, value := classifyContainerName(svc.ContainerName); state {
		case containerNameSet:
			scan.findings = append(scan.findings, IsolationFinding{
				Kind:     KindContainerName,
				Resource: name,
				Value:    value,
				Blocking: true,
				Message: "service " + name + " sets container_name: " + value +
					" — bypasses compose project-name scoping and collides with any other" +
					" project/run using the same fixed name",
				File: file,
			})
		case containerNameCleared:
			scan.cleared = append(scan.cleared, declKey{KindContainerName, name})
		case containerNameAbsent:
		}

		// `ports:` is a sequence, which compose merges by APPENDING — so a
		// per-file entry stands unless this file replaces the whole list.
		// `!reset` drops the key outright (its own content, conventionally
		// null, never reaches the merged stack); `!override` replaces the
		// earlier entries with this file's.
		tag := mergeTagOf(svc.Ports)
		if tag == mergeReset || tag == mergeOverride {
			scan.replacedPorts = append(scan.replacedPorts, name)
		}
		if tag != mergeReset {
			scan.findings = append(scan.findings, scanPortEntries(name, svc.Ports, file)...)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(doc.Volumes)) {
		scan.addEntity(doc.Volumes[name], name, file, KindExternalVolume, KindNamedVolume)
	}
	for _, name := range slices.Sorted(maps.Keys(doc.Networks)) {
		scan.addEntity(doc.Networks[name], name, file, KindExternalNetwork, KindNamedNetwork)
	}

	return scan
}

// composeDocScan is one compose file's contribution to the merged scan.
type composeDocScan struct {
	findings []IsolationFinding
	// cleared lists last-wins declarations this file drops, cancelling any
	// finding an earlier file in the chain produced for the same key.
	cleared []declKey
	// replacedPorts lists the compose services whose `ports:` this file
	// replaces wholesale (`!reset` / `!override`), cancelling every earlier
	// file's port findings for that service.
	replacedPorts []string
}

func (s *composeDocScan) addEntity(e composeScanNamedEntity, name, file string, externalKind, namedKind IsolationKind) {
	findings, cleared := scanNamedEntity(e, name, file, externalKind, namedKind)
	s.findings = append(s.findings, findings...)
	s.cleared = append(s.cleared, cleared...)
}

// scanPortEntries scans one file's `ports:` list. A non-sequence (absent key,
// or a shape ports: cannot legally take) contributes nothing.
func scanPortEntries(service string, n yaml.Node, file string) []IsolationFinding {
	n = resolveAlias(n)
	if n.Kind != yaml.SequenceNode {
		return nil
	}
	var findings []IsolationFinding
	for _, entry := range n.Content {
		if f, ok := scanPortNode(service, *entry, file); ok {
			findings = append(findings, f)
		}
	}
	return findings
}

// containerNameState classifies one file's `container_name:` declaration for a
// compose service, as compose's `-f` merge sees it.
type containerNameState int

const (
	// containerNameAbsent — the key is missing (or explicitly null, which
	// compose does not treat as a reset): the merge keeps whatever an earlier
	// file set.
	containerNameAbsent containerNameState = iota
	// containerNameSet — a concrete value that overrides any earlier one.
	containerNameSet
	// containerNameCleared — compose's `!reset` tag, which DROPS the merged
	// value so the stack runs with no container_name at all.
	containerNameCleared
)

func classifyContainerName(n yaml.Node) (containerNameState, string) {
	if mergeTagOf(n) == mergeReset {
		return containerNameCleared, ""
	}
	// `!override foo` (the other compose merge tag) is a plain value as far as
	// scoping is concerned, so anything non-empty and non-null counts as set.
	if value := scalarValue(n); value != "" {
		return containerNameSet, value
	}
	// Absent (zero Node), a plain explicit null, or a shape container_name
	// cannot legally take.
	return containerNameAbsent, ""
}

// scanNamedEntity classifies one volume/network entry. Both `external:` and
// `name:` are scalar fields, so each is last-declaration-wins across the `-f`
// chain: a file that declares one non-effectively (`external: false`, a
// `!reset`) cancels an earlier file's finding rather than adding one, which is
// the second return value.
func scanNamedEntity(e composeScanNamedEntity, name, file string, externalKind, namedKind IsolationKind) ([]IsolationFinding, []declKey) {
	var findings []IsolationFinding
	var cleared []declKey

	switch {
	case !nodeDeclared(e.External):
		// Not mentioned here — an earlier file keeps the last word.
	case entityTruthy(e.External):
		findings = append(findings, IsolationFinding{
			Kind:     externalKind,
			Resource: name,
			Blocking: false,
			Message:  string(externalKind) + " " + name + " is declared external: — shared with other projects/runs, not scoped to this one",
			File:     file,
		})
	default:
		cleared = append(cleared, declKey{externalKind, name})
	}

	explicit := ""
	if mergeTagOf(e.Name) != mergeReset {
		explicit = scalarValue(e.Name)
	}
	switch {
	case !nodeDeclared(e.Name):
	case explicit != "":
		findings = append(findings, IsolationFinding{
			Kind:     namedKind,
			Resource: name,
			Blocking: false,
			Message:  string(namedKind) + " " + name + " sets an explicit name: " + explicit + " — bypasses compose project-name scoping",
			File:     file,
		})
	default:
		cleared = append(cleared, declKey{namedKind, name})
	}

	return findings, cleared
}

// entityTruthy reports whether an `external:` node is truthy: either the bool
// `true`, or a non-empty mapping (`external: { name: foo }`).
func entityTruthy(n yaml.Node) bool {
	switch n = resolveAlias(n); n.Kind {
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
	switch n = resolveAlias(n); n.Kind {
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
	n = resolveAlias(n)
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
