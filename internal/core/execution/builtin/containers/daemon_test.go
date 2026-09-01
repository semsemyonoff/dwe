package containers

import (
	"slices"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

func TestBuildStartExtraArgs_orderAndFlags(t *testing.T) {
	args := buildStartExtraArgs(startArgsInput{
		FullName:    "proj-php_queue_default",
		Service:     "app-main",
		User:        "www-data",
		Workdir:     "/var/www",
		AutoRemove:  true,
		Argv:        []string{"php", "artisan", "queue:listen"},
		ComposeArgs: []string{"--pull", "never"},
		EnvKeys:     []string{"DB_PASS", "QUEUE_CONNECTION"},
		ProjectFull: "proj",
		DaemonID:    "services.main.queue",
		LabelParams: map[string]any{"name": "default"},
	})

	// Header flags present, in order:
	mustContainOrdered(t, args, "-d", "--no-deps", "--entrypoint", "", "--rm", "--name", "proj-php_queue_default")

	// Workdir flag present.
	if !hasPair(args, "--workdir", "/var/www") {
		t.Fatalf("--workdir /var/www missing; args=%v", args)
	}

	// compose_args must appear BEFORE --user/--workdir/-e/--label/service/argv.
	pullIdx := slices.Index(args, "--pull")
	userIdx := slices.Index(args, "--user")
	if pullIdx < 0 || userIdx < 0 || pullIdx > userIdx {
		t.Fatalf("compose_args must precede --user; args=%v", args)
	}

	// -e KEY argv pattern — no value baked into argv.
	for i, a := range args {
		if a == "-e" {
			if i+1 >= len(args) {
				t.Fatalf("dangling -e flag: %v", args)
			}
			if strings.Contains(args[i+1], "=") {
				t.Fatalf("env value leaked into argv at index %d: %q", i+1, args[i+1])
			}
		}
	}

	// Label triple present.
	if !hasPair(args, "--label", "dwe.project=proj") {
		t.Fatalf("missing project label; args=%v", args)
	}
	if !hasPair(args, "--label", "dwe.daemon.id=services.main.queue") {
		t.Fatalf("missing daemon.id label; args=%v", args)
	}
	if !hasPair(args, "--label", `dwe.daemon.params={"name":"default"}`) {
		t.Fatalf("missing daemon.params label; args=%v", args)
	}

	// Service and argv at the tail.
	if args[len(args)-4] != "app-main" {
		t.Fatalf("expected service before argv, got tail=%v", args[len(args)-5:])
	}
	if !slices.Equal(args[len(args)-3:], []string{"php", "artisan", "queue:listen"}) {
		t.Fatalf("argv tail wrong; got=%v", args[len(args)-3:])
	}
}

func TestBuildStartExtraArgs_noAutoRemove(t *testing.T) {
	args := buildStartExtraArgs(startArgsInput{
		FullName:    "proj-x",
		Service:     "app",
		AutoRemove:  false,
		ProjectFull: "proj",
		DaemonID:    "x",
	})
	for _, a := range args {
		if a == "--rm" {
			t.Fatalf("--rm should be absent when AutoRemove=false; args=%v", args)
		}
	}
}

func TestBuildStartExtraArgs_userModes(t *testing.T) {
	tests := []struct {
		mode     string
		wantFlag string
		wantVal  string
		absent   bool
	}{
		{"root", "--user", "root", false},
		{"www-data", "--user", "www-data", false},
		{"", "", "", true},
		{"internal", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			args := buildStartExtraArgs(startArgsInput{
				FullName:    "proj-x",
				Service:     "app",
				User:        tc.mode,
				ProjectFull: "proj",
			})
			has := false
			for i, a := range args {
				if a == "--user" {
					has = true
					if !tc.absent && args[i+1] != tc.wantVal {
						t.Fatalf("--user got %q want %q", args[i+1], tc.wantVal)
					}
				}
			}
			if tc.absent && has {
				t.Fatalf("did not expect --user; args=%v", args)
			}
			if !tc.absent && !has {
				t.Fatalf("expected --user; args=%v", args)
			}
		})
	}
}

func TestBuildStartExtraArgs_noDepsEntrypointUnconditional(t *testing.T) {
	args := buildStartExtraArgs(startArgsInput{
		FullName:    "proj-x",
		Service:     "app",
		ProjectFull: "proj",
	})
	if !hasInOrder(args, "-d", "--no-deps", "--entrypoint", "") {
		t.Fatalf("missing -d/--no-deps/--entrypoint header; args=%v", args)
	}
}

func TestBuildStartExtraArgs_secretValueNeverInArgv(t *testing.T) {
	args := buildStartExtraArgs(startArgsInput{
		FullName:    "proj-x",
		Service:     "app",
		EnvKeys:     []string{"DB_PASSWORD"},
		ProjectFull: "proj",
	})
	for _, a := range args {
		if strings.Contains(a, "PASSWORD=") || strings.Contains(a, "secret") {
			t.Fatalf("env value leaked into argv: %q", a)
		}
	}
}

