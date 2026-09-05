package config

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// scanFixture builds a minimal DweConfig pointing compose.base at the given
// testdata fixture (absolute path, so projectRoot is irrelevant) and returns
// the scan findings.
func scanFixture(t *testing.T, filename string) []IsolationFinding {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "compose_scan", filename))
	require.NoError(t, err)
	cfg := &DweConfig{Compose: ComposeConfig{Base: abs}}
	return ScanComposeIsolation(cfg, t.TempDir())
}

func findByResource(findings []IsolationFinding, kind IsolationKind, resource string) (IsolationFinding, bool) {
	for _, f := range findings {
		if f.Kind == kind && f.Resource == resource {
			return f, true
		}
	}
	return IsolationFinding{}, false
}

func TestScanComposeIsolation_Ports(t *testing.T) {
	t.Parallel()
	findings := scanFixture(t, "ports.yml")

	tests := []struct {
		service      string
		wantFinding  bool
		wantHostPort int
		msgHas       string
	}{
		{service: "literal", wantFinding: true, wantHostPort: 8080},
		{service: "range", wantFinding: true, wantHostPort: 8080, msgHas: "8080-8090"},
		{service: "envvar", wantFinding: false},
		{service: "containeronly", wantFinding: false},
		{service: "withip", wantFinding: true, wantHostPort: 8080},
		{service: "proto", wantFinding: true, wantHostPort: 8080},
		{service: "ipv6", wantFinding: false},
		{service: "longform", wantFinding: true, wantHostPort: 8080},
		{service: "longformbare", wantFinding: true, wantHostPort: 8080},
	}

	for _, tc := range tests {
		t.Run(tc.service, func(t *testing.T) {
			t.Parallel()
			f, ok := findByResource(findings, KindRawHostPort, tc.service)
			if !tc.wantFinding {
				require.False(t, ok, "unexpected finding: %+v", f)
				return
			}
			require.True(t, ok, "expected a raw_host_port finding for %s", tc.service)
			require.True(t, f.Blocking)
			require.Equal(t, tc.wantHostPort, f.HostPort)
			if tc.msgHas != "" {
				require.Contains(t, f.Message, tc.msgHas)
			}
		})
	}
}

func TestScanComposeIsolation_ContainerName(t *testing.T) {
	t.Parallel()
	findings := scanFixture(t, "container_name.yml")
	f, ok := findByResource(findings, KindContainerName, "app")
	require.True(t, ok)
	require.True(t, f.Blocking)
	require.Contains(t, f.Message, "fixed-name")
}

func TestScanComposeIsolation_VolumesAndNetworks(t *testing.T) {
	t.Parallel()
	findings := scanFixture(t, "volumes_networks.yml")

	extVol, ok := findByResource(findings, KindExternalVolume, "ext")
	require.True(t, ok)
	require.False(t, extVol.Blocking)

	// external declared as a mapping (`external: { name: shared }`) is truthy too.
	extMapVol, ok := findByResource(findings, KindExternalVolume, "extmap")
	require.True(t, ok)
	require.False(t, extMapVol.Blocking)

	namedVol, ok := findByResource(findings, KindNamedVolume, "named")
	require.True(t, ok)
	require.False(t, namedVol.Blocking)
	require.Contains(t, namedVol.Message, "my-vol")

	extNet, ok := findByResource(findings, KindExternalNetwork, "extnet")
	require.True(t, ok)
	require.False(t, extNet.Blocking)

	namedNet, ok := findByResource(findings, KindNamedNetwork, "namednet")
	require.True(t, ok)
	require.False(t, namedNet.Blocking)
	require.Contains(t, namedNet.Message, "my-net")
}

func TestScanComposeIsolation_CleanProjectIsEmpty(t *testing.T) {
	t.Parallel()
	findings := scanFixture(t, "clean.yml")
	require.Empty(t, findings)
}

func TestScanComposeIsolation_UnreadableFileSkippedSilently(t *testing.T) {
	t.Parallel()
	cfg := &DweConfig{Compose: ComposeConfig{Base: "does-not-exist.yml"}}
	findings := ScanComposeIsolation(cfg, t.TempDir())
	require.Empty(t, findings)
}

