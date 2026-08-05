package config

import (
	"os"
	"path/filepath"
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

func TestScanComposeCost_UnreadableFileSkippedSilently(t *testing.T) {
	t.Parallel()
	cfg := &DweConfig{Compose: ComposeConfig{Base: "does-not-exist.yml"}}
	facts := ScanComposeCost(cfg, t.TempDir())
	require.Empty(t, facts.BuildServices)
	require.Empty(t, facts.ExternalImages)
	require.Zero(t, facts.MaxStartPeriod)
}