// TestResolveDaemonWorkdirUser_workdirChain walks every rung of the workdir
// chain the daemon now shares with the service runners, including the `internal`
// opt-out sentinel, the container-differs-from-map-key lookup, and workdir_from
// beating a literal workdir.
func TestResolveDaemonWorkdirUser_workdirChain(t *testing.T) {
	rawDirInternal := map[string]any{
		"services": map[string]any{
			"main": map[string]any{"dir_internal": "/from/config"},
		},
	}

	tests := []struct {
		name        string
		workdir     string
		workdirFrom string
		services    map[string]config.ServiceConfig
		raw         map[string]any
		want        string // "" means: no --workdir flag at all
	}{
		{
			name:    "sentinel skips the fallback",
			workdir: "internal",
			services: map[string]config.ServiceConfig{
				"main": {Container: "app-main", CLI: config.ServiceCLIConfig{WorkDir: "/cli"}},
			},
		},
		{
			name:        "sentinel outranks workdir_from",
			workdir:     "internal",
			workdirFrom: "services.main.dir_internal",
			raw:         rawDirInternal,
		},
		{
			name:        "workdir_from beats the literal",
			workdir:     "/literal",
			workdirFrom: "services.main.dir_internal",
			raw:         rawDirInternal,
			want:        "/from/config",
		},
		{
			name:        "workdir_from resolving to nil falls through to the literal",
			workdir:     "/literal",
			workdirFrom: "services.missing.dir_internal",
			raw:         rawDirInternal,
			want:        "/literal",
		},
		{
			name:        "workdir_from resolving to nil falls through to the service fallback",
			workdirFrom: "services.missing.dir_internal",
			raw:         rawDirInternal,
			services: map[string]config.ServiceConfig{
				"main": {Container: "app-main", DirInternal: "/dir"},
			},
			want: "/dir",
		},
		{
			name:    "literal beats cli.workdir",
			workdir: "/literal",
			services: map[string]config.ServiceConfig{
				"main": {Container: "app-main", CLI: config.ServiceCLIConfig{WorkDir: "/cli"}},
			},
			want: "/literal",
		},
		{
			name: "cli.workdir beats work_dir_internal",
			services: map[string]config.ServiceConfig{
				"main": {
					Container:       "app-main",
					CLI:             config.ServiceCLIConfig{WorkDir: "/cli"},
					WorkDirInternal: "/work",
					DirInternal:     "/dir",
				},
			},
			want: "/cli",
		},
		{
			name: "work_dir_internal without cli.workdir",
			services: map[string]config.ServiceConfig{
				"main": {Container: "app-main", WorkDirInternal: "/work", DirInternal: "/dir"},
			},
			want: "/work",
		},
		{
			name: "dir_internal is the last rung",
			services: map[string]config.ServiceConfig{
				"main": {Container: "app-main", DirInternal: "/dir"},
			},
			want: "/dir",
		},
		{
			name: "container differs from the services map key",
			services: map[string]config.ServiceConfig{
				"other": {Container: "app-other", DirInternal: "/other"},
				"main":  {Container: "app-main", DirInternal: "/dir"},
			},
			want: "/dir",
		},
		{
			name: "no service entry leaves the image WORKDIR alone",
			services: map[string]config.ServiceConfig{
				"other": {Container: "app-other", DirInternal: "/other"},
			},
		},
		{
			name: "nothing declared anywhere",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.DweConfig{Services: tt.services, Raw: tt.raw}
			gotWorkdir, _, err := resolveDaemonWorkdirUser(cfg, "app-main", "", tt.workdir, tt.workdirFrom)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotWorkdir != tt.want {
				t.Fatalf("workdir = %q, want %q", gotWorkdir, tt.want)
			}

			args := buildStartExtraArgs(startArgsInput{
				FullName:    "proj-x",
				Service:     "app-main",
				Workdir:     gotWorkdir,
				ProjectFull: "proj",
			})
			if tt.want == "" {
				if slices.Contains(args, "--workdir") {
					t.Fatalf("expected no --workdir flag, got: %v", args)
				}
				return
			}
			if !hasPair(args, "--workdir", tt.want) {
				t.Fatalf("expected '--workdir %s', got: %v", tt.want, args)
			}
		})
	}
}