func TestScanComposeIsolation_MalformedFileSkippedSilently(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bad := filepath.Join(root, "bad.yml")
	require.NoError(t, os.WriteFile(bad, []byte("services: [this is not a mapping\n"), 0o644))
	cfg := &DweConfig{Compose: ComposeConfig{Base: bad}}
	findings := ScanComposeIsolation(cfg, root)
	require.Empty(t, findings)
}

// TestScanComposeIsolation_DeterministicOrder pins the sorted-key iteration.
// Findings from one compose file are identical in every `dwe validate` sort key
// (severity/domain/target/file/line), so map-order iteration here surfaces as
// `--output json` diagnostics that flap between runs on an unchanged project.
// Every other assertion in this file is order-independent, so nothing else
// would catch a regression to `range doc.Services`.
func TestScanComposeIsolation_DeterministicOrder(t *testing.T) {
	t.Parallel()

	want := []struct {
		kind     IsolationKind
		resource string
	}{
		{KindContainerName, "alpha"},
		{KindContainerName, "mid"},
		{KindContainerName, "zeta"},
		{KindNamedVolume, "avol"},
		{KindNamedVolume, "zvol"},
		{KindNamedNetwork, "anet"},
		{KindNamedNetwork, "znet"},
	}

	// Repeated because Go randomizes map iteration per range statement: a
	// single pass can match by luck.
	for range 20 {
		findings := scanFixture(t, "ordering.yml")
		require.Len(t, findings, len(want))
		for i, w := range want {
			require.Equal(t, w.kind, findings[i].Kind, "finding %d kind", i)
			require.Equal(t, w.resource, findings[i].Resource, "finding %d resource", i)
		}
	}
}

// scanChain writes the given compose documents to a temp dir as a `-f` chain
// (first is compose.base, the rest are extras) and returns the scan findings.
func scanChain(t *testing.T, docs ...string) []IsolationFinding {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 0, len(docs))
	for i, doc := range docs {
		p := filepath.Join(root, "compose-"+strconv.Itoa(i)+".yml")
		require.NoError(t, os.WriteFile(p, []byte(doc), 0o644))
		paths = append(paths, p)
	}
	cfg := &DweConfig{Compose: ComposeConfig{Base: paths[0], Extra: paths[1:]}}
	return ScanComposeIsolation(cfg, root)
}

// TestScanComposeIsolation_ContainerNameLastWins pins the `-f` merge collapse:
// container_name is a scalar field, so only the last file that sets it takes
// effect. Reporting every declaration would flag a base value an overlay has
// already replaced — a container the merged stack never creates.
func TestScanComposeIsolation_ContainerNameLastWins(t *testing.T) {
	t.Parallel()
	findings := scanChain(t,
		"services:\n  app:\n    image: busybox\n    container_name: base-name\n",
		"services:\n  app:\n    container_name: overlay-name\n",
	)
	require.Len(t, findings, 1)
	require.Equal(t, KindContainerName, findings[0].Kind)
	require.Equal(t, "overlay-name", findings[0].Value)
}

// TestScanComposeIsolation_ContainerNameReset pins compose's `!reset` merge tag
// (Compose v2.24+): it DROPS the merged value, so the stack runs with no
// container_name and there is nothing left to collide. yaml.v3 leaves an
// unknown tag unresolved, so the node arrives with the raw scalar text as its
// value — decoding `!reset null` straight into a string would both keep the
// finding alive and report a container literally named "null".
func TestScanComposeIsolation_ContainerNameReset(t *testing.T) {
	t.Parallel()
	base := "services:\n  app:\n    image: busybox\n    container_name: base-name\n"
	for _, clear := range []string{
		"services:\n  app:\n    container_name: !reset null\n",
		"services:\n  app:\n    container_name: !reset ''\n",
	} {
		require.Empty(t, scanChain(t, base, clear), "clear=%q", clear)
	}

	// A value re-declared AFTER the reset is in effect again.
	findings := scanChain(t, base,
		"services:\n  app:\n    container_name: !reset null\n",
		"services:\n  app:\n    container_name: later-name\n",
	)
	require.Len(t, findings, 1)
	require.Equal(t, "later-name", findings[0].Value)
}

