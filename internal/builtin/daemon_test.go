package builtin

import (
	"slices"
	"strings"
	"testing"
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
	if !hasPair(args, "--label", "devbox.project=proj") {
		t.Fatalf("missing project label; args=%v", args)
	}
	if !hasPair(args, "--label", "devbox.daemon.id=services.main.queue") {
		t.Fatalf("missing daemon.id label; args=%v", args)
	}
	if !hasPair(args, "--label", `devbox.daemon.params={"name":"default"}`) {
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

func TestDaemonBuiltins_Validate(t *testing.T) {
	tests := []struct {
		name    string
		builtin string
		with    map[string]any
		wantErr string
	}{
		{"start missing service", "docker_daemon_start", map[string]any{"container_template": "x"}, "service required"},
		{"start missing template", "docker_daemon_start", map[string]any{"service": "app"}, "container_template required"},
		{"start ok", "docker_daemon_start", map[string]any{"service": "app", "container_template": "x"}, ""},
		{"logs missing template", "docker_daemon_logs", map[string]any{}, "container_template required"},
		{"logs ok", "docker_daemon_logs", map[string]any{"container_template": "x"}, ""},
		{"stop missing template", "docker_daemon_stop", map[string]any{}, "container_template required"},
		{"stop ok", "docker_daemon_stop", map[string]any{"container_template": "x"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.builtin, tc.with)
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

func TestDaemonBuiltins_Registered(t *testing.T) {
	for _, name := range []string{"docker_daemon_start", "docker_daemon_logs", "docker_daemon_stop"} {
		if _, ok := Get(name); !ok {
			t.Errorf("builtin %q not registered", name)
		}
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

// --- helpers ---

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
