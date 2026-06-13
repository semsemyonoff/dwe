package docker

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestServiceContainerPSArgs(t *testing.T) {
	cases := []struct {
		name          string
		runningOnly   bool
		excludeOneoff bool
		wantContains  []string
		wantAbsent    []string
	}{
		{
			name:        "running, exclude one-off",
			runningOnly: true, excludeOneoff: true,
			wantContains: []string{"status=running", "label=" + ComposeOneoffLabel + "=False"},
			wantAbsent:   []string{"--all"},
		},
		{
			name:        "running, include one-off (fallback)",
			runningOnly: true, excludeOneoff: false,
			wantContains: []string{"status=running"},
			wantAbsent:   []string{"label=" + ComposeOneoffLabel + "=False", "--all"},
		},
		{
			name:        "all, exclude one-off",
			runningOnly: false, excludeOneoff: true,
			wantContains: []string{"--all", "label=" + ComposeOneoffLabel + "=False"},
			wantAbsent:   []string{"status=running"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := serviceContainerPSArgs("dwe-shop", "app-main", tc.runningOnly, tc.excludeOneoff)
			// Always project + service label filters and the Names format.
			for _, want := range []string{
				"ps",
				"label=" + ComposeProjectLabel + "=dwe-shop",
				"label=" + ComposeServiceLabel + "=app-main",
				"{{.Names}}",
			} {
				if !slices.Contains(got, want) {
					t.Errorf("args missing %q: %v", want, got)
				}
			}
			for _, want := range tc.wantContains {
				if !slices.Contains(got, want) {
					t.Errorf("args missing %q: %v", want, got)
				}
			}
			for _, absent := range tc.wantAbsent {
				if slices.Contains(got, absent) {
					t.Errorf("args unexpectedly contains %q: %v", absent, got)
				}
			}
		})
	}
}

// withStubPSNames swaps the psNamesRunner seam and records every call's args.
func withStubPSNames(t *testing.T, fn func(args []string) (string, error)) *[][]string {
	t.Helper()
	prev := psNamesRunner
	var calls [][]string
	psNamesRunner = func(_ string, _ []string, args []string) (string, error) {
		calls = append(calls, args)
		return fn(args)
	}
	t.Cleanup(func() { psNamesRunner = prev })
	return &calls
}

func TestServiceContainerName_PrefersNonOneoff(t *testing.T) {
	// First (oneoff=False) query returns the real container — no fallback call.
	calls := withStubPSNames(t, func(args []string) (string, error) {
		if slices.Contains(args, "label="+ComposeOneoffLabel+"=False") {
			return "real-app", nil
		}
		return "oneoff-app", nil
	})
	name, err := ServiceContainerName("docker", nil, "dwe-shop", "app", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "real-app" {
		t.Errorf("name: got %q, want %q", name, "real-app")
	}
	if len(*calls) != 1 {
		t.Errorf("expected a single (non-fallback) query, got %d", len(*calls))
	}
}

func TestServiceContainerName_FallsBackWhenNoLabel(t *testing.T) {
	// The oneoff=False query finds nothing (e.g. podman omits the label); the
	// fallback (without the one-off filter) resolves the real container.
	calls := withStubPSNames(t, func(args []string) (string, error) {
		if slices.Contains(args, "label="+ComposeOneoffLabel+"=False") {
			return "", nil
		}
		return "podman-app", nil
	})
	name, err := ServiceContainerName("podman", nil, "dwe-shop", "app", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "podman-app" {
		t.Errorf("name: got %q, want %q (fallback)", name, "podman-app")
	}
	if len(*calls) != 2 {
		t.Errorf("expected prefer + fallback queries, got %d", len(*calls))
	}
}

func TestServiceContainerName_DockerErrorNoFallback(t *testing.T) {
	// A real docker error on the first query must surface immediately, not be
	// masked by the fallback.
	wantErr := errors.New("Cannot connect to the Docker daemon")
	calls := withStubPSNames(t, func([]string) (string, error) {
		return "", wantErr
	})
	_, err := ServiceContainerName("docker", nil, "dwe-shop", "app", true)
	if !errors.Is(err, wantErr) {
		t.Fatalf("want surfaced docker error, got %v", err)
	}
	if len(*calls) != 1 {
		t.Errorf("error must short-circuit the fallback; got %d calls", len(*calls))
	}
}

func TestServiceContainerName_EmptyIdentity(t *testing.T) {
	calls := withStubPSNames(t, func([]string) (string, error) { return "x", nil })
	for _, tc := range [][2]string{{"", "svc"}, {"proj", ""}} {
		name, err := ServiceContainerName("docker", nil, tc[0], tc[1], true)
		if err != nil || name != "" {
			t.Errorf("empty identity (%q,%q): got (%q,%v), want (\"\",nil)", tc[0], tc[1], name, err)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("empty identity must not query docker; got %d calls", len(*calls))
	}
}

// TestLookupServiceContainer_ThreadsEnvAndSearchesAllStates pins the two
// contracts the logs/stop/reset callers depend on: the caller's processEnv is
// passed through to the `docker ps` probe (so probe and action hit the same
// daemon), and the search covers all container states (--all), not just running.
func TestLookupServiceContainer_ThreadsEnvAndSearchesAllStates(t *testing.T) {
	prev := psNamesRunner
	t.Cleanup(func() { psNamesRunner = prev })
	var gotEnv, gotArgs []string
	psNamesRunner = func(_ string, processEnv []string, args []string) (string, error) {
		gotEnv, gotArgs = processEnv, args
		return "real-web", nil
	}
	env := []string{"DOCKER_HOST=tcp://remote:2375"}
	name, err := LookupServiceContainer("docker", env, "dwe-shop", "web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "real-web" {
		t.Errorf("name = %q, want real-web", name)
	}
	if !slices.Equal(gotEnv, env) {
		t.Errorf("processEnv not threaded to probe: got %v, want %v", gotEnv, env)
	}
	if !slices.Contains(gotArgs, "--all") {
		t.Errorf("expected --all (search every state) in args, got %v", gotArgs)
	}
}

func TestLookupServiceContainer_EmptyWhenNoMatch(t *testing.T) {
	prev := psNamesRunner
	t.Cleanup(func() { psNamesRunner = prev })
	psNamesRunner = func(_ string, _ []string, _ []string) (string, error) { return "", nil }
	name, err := LookupServiceContainer("docker", nil, "proj", "svc")
	if err != nil || name != "" {
		t.Fatalf(`got (%q,%v), want ("",nil)`, name, err)
	}
}

// guard against accidental arg-shape drift the stub tests above rely on.
func TestServiceContainerPSArgs_StableShape(t *testing.T) {
	got := serviceContainerPSArgs("p", "s", true, true)
	want := []string{
		"ps",
		"--filter", "label=" + ComposeProjectLabel + "=p",
		"--filter", "label=" + ComposeServiceLabel + "=s",
		"--filter", "status=running",
		"--filter", "label=" + ComposeOneoffLabel + "=False",
		"--format", "{{.Names}}",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("args shape drift:\n got: %v\nwant: %v", got, want)
	}
}