// TestScanComposeIsolation_ContainerNameOverrideTag pins the sibling merge tag:
// `!override` replaces the merged value rather than clearing it, so the tagged
// value is a perfectly ordinary container_name finding.
func TestScanComposeIsolation_ContainerNameOverrideTag(t *testing.T) {
	t.Parallel()
	findings := scanChain(t,
		"services:\n  app:\n    image: busybox\n    container_name: base-name\n",
		"services:\n  app:\n    container_name: !override overlay-name\n",
	)
	require.Len(t, findings, 1)
	require.Equal(t, "overlay-name", findings[0].Value)
}

// TestScanComposeIsolation_ContainerNameNullIsNotAReset pins that a plain
// explicit null is NOT compose's reset: `!reset` is the only documented way to
// drop a merged value, so an untagged null must leave the base finding standing
// rather than silently suppress a real collision.
func TestScanComposeIsolation_ContainerNameNullIsNotAReset(t *testing.T) {
	t.Parallel()
	findings := scanChain(t,
		"services:\n  app:\n    image: busybox\n    container_name: base-name\n",
		"services:\n  app:\n    container_name: null\n",
	)
	require.Len(t, findings, 1)
	require.Equal(t, "base-name", findings[0].Value)
}

// TestScanComposeIsolation_PortsAppendAcrossChain pins that `ports:` is NOT
// collapsed last-wins like the scalar fields: compose merges sequences by
// appending, so every file's literal host port really is published and every
// one of them is a finding.
func TestScanComposeIsolation_PortsAppendAcrossChain(t *testing.T) {
	t.Parallel()
	findings := scanChain(t,
		"services:\n  app:\n    image: busybox\n    ports:\n      - \"8080:80\"\n",
		"services:\n  app:\n    ports:\n      - \"9090:90\"\n",
	)
	require.Len(t, findings, 2)
	require.Equal(t, 8080, findings[0].HostPort)
	require.Equal(t, 9090, findings[1].HostPort)
}

// TestScanComposeIsolation_PortsResetAndOverride pins the two merge tags that
// DO cancel an earlier file's ports. Without this the scanner blocks
// `dwe test run` over a port the merged stack never publishes — which is
// exactly the overlay an author would write to fix the finding.
func TestScanComposeIsolation_PortsResetAndOverride(t *testing.T) {
	t.Parallel()
	base := "services:\n  app:\n    image: busybox\n    ports:\n      - \"8080:80\"\n"

	// `!reset` drops the key: nothing is published, so nothing is a finding.
	// Its own content never reaches the merged stack either.
	for _, clear := range []string{
		"services:\n  app:\n    ports: !reset null\n",
		"services:\n  app:\n    ports: !reset []\n",
		"services:\n  app:\n    ports: !reset\n      - \"7070:70\"\n",
	} {
		require.Empty(t, scanChain(t, base, clear), "clear=%q", clear)
	}

	// `!override []` replaces the merged list with an empty one.
	require.Empty(t, scanChain(t, base, "services:\n  app:\n    ports: !override []\n"))

	// `!override` with entries replaces — only the overriding file's ports
	// survive.
	findings := scanChain(t, base, "services:\n  app:\n    ports: !override\n      - \"9090:90\"\n")
	require.Len(t, findings, 1)
	require.Equal(t, 9090, findings[0].HostPort)

	// A plain re-declaration AFTER a reset is published again.
	findings = scanChain(t, base,
		"services:\n  app:\n    ports: !reset null\n",
		"services:\n  app:\n    ports:\n      - \"7070:70\"\n",
	)
	require.Len(t, findings, 1)
	require.Equal(t, 7070, findings[0].HostPort)
}

// TestScanComposeIsolation_PortsResetIsPerService pins that a reset cancels
// only the service it names — a sibling service's ports are untouched.
func TestScanComposeIsolation_PortsResetIsPerService(t *testing.T) {
	t.Parallel()
	findings := scanChain(t,
		"services:\n  app:\n    image: busybox\n    ports:\n      - \"8080:80\"\n  db:\n    image: postgres:16\n    ports:\n      - \"5432:5432\"\n",
		"services:\n  app:\n    ports: !reset null\n",
	)
	require.Len(t, findings, 1)
	require.Equal(t, "db", findings[0].Resource)
	require.Equal(t, 5432, findings[0].HostPort)
}

