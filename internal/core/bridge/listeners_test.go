package bridge

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/bridgeclient"
)

func TestParseBindOverride(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single", "127.0.0.1", []string{"127.0.0.1"}},
		{"comma", "127.0.0.1,10.0.0.1", []string{"127.0.0.1", "10.0.0.1"}},
		{"comma with spaces", " 127.0.0.1 , 10.0.0.1 ", []string{"127.0.0.1", "10.0.0.1"}},
		{"whitespace separated", "127.0.0.1 10.0.0.1\t172.17.0.1", []string{"127.0.0.1", "10.0.0.1", "172.17.0.1"}},
		{"empty entries dropped", ",,127.0.0.1,,", []string{"127.0.0.1"}},
		{"only separators", " ,, \t", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseBindOverride(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("ParseBindOverride(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestStart_WritesPortAndTokenFiles(t *testing.T) {
	rec := &launchRecorder{}
	d := startDaemon(t, fakeLauncher(rec, exitScript(0)), nil)

	portRaw, err := os.ReadFile(d.PortPath())
	if err != nil {
		t.Fatalf("port file not written: %v", err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(portRaw)))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("port file content %q is not a valid port", portRaw)
	}
	if port != d.Port() {
		t.Errorf("port file = %d, want bound port %d", port, d.Port())
	}
	portInfo, err := os.Stat(d.PortPath())
	if err != nil {
		t.Fatalf("stat port file: %v", err)
	}
	// The port file is written atomically (temp + rename); the temp file's
	// chmod must survive the rename as the public 0644 (it is not a secret).
	if mode := portInfo.Mode().Perm(); mode != 0o644 {
		t.Errorf("port file mode = %o, want 0644", mode)
	}

	tokenInfo, err := os.Stat(d.TokenPath())
	if err != nil {
		t.Fatalf("token file not written: %v", err)
	}
	if mode := tokenInfo.Mode().Perm(); mode != 0o600 {
		t.Errorf("token file mode = %o, want 0600", mode)
	}

	sockInfo, err := os.Stat(d.SocketPath())
	if err != nil {
		t.Fatalf("host.sock not created: %v", err)
	}
	if sockInfo.Mode()&os.ModeSocket == 0 {
		t.Error("host.sock is not a socket")
	}
	if mode := sockInfo.Mode().Perm(); mode != 0o660 {
		t.Errorf("host.sock mode = %o, want 0660", mode)
	}

	dirInfo, err := os.Stat(d.BridgeDir())
	if err != nil {
		t.Fatalf("bridge dir missing: %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("bridge dir mode = %o, want 0700", mode)
	}
}

func TestCloseRemovesEndpointsKeepsToken(t *testing.T) {
	rec := &launchRecorder{}
	d := startDaemon(t, fakeLauncher(rec, exitScript(0)), nil)
	tokenBefore, err := os.ReadFile(d.TokenPath())
	if err != nil {
		t.Fatalf("reading token: %v", err)
	}

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(d.SocketPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("host.sock still present after Close (err = %v)", err)
	}
	if _, err := os.Stat(d.PortPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("port file still present after Close (err = %v)", err)
	}

	// Restart in the same bridge dir: the token is the stable project
	// identity (D6) and must survive; the endpoints come back fresh.
	d2 := New(Config{
		ProjectRoot: d.cfg.ProjectRoot,
		BridgeDir:   d.cfg.BridgeDir,
		Launch:      fakeLauncher(rec, exitScript(0)),
	})
	if err := d2.Start(); err != nil {
		t.Fatalf("restart Start: %v", err)
	}
	t.Cleanup(func() { _ = d2.Close() })

	tokenAfter, err := os.ReadFile(d2.TokenPath())
	if err != nil {
		t.Fatalf("reading token after restart: %v", err)
	}
	if !bytes.Equal(tokenBefore, tokenAfter) {
		t.Error("token changed across daemon restart; must stay stable")
	}
	if _, err := os.Stat(d2.PortPath()); err != nil {
		t.Errorf("port file not rewritten after restart: %v", err)
	}
}

func TestStart_BindOverride(t *testing.T) {
	rec := &launchRecorder{}
	// The second address is TEST-NET-1 (RFC 5737), unbindable on any host:
	// extra binds are best-effort and must not fail Start (design D3).
	d := startDaemon(t, fakeLauncher(rec, exitScript(7)), func(cfg *Config) {
		cfg.BindOverride = "127.0.0.1,192.0.2.1"
	})

	var stdout, stderr bytes.Buffer
	if code := bridgeclient.Run(clientOptions(d, tcpClientDir(t, d), &stdout, &stderr)); code != 7 {
		t.Errorf("Run = %d, want 7 over the override bind (stderr: %q)", code, stderr.String())
	}
}

func TestStart_GatewayResolverBestEffort(t *testing.T) {
	rec := &launchRecorder{}

	t.Run("resolver error is skipped", func(t *testing.T) {
		var logged []string
		d := startDaemon(t, fakeLauncher(rec, exitScript(0)), func(cfg *Config) {
			cfg.GatewayIP = func() (string, error) { return "", errors.New("docker not running") }
			cfg.Logf = func(format string, args ...any) {
				logged = append(logged, format)
			}
		})
		if d.Port() == 0 {
			t.Error("primary bind missing despite failing gateway resolver")
		}
		if len(logged) == 0 {
			t.Error("failing gateway resolver should be logged")
		}
	})

	t.Run("unbindable gateway is skipped", func(t *testing.T) {
		d := startDaemon(t, fakeLauncher(rec, exitScript(0)), func(cfg *Config) {
			cfg.GatewayIP = func() (string, error) { return "192.0.2.1", nil }
		})
		var stdout, stderr bytes.Buffer
		if code := bridgeclient.Run(clientOptions(d, tcpClientDir(t, d), &stdout, &stderr)); code != 0 {
			t.Errorf("Run = %d, want 0 (stderr: %q)", code, stderr.String())
		}
	})
}

func TestStart_RemovesStaleSocket(t *testing.T) {
	rec := &launchRecorder{}
	root := shortTempDir(t)
	bridgeDir := filepath.Join(root, ".dwe", "bridge")
	if err := os.MkdirAll(bridgeDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// A dead daemon's leftover socket file: bind must succeed anyway
	// (single-instance is the ensure flock's job, not this package's).
	if err := os.WriteFile(filepath.Join(bridgeDir, "host.sock"), nil, 0o660); err != nil {
		t.Fatalf("planting stale socket: %v", err)
	}

	d := New(Config{
		ProjectRoot: root,
		BridgeDir:   bridgeDir,
		Launch:      fakeLauncher(rec, exitScript(0)),
	})
	if err := d.Start(); err != nil {
		t.Fatalf("Start over a stale socket: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
}

func TestStart_RelativeProjectRootRejected(t *testing.T) {
	d := New(Config{ProjectRoot: "relative/path"})
	if err := d.Start(); err == nil || !strings.Contains(err.Error(), "absolute") {
		_ = d.Close()
		t.Errorf("Start with relative root err = %v, want absolute-path error", err)
	}
}