// TestResolveDaemonWorkdirUser_cliUserFallback pins the daemon's user chain:
// an explicit user: wins, "internal" suppresses both the flag and the fallback,
// and services.<svc>.cli.user fills in otherwise.
func TestResolveDaemonWorkdirUser_cliUserFallback(t *testing.T) {
	services := map[string]config.ServiceConfig{
		"other": {Container: "app-other", CLI: config.ServiceCLIConfig{User: "nobody"}},
		"main":  {Container: "app-main", CLI: config.ServiceCLIConfig{User: "www-data"}},
	}

	tests := []struct {
		name string
		user string
		want string
	}{
		{"cli.user fills in when the daemon declares none", "", "www-data"},
		{"explicit user beats cli.user", "root", "root"},
		{"internal suppresses cli.user", "internal", "internal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.DweConfig{Services: services}
			_, gotUser, err := resolveDaemonWorkdirUser(cfg, "app-main", tt.user, "", "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotUser != tt.want {
				t.Fatalf("user = %q, want %q", gotUser, tt.want)
			}
		})
	}

	t.Run("no service entry leaves the image USER alone", func(t *testing.T) {
		cfg := &config.DweConfig{Services: map[string]config.ServiceConfig{
			"other": {Container: "app-other", CLI: config.ServiceCLIConfig{User: "nobody"}},
		}}
		_, gotUser, err := resolveDaemonWorkdirUser(cfg, "app-main", "", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotUser != "" {
			t.Fatalf("user = %q, want empty", gotUser)
		}
		args := buildStartExtraArgs(startArgsInput{FullName: "proj-x", Service: "app-main", User: gotUser, ProjectFull: "proj"})
		if slices.Contains(args, "--user") {
			t.Fatalf("expected no --user flag, got: %v", args)
		}
	})

	// A cli.user of "current" travels through the fallback and is expanded by
	// buildStartExtraArgs into the ${host.uid}:${host.gid} pair — never the host
	// shell's `id -u`, which differs from what dwe exports into containers.
	t.Run("cli.user current expands to the host uid:gid pair", func(t *testing.T) {
		cfg := &config.DweConfig{Services: map[string]config.ServiceConfig{
			"main": {Container: "app-main", CLI: config.ServiceCLIConfig{User: "current"}},
		}}
		_, gotUser, err := resolveDaemonWorkdirUser(cfg, "app-main", "", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotUser != "current" {
			t.Fatalf("user = %q, want %q", gotUser, "current")
		}
		args := buildStartExtraArgs(startArgsInput{FullName: "proj-x", Service: "app-main", User: gotUser, ProjectFull: "proj"})
		h := tpl.CurrentHostInfo()
		if !hasPair(args, "--user", h.UID+":"+h.GID) {
			t.Fatalf("expected '--user %s:%s', got: %v", h.UID, h.GID, args)
		}
	})
}

// TestResolveDaemonWorkdirUser_workdirFromNonString pins that a dot-path
// pointing at a non-string value still fails the step loudly — only a nil
// (absent) value falls through.
// TestResolveDaemonWorkdirUser_workdirFromNonString pins the DIAGNOSTIC, not
// the local type assertion: config.LookupDotPath already rejects a non-string
// value, so the `resolved value is not a string` branch below it is
// unreachable today and deleting it leaves this test green. What the test does
// guard is that a non-string dot-path still surfaces as a workdir_from error
// rather than being swallowed into an empty workdir — which is what would
// happen if the wrap were dropped or LookupDotPath were ever loosened.
func TestResolveDaemonWorkdirUser_workdirFromNonString(t *testing.T) {
	cfg := &config.DweConfig{Raw: map[string]any{
		"services": map[string]any{"main": map[string]any{"ports": map[string]any{"http": 8080}}},
	}}
	_, _, err := resolveDaemonWorkdirUser(cfg, "app-main", "", "", "services.main.ports")
	if err == nil || !strings.Contains(err.Error(), "workdir_from") {
		t.Fatalf("got err=%v, want a workdir_from diagnostic", err)
	}
}

func TestDaemonBuiltins_Validate(t *testing.T) {
	tests := []struct {
		name string
		impl interface {
			Validate(map[string]any) error
		}
		with    map[string]any
		wantErr string
	}{
		{"start missing service", DaemonStart{}, map[string]any{"container_template": "x"}, "service required"},
		{"start missing template", DaemonStart{}, map[string]any{"service": "app"}, "container_template required"},
		{"start ok", DaemonStart{}, map[string]any{"service": "app", "container_template": "x"}, ""},
		{"logs missing template", DaemonLogs{}, map[string]any{}, "container_template required"},
		{"logs ok", DaemonLogs{}, map[string]any{"container_template": "x"}, ""},
		{"stop missing template", DaemonStop{}, map[string]any{}, "container_template required"},
		{"stop ok", DaemonStop{}, map[string]any{"container_template": "x"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.impl.Validate(tc.with)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got err=%v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestStopTimeoutSeconds(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", 10}, // default
		{"5s", 5},
		{"500ms", 1}, // round up from sub-second
		{"400ms", 1}, // round to 0s → clamp to 1
		{"1m30s", 90},
		{"-5s", 10},     // negative falls back to default
		{"garbage", 10}, // unparseable falls back
		{"0s", 10},      // zero falls back
	}
	for _, tc := range tests {
		got := stopTimeoutSeconds(tc.in)
		if got != tc.want {
			t.Errorf("stopTimeoutSeconds(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func mustContainOrdered(t *testing.T, args []string, want ...string) {
	t.Helper()
	if !hasInOrder(args, want...) {
		t.Fatalf("args missing expected ordered sequence %v; got=%v", want, args)
	}
}

func hasInOrder(args []string, want ...string) bool {
	i := 0
	for _, a := range args {
		if i < len(want) && a == want[i] {
			i++
		}
	}
	return i == len(want)
}

func hasPair(args []string, k, v string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == k && args[i+1] == v {
			return true
		}
	}
	return false
}