// TestScanComposeIsolation_NamedEntityLastWins pins the same collapse on the
// volume/network scalars: `external:` and `name:` are last-declaration-wins, so
// an overlay turning one off must cancel the base finding rather than leave a
// stale warning for a resource the merged stack scopes normally.
func TestScanComposeIsolation_NamedEntityLastWins(t *testing.T) {
	t.Parallel()
	base := "volumes:\n  data:\n    external: true\n    name: shared-vol\nnetworks:\n  net:\n    external: true\n"

	require.Empty(t, scanChain(t, base,
		"volumes:\n  data:\n    external: false\n    name: !reset null\nnetworks:\n  net:\n    external: !reset null\n",
	))

	// A re-declaration after the clear is in effect again, and reports the
	// overlay's value.
	findings := scanChain(t, base,
		"volumes:\n  data:\n    external: false\n    name: !reset null\nnetworks:\n  net:\n    external: !reset null\n",
		"volumes:\n  data:\n    name: later-vol\n",
	)
	require.Len(t, findings, 1)
	require.Equal(t, KindNamedVolume, findings[0].Kind)
	require.Contains(t, findings[0].Message, "later-vol")
}

// scanChainWithDocker is scanChain plus a workspace/docker.yml (skipped when
// dockerYML is empty), so the scan can resolve `shared: true` acknowledgements.
func scanChainWithDocker(t *testing.T, dockerYML string, docs ...string) []IsolationFinding {
	t.Helper()
	root := t.TempDir()
	if dockerYML != "" {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "workspace", "docker.yml"), []byte(dockerYML), 0o644))
	}
	paths := make([]string, 0, len(docs))
	for i, doc := range docs {
		p := filepath.Join(root, "compose-"+strconv.Itoa(i)+".yml")
		require.NoError(t, os.WriteFile(p, []byte(doc), 0o644))
		paths = append(paths, p)
	}
	cfg := &DweConfig{Compose: ComposeConfig{Base: paths[0], Extra: paths[1:]}}
	return ScanComposeIsolation(cfg, root)
}

