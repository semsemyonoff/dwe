package config

import (
	"os"
	"path/filepath"
	"testing"

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