// TestScanComposeIsolation_SharedVolumes pins the docker.yml acknowledgement:
// the documented cross-project cache recipe (a `shared: true` resources.volumes
// entry plus the matching raw compose declaration) is a deliberate choice dwe
// itself implements, so the scanner marks it rather than leaving the author a
// warning they can never clear. The match is on the volume's EFFECTIVE name —
// the surviving explicit `name:`, else the compose map key.
func TestScanComposeIsolation_SharedVolumes(t *testing.T) {
	t.Parallel()

	const sharedDocker = "resources:\n  volumes:\n    npm:\n      name: dwe_npm_cache\n      shared: true\n"

	tests := []struct {
		name       string
		dockerYML  string
		docs       []string
		kind       IsolationKind
		resource   string
		wantShared bool
	}{
		{
			name:       "explicit name matches",
			dockerYML:  sharedDocker,
			docs:       []string{"volumes:\n  npm_cache:\n    external: true\n    name: dwe_npm_cache\n"},
			kind:       KindNamedVolume,
			resource:   "npm_cache",
			wantShared: true,
		},
		{
			name:       "external finding of the same volume is marked too",
			dockerYML:  sharedDocker,
			docs:       []string{"volumes:\n  npm_cache:\n    external: true\n    name: dwe_npm_cache\n"},
			kind:       KindExternalVolume,
			resource:   "npm_cache",
			wantShared: true,
		},
		{
			name:       "map key matches when compose declares no name",
			dockerYML:  "resources:\n  volumes:\n    composer:\n      name: composer-cache\n      shared: true\n",
			docs:       []string{"volumes:\n  composer-cache:\n    external: true\n"},
			kind:       KindExternalVolume,
			resource:   "composer-cache",
			wantShared: true,
		},
		{
			name:      "explicit name mismatch",
			dockerYML: sharedDocker,
			docs:      []string{"volumes:\n  npm_cache:\n    external: true\n    name: other_cache\n"},
			kind:      KindExternalVolume,
			resource:  "npm_cache",
		},
		// Compose's deprecated long form names the same real volume, so it has
		// to be acknowledgeable too — otherwise a project spelling the cache
		// this way keeps a warning it can never clear.
		{
			name:       "legacy external long form matches",
			dockerYML:  sharedDocker,
			docs:       []string{"volumes:\n  npm_cache:\n    external:\n      name: dwe_npm_cache\n"},
			kind:       KindExternalVolume,
			resource:   "npm_cache",
			wantShared: true,
		},
		{
			name:      "legacy external long form mismatch",
			dockerYML: sharedDocker,
			docs:      []string{"volumes:\n  npm_cache:\n    external:\n      name: other_cache\n"},
			kind:      KindExternalVolume,
			resource:  "npm_cache",
		},
		// Compose lets a top-level name: override the long form; so must the
		// effective name the acknowledgement matches on.
		{
			name:      "top-level name outranks the long form",
			dockerYML: sharedDocker,
			docs:      []string{"volumes:\n  npm_cache:\n    external:\n      name: dwe_npm_cache\n    name: other_cache\n"},
			kind:      KindExternalVolume,
			resource:  "npm_cache",
		},
		{
			name:       "top-level name outranks the long form the other way",
			dockerYML:  sharedDocker,
			docs:       []string{"volumes:\n  npm_cache:\n    external:\n      name: other_cache\n    name: dwe_npm_cache\n"},
			kind:       KindExternalVolume,
			resource:   "npm_cache",
			wantShared: true,
		},
		// A bare `external: true` carries no name, so the map key stays the
		// effective name — the pre-existing fallback must survive.
		{
			name:       "long form absent falls back to the map key",
			dockerYML:  "resources:\n  volumes:\n    npm:\n      name: npm_cache\n      shared: true\n",
			docs:       []string{"volumes:\n  npm_cache:\n    external: true\n"},
			kind:       KindExternalVolume,
			resource:   "npm_cache",
			wantShared: true,
		},
		{
			name:      "networks are never acknowledged via the long form either",
			dockerYML: sharedDocker,
			docs:      []string{"networks:\n  npm_cache:\n    external:\n      name: dwe_npm_cache\n"},
			kind:      KindExternalNetwork,
			resource:  "npm_cache",
		},
		{
			name:      "shared false is not an acknowledgement",
			dockerYML: "resources:\n  volumes:\n    npm:\n      name: dwe_npm_cache\n      shared: false\n",
			docs:      []string{"volumes:\n  npm_cache:\n    external: true\n    name: dwe_npm_cache\n"},
			kind:      KindExternalVolume,
			resource:  "npm_cache",
		},
		{
			name:      "no docker.yml at all",
			dockerYML: "",
			docs:      []string{"volumes:\n  npm_cache:\n    external: true\n    name: dwe_npm_cache\n"},
			kind:      KindExternalVolume,
			resource:  "npm_cache",
		},
		{
			name:      "networks are never acknowledged",
			dockerYML: sharedDocker,
			docs:      []string{"networks:\n  npm_cache:\n    external: true\n    name: dwe_npm_cache\n"},
			kind:      KindExternalNetwork,
			resource:  "npm_cache",
		},
		{
			name:      "overlay reset of name falls back to the map key",
			dockerYML: sharedDocker,
			docs: []string{
				"volumes:\n  npm_cache:\n    external: true\n    name: dwe_npm_cache\n",
				"volumes:\n  npm_cache:\n    name: !reset null\n",
			},
			kind:     KindExternalVolume,
			resource: "npm_cache",
		},
		{
			name:      "overlay reset of name matches the map key",
			dockerYML: "resources:\n  volumes:\n    npm:\n      name: npm_cache\n      shared: true\n",
			docs: []string{
				"volumes:\n  npm_cache:\n    external: true\n    name: dwe_npm_cache\n",
				"volumes:\n  npm_cache:\n    name: !reset null\n",
			},
			kind:       KindExternalVolume,
			resource:   "npm_cache",
			wantShared: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			findings := scanChainWithDocker(t, tc.dockerYML, tc.docs...)
			f, ok := findByResource(findings, tc.kind, tc.resource)
			require.True(t, ok, "expected a %s finding for %s", tc.kind, tc.resource)
			require.Equal(t, tc.wantShared, f.Shared)
		})
	}
}

// TestScanComposeIsolation_SharedLeavesBlockingAndMessage pins that the
// acknowledgement is purely additive: Shared is a separate fact, so a caller
// that ignores it sees byte-identical output to before.
func TestScanComposeIsolation_SharedLeavesBlockingAndMessage(t *testing.T) {
	t.Parallel()

	const doc = "services:\n  app:\n    image: busybox\n    container_name: fixed-name\n" +
		"volumes:\n  npm_cache:\n    external: true\n    name: dwe_npm_cache\n"
	const dockerYML = "resources:\n  volumes:\n    npm:\n      name: dwe_npm_cache\n      shared: true\n"

	plain := scanChainWithDocker(t, "", doc)
	acknowledged := scanChainWithDocker(t, dockerYML, doc)
	require.Len(t, acknowledged, len(plain))

	for i, f := range acknowledged {
		require.Equal(t, plain[i].Kind, f.Kind)
		require.Equal(t, plain[i].Blocking, f.Blocking, "%s: Blocking must not depend on docker.yml", f.Kind)
		require.Equal(t, plain[i].Message, f.Message, "%s: Message must not depend on docker.yml", f.Kind)
	}

	// The blocking container_name finding is never acknowledged.
	container, ok := findByResource(acknowledged, KindContainerName, "app")
	require.True(t, ok)
	require.False(t, container.Shared)
}

// TestScanComposeIsolation_NamedEntityValue pins that the named_* finding
// carries its explicit name: in Value. markSharedVolumes reads it to derive the
// volume's effective name after the `-f` collapse.
func TestScanComposeIsolation_NamedEntityValue(t *testing.T) {
	t.Parallel()
	findings := scanChain(t, "volumes:\n  data:\n    name: my-vol\nnetworks:\n  net:\n    name: my-net\n")

	vol, ok := findByResource(findings, KindNamedVolume, "data")
	require.True(t, ok)
	require.Equal(t, "my-vol", vol.Value)

	net, ok := findByResource(findings, KindNamedNetwork, "net")
	require.True(t, ok)
	require.Equal(t, "my-net", net.Value)
}

// TestScanComposeIsolation_PartialDecodeKeepsFindings pins that one field this
// scanner's narrow shape cannot read does not blind it to the rest of the file:
// yaml.v3 reports a *yaml.TypeError after decoding everything else, and
// dropping the file would silently hide a blocking finding declared beside it.
func TestScanComposeIsolation_PartialDecodeKeepsFindings(t *testing.T) {
	t.Parallel()
	findings := scanChain(t,
		"services:\n  app:\n    image: busybox\n    container_name: fixed-name\nvolumes:\n  data: !reset null\n",
	)
	require.Len(t, findings, 1)
	require.Equal(t, KindContainerName, findings[0].Kind)
	require.Equal(t, "fixed-name", findings[0].Value)
}

// TestScanComposeIsolation_AliasNodes pins that a YAML alias is dereferenced
// before classification. yaml.v3 resolves aliases transparently into a typed
// field but hands a `yaml.Node` field the AliasNode itself, while compose sees
// the anchored value — so every construct scanned through a yaml.Node must
// follow the alias or the finding silently disappears.
func TestScanComposeIsolation_AliasNodes(t *testing.T) {
	t.Parallel()
	findings := scanChain(t, `
x-name: &name fixed-name
x-port: &port "8080:80"
x-published: &published 9090
x-true: &yes true
services:
  app:
    image: busybox
    container_name: *name
  ports-short:
    image: busybox
    ports:
      - *port
  ports-long:
    image: busybox
    ports:
      - target: 90
        published: *published
volumes:
  ext:
    external: *yes
`)

	cn, ok := findByResource(findings, KindContainerName, "app")
	require.True(t, ok, "alias-valued container_name must still be a finding")
	require.Equal(t, "fixed-name", cn.Value)
	require.True(t, cn.Blocking)

	short, ok := findByResource(findings, KindRawHostPort, "ports-short")
	require.True(t, ok, "alias-valued short port entry must still be a finding")
	require.Equal(t, 8080, short.HostPort)

	long, ok := findByResource(findings, KindRawHostPort, "ports-long")
	require.True(t, ok, "alias-valued long-form published must still be a finding")
	require.Equal(t, 9090, long.HostPort)

	_, ok = findByResource(findings, KindExternalVolume, "ext")
	require.True(t, ok, "alias-valued external: must still be a finding")
}

// TestScanComposeCost_AliasedBuild pins the same dereference on the cost
// scanner's build: probe — an aliased build block still marks the service as
// building, so it must not be reported as an image to pull.
func TestScanComposeCost_AliasedBuild(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := filepath.Join(root, "compose.yml")
	require.NoError(t, os.WriteFile(p, []byte(`
x-build: &build
  context: .
services:
  app:
    image: local/app:dev
    build: *build
`), 0o644))
	facts := ScanComposeCost(&DweConfig{Compose: ComposeConfig{Base: p}}, root)
	require.Equal(t, []string{"app"}, facts.BuildServices)
	require.Empty(t, facts.ExternalImages)
}

// costFixture builds a DweConfig whose compose chain is the given testdata
// fixtures, in order (absolute paths, so projectRoot is irrelevant).
func costFixture(t *testing.T, filenames ...string) ComposeCostFacts {
	t.Helper()
	require.NotEmpty(t, filenames)
	abs := make([]string, 0, len(filenames))
	for _, name := range filenames {
		p, err := filepath.Abs(filepath.Join("testdata", "compose_scan", name))
		require.NoError(t, err)
		abs = append(abs, p)
	}
	cfg := &DweConfig{Compose: ComposeConfig{Base: abs[0], Extra: abs[1:]}}
	return ScanComposeCost(cfg, t.TempDir())
}

func TestScanComposeCost_Facts(t *testing.T) {
	t.Parallel()
	facts := costFixture(t, "cost.yml")

	// `buildless` declares an empty build: — a null node is not a build.
	require.Equal(t, []string{"app", "worker"}, facts.BuildServices)
	// demo-app:dev is excluded (app builds it), redis:7 is deduped, and a
	// buildless service with no image contributes nothing.
	require.Equal(t, []string{"busybox", "postgres:16", "redis:7"}, facts.ExternalImages)
	// Max, not sum — and a disabled healthcheck / unparseable duration is ignored.
	require.Equal(t, 90*time.Second, facts.MaxStartPeriod)
}

// TestScanComposeCost_NoBuildNoHealthcheck uses the isolation suite's clean
// fixture: one pulled image, no build, no healthcheck.
func TestScanComposeCost_NoBuildNoHealthcheck(t *testing.T) {
	t.Parallel()
	facts := costFixture(t, "clean.yml")
	require.Empty(t, facts.BuildServices)
	require.Equal(t, []string{"busybox"}, facts.ExternalImages)
	require.Zero(t, facts.MaxStartPeriod)
}

// TestScanComposeCost_OverlayWins pins the per-service merge across the -f
// chain: a later file's image: wins, and a build: anywhere in the chain marks
// the service as building — matching how compose itself resolves the chain.
func TestScanComposeCost_OverlayWins(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	base := filepath.Join(root, "base.yml")
	overlay := filepath.Join(root, "overlay.yml")
	require.NoError(t, os.WriteFile(base, []byte("services:\n  app:\n    image: upstream:1\n  db:\n    image: postgres:15\n"), 0o644))
	require.NoError(t, os.WriteFile(overlay, []byte("services:\n  app:\n    build: .\n  db:\n    image: postgres:16\n"), 0o644))

	cfg := &DweConfig{Compose: ComposeConfig{Base: base, Extra: []string{overlay}}}
	facts := ScanComposeCost(cfg, root)

	require.Equal(t, []string{"app"}, facts.BuildServices)
	require.Equal(t, []string{"postgres:16"}, facts.ExternalImages)
}

// TestScanComposeCost_OverlayDisablingHealthcheckClearsStartPeriod pins the
// clearing half of the merge: a later file disabling the healthcheck removes
// the earlier start_period instead of leaving it standing, so the reported
// wait matches what the merged stack actually performs.
func TestScanComposeCost_OverlayDisablingHealthcheckClearsStartPeriod(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	base := filepath.Join(root, "base.yml")
	overlay := filepath.Join(root, "overlay.yml")
	require.NoError(t, os.WriteFile(base, []byte("services:\n  app:\n    image: nginx:1\n    healthcheck:\n      start_period: 300s\n  db:\n    image: postgres:16\n    healthcheck:\n      start_period: 20s\n"), 0o644))
	require.NoError(t, os.WriteFile(overlay, []byte("services:\n  app:\n    healthcheck:\n      disable: true\n"), 0o644))

	cfg := &DweConfig{Compose: ComposeConfig{Base: base, Extra: []string{overlay}}}
	facts := ScanComposeCost(cfg, root)

	require.Equal(t, 20*time.Second, facts.MaxStartPeriod)
}

// costChain writes the given compose documents to a temp dir as a `-f` chain
// and returns the cost facts.
func costChain(t *testing.T, docs ...string) ComposeCostFacts {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 0, len(docs))
	for i, doc := range docs {
		p := filepath.Join(root, "compose-"+strconv.Itoa(i)+".yml")
		require.NoError(t, os.WriteFile(p, []byte(doc), 0o644))
		paths = append(paths, p)
	}
	cfg := &DweConfig{Compose: ComposeConfig{Base: paths[0], Extra: paths[1:]}}
	return ScanComposeCost(cfg, root)
}

// TestScanComposeCost_OverlayResetClearsBuild pins the clearing half of the
// build merge: `build:` is sticky across the chain only until a later file
// drops it with `!reset`. Leaving it sticky would report a build the merged
// stack never runs AND suppress the image it actually pulls.
func TestScanComposeCost_OverlayResetClearsBuild(t *testing.T) {
	t.Parallel()
	facts := costChain(t,
		"services:\n  app:\n    image: busybox\n    build:\n      context: .\n",
		"services:\n  app:\n    build: !reset null\n",
	)
	require.Empty(t, facts.BuildServices)
	require.Equal(t, []string{"busybox"}, facts.ExternalImages)
}

// TestScanComposeCost_OverlayResetClearsImage pins the same on `image:`. The
// value must not be read as a plain string either — yaml.v3 leaves the unknown
// tag unresolved, so `!reset null` would report an image literally named "null".
func TestScanComposeCost_OverlayResetClearsImage(t *testing.T) {
	t.Parallel()
	facts := costChain(t,
		"services:\n  app:\n    image: busybox\n",
		"services:\n  app:\n    image: !reset null\n",
	)
	require.Empty(t, facts.BuildServices)
	require.Empty(t, facts.ExternalImages)
}

// TestScanComposeCost_OverlayResetClearsHealthcheck pins the reset sibling of
// the `disable: true` clear already covered above.
func TestScanComposeCost_OverlayResetClearsHealthcheck(t *testing.T) {
	t.Parallel()
	facts := costChain(t,
		"services:\n  app:\n    image: nginx:1\n    healthcheck:\n      start_period: 300s\n",
		"services:\n  app:\n    healthcheck: !reset null\n",
	)
	require.Zero(t, facts.MaxStartPeriod)
}

// TestScanComposeCost_OverrideTagIsAnOrdinaryDeclaration pins that `!override`
// is NOT a clear: it replaces the merged value, which for these scalar fields
// is what a plain declaration already does.
func TestScanComposeCost_OverrideTagIsAnOrdinaryDeclaration(t *testing.T) {
	t.Parallel()
	facts := costChain(t,
		"services:\n  app:\n    image: busybox\n",
		"services:\n  app:\n    image: !override alpine:3\n    build: !override\n      context: .\n",
	)
	require.Equal(t, []string{"app"}, facts.BuildServices)
	require.Empty(t, facts.ExternalImages)
}

func TestScanComposeCost_UnreadableFileSkippedSilently(t *testing.T) {
	t.Parallel()
	cfg := &DweConfig{Compose: ComposeConfig{Base: "does-not-exist.yml"}}
	facts := ScanComposeCost(cfg, t.TempDir())
	require.Empty(t, facts.BuildServices)
	require.Empty(t, facts.ExternalImages)
	require.Zero(t, facts.MaxStartPeriod)
}
