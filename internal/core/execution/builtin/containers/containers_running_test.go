package containers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

func TestContainersRunning_Validate(t *testing.T) {
	cases := []struct {
		name    string
		with    map[string]any
		wantErr string
	}{
		{"absent", nil, ""},
		{"null", map[string]any{"services": nil}, ""},
		{"empty", map[string]any{"services": []any{}}, ""},
		{"non-empty", map[string]any{"services": []any{"app", "db"}}, ""},
		{"empty-string element", map[string]any{"services": []any{"app", ""}}, "empty string"},
		{"non-string element", map[string]any{"services": []any{1}}, "expected string"},
		{"unknown key", map[string]any{"services": []any{"app"}, "timeout": "5s"}, `unknown key "timeout"`},
		{"unknown key in whole-project mode", map[string]any{"timeout": "5s"}, `unknown key "timeout"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ContainersRunning{}.Validate(tc.with)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestContainersRunning_Describe(t *testing.T) {
	cases := map[string]map[string]any{
		"check that every compose container is running (or exited 0)": nil,
		`check that service "app" is running`:                         {"services": []any{"app"}},
		"check that 2 services are running":                           {"services": []any{"app", "db"}},
	}
	for want, with := range cases {
		if got := (ContainersRunning{}).Describe(with); got != want {
			t.Errorf("Describe(%v) = %q, want %q", with, got, want)
		}
	}
}

// fakeContainer is one container as the fake daemon sees it. oneoff is the
// com.docker.compose.oneoff label value; "" means the backend omits the label.
// state is "running", "created", "restarting" or "exited<code>".
type fakeContainer struct {
	name, service, oneoff, state string
}

// fakeDocker drives the whole-project seams from an in-memory container list,
// evaluating each query's filters the way the daemon would.
type fakeDocker struct {
	project    string
	containers []fakeContainer
	services   []string // compose config --services
	configName string   // compose config --format json name

	servicesErr  error
	psErr        error
	psServiceErr error // fail only the per-service queries (q.Service != "")
	psFailures   int   // fail this many ps calls before answering

	queries  []docker.ProjectContainerQuery
	psCalls  int
	svcCalls int
}

func (f *fakeDocker) matches(c fakeContainer, q docker.ProjectContainerQuery) bool {
	if q.Service != "" && c.service != q.Service {
		return false
	}
	switch q.Oneoff {
	case docker.OneoffLabelled:
		if c.oneoff == "" {
			return false
		}
	case docker.OneoffExcluded:
		if c.oneoff != "False" {
			return false
		}
	case docker.OneoffAny:
	}
	switch q.State {
	case docker.StateRunning:
		return c.state == "running"
	case docker.StateExitedZero:
		return c.state == "exited0"
	case docker.StateAny:
	}
	return true
}

func installFake(t *testing.T, f *fakeDocker) {
	t.Helper()
	shrinkBackoff(t)
	origName, origSvc, origPS := configProjectName, configServices, projectContainerNames
	t.Cleanup(func() { configProjectName, configServices, projectContainerNames = origName, origSvc, origPS })

	configProjectName = func(*docker.Compose, context.Context) (string, error) { return f.configName, nil }
	configServices = func(*docker.Compose, context.Context) ([]string, error) {
		f.svcCalls++
		return f.services, f.servicesErr
	}
	projectContainerNames = func(_ context.Context, _ string, _ []string, q docker.ProjectContainerQuery) ([]string, error) {
		f.psCalls++
		if f.psErr != nil {
			return nil, f.psErr
		}
		if f.psServiceErr != nil && q.Service != "" {
			return nil, f.psServiceErr
		}
		if f.psCalls <= f.psFailures {
			return nil, errors.New("transient probe failure")
		}
		f.queries = append(f.queries, q)
		if q.Project != f.project {
			return nil, nil
		}
		var names []string
		for _, c := range f.containers {
			if f.matches(c, q) {
				names = append(names, c.name)
			}
		}
		return names, nil
	}
}

func runWhole(t *testing.T, dockerCfg *config.DockerConfig) error {
	t.Helper()
	return ContainersRunning{}.Run(context.Background(), nil, spec.ExecContext{
		Config:       &config.DweConfig{},
		DockerConfig: dockerCfg,
	})
}

func TestContainersRunning_WholeProject(t *testing.T) {
	cases := []struct {
		name       string
		containers []fakeContainer
		services   []string
		wantErr    string // "" = pass
	}{
		{
			name: "all running",
			containers: []fakeContainer{
				{"shop-app-1", "app", "False", "running"},
				{"shop-db-1", "db", "False", "running"},
			},
			services: []string{"app", "db"},
		},
		{
			name: "one-shot exited 0",
			containers: []fakeContainer{
				{"shop-app-1", "app", "False", "running"},
				{"shop-migrate-1", "migrate", "False", "exited0"},
			},
			services: []string{"app", "migrate"},
		},
		{
			name: "crashed container",
			containers: []fakeContainer{
				{"shop-app-1", "app", "False", "running"},
				{"shop-db-1", "db", "False", "exited1"},
			},
			services: []string{"app", "db"},
			wantErr:  "containers not running: shop-db-1",
		},
		{
			name: "created and restarting containers",
			containers: []fakeContainer{
				{"shop-app-1", "app", "False", "restarting"},
				{"shop-db-1", "db", "False", "created"},
			},
			services: []string{"app", "db"},
			wantErr:  "containers not running: shop-app-1, shop-db-1",
		},
		{
			name: "stopped inactive-profile service passes via the slow path",
			containers: []fakeContainer{
				{"shop-app-1", "app", "False", "running"},
				{"shop-debug-1", "debug", "False", "exited143"},
			},
			services: []string{"app"},
		},
		{
			name: "backend without the oneoff label is evaluated unfiltered",
			containers: []fakeContainer{
				{"shop-app-1", "app", "", "running"},
				{"shop-db-1", "db", "", "exited1"},
			},
			services: []string{"app", "db"},
			wantErr:  "containers not running: shop-db-1",
		},
		{
			name: "only one-off containers on a labelled backend",
			containers: []fakeContainer{
				{"shop-worker-run-1", "app", "True", "exited1"},
			},
			services: []string{"app"},
		},
		{
			name:     "no containers at all (scale 0)",
			services: []string{"app"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDocker{project: "shop", containers: tc.containers, services: tc.services}
			installFake(t, f)
			err := runWhole(t, &config.DockerConfig{ProjectName: "shop"})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("want error %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestContainersRunning_WholeProject_OneoffFilterFollowsProbe(t *testing.T) {
	for _, tc := range []struct {
		name   string
		oneoff string
		want   docker.OneoffFilter
	}{
		{"labelled backend", "False", docker.OneoffExcluded},
		{"backend without the label", "", docker.OneoffAny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDocker{project: "shop", containers: []fakeContainer{{"shop-app-1", "app", tc.oneoff, "exited1"}}, services: []string{"app"}}
			installFake(t, f)
			err := runWhole(t, &config.DockerConfig{ProjectName: "shop"})
			if err == nil || err.Error() != "containers not running: shop-app-1" {
				t.Fatalf("want the crashed container reported, got %v", err)
			}
			// oneoff probe, all, running, exited 0, then the per-service query.
			if len(f.queries) != 5 || f.queries[0].Oneoff != docker.OneoffLabelled {
				t.Fatalf("want the oneoff-label probe then four queries, got %+v", f.queries)
			}
			for _, q := range f.queries[1:] {
				if q.Oneoff != tc.want {
					t.Errorf("query %+v: oneoff filter = %v, want %v", q, q.Oneoff, tc.want)
				}
			}
		})
	}
}

func TestContainersRunning_WholeProject_FastPathSkipsConfig(t *testing.T) {
	f := &fakeDocker{project: "shop", containers: []fakeContainer{{"shop-app-1", "app", "False", "running"}}}
	installFake(t, f)
	if err := runWhole(t, &config.DockerConfig{ProjectName: "shop"}); err != nil {
		t.Fatal(err)
	}
	if f.svcCalls != 0 {
		t.Errorf("config --services must run only when candidates exist, got %d calls", f.svcCalls)
	}
}

func TestContainersRunning_WholeProject_ConfigServicesFailureSurfaced(t *testing.T) {
	f := &fakeDocker{
		project:     "shop",
		containers:  []fakeContainer{{"shop-db-1", "db", "False", "exited1"}},
		servicesErr: errors.New("config --services: unknown flag"),
	}
	installFake(t, f)
	err := runWhole(t, &config.DockerConfig{ProjectName: "shop"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("want the config --services failure surfaced, got %v", err)
	}
	if f.svcCalls != probeRetries+1 {
		t.Errorf("config --services attempts = %d, want %d", f.svcCalls, probeRetries+1)
	}
}

// A failed per-service query must fail the check: skipping it would leave that
// service's crashed container out of the active set and pass the check.
func TestContainersRunning_WholeProject_ServiceQueryFailureSurfaced(t *testing.T) {
	f := &fakeDocker{
		project:      "shop",
		containers:   []fakeContainer{{"shop-db-1", "db", "False", "exited1"}},
		services:     []string{"db"},
		psServiceErr: errors.New("Cannot connect to the Docker daemon"),
	}
	installFake(t, f)
	err := runWhole(t, &config.DockerConfig{ProjectName: "shop"})
	if err == nil || !strings.Contains(err.Error(), "Cannot connect to the Docker daemon") {
		t.Fatalf("want the per-service query failure surfaced, got %v", err)
	}
}

func TestContainersRunning_WholeProject_NameFromComposeConfig(t *testing.T) {
	// No project.name and no docker.yml project_name: compose.ProjectName is
	// empty, so the name comes from compose's own config.
	f := &fakeDocker{project: "fromcompose", configName: "fromcompose", containers: []fakeContainer{{"a-1", "a", "False", "exited1"}}, services: []string{"a"}}
	installFake(t, f)
	err := runWhole(t, nil)
	if err == nil || err.Error() != "containers not running: a-1" {
		t.Fatalf("want the compose-config project queried, got %v", err)
	}
	for _, q := range f.queries {
		if q.Project != "fromcompose" {
			t.Errorf("query project = %q, want fromcompose", q.Project)
		}
	}
}

func TestContainersRunning_WholeProject_NameUnresolvable(t *testing.T) {
	t.Run("empty name", func(t *testing.T) {
		f := &fakeDocker{}
		installFake(t, f)
		err := runWhole(t, nil)
		if !errors.Is(err, errNoProjectName) {
			t.Fatalf("want errNoProjectName, got %v", err)
		}
		if f.psCalls != 0 {
			t.Errorf("no ps query may run without a project name, got %d", f.psCalls)
		}
	})
	t.Run("compose config fails", func(t *testing.T) {
		installFake(t, &fakeDocker{})
		configProjectName = func(*docker.Compose, context.Context) (string, error) {
			return "", errors.New("unknown flag: --format")
		}
		err := runWhole(t, nil)
		if !errors.Is(err, errNoProjectName) || !strings.Contains(err.Error(), "unknown flag: --format") {
			t.Fatalf("want errNoProjectName wrapping the cause, got %v", err)
		}
	})
}

func TestContainersRunning_WholeProject_ProbeRetry(t *testing.T) {
	t.Run("transient failure recovers", func(t *testing.T) {
		f := &fakeDocker{project: "shop", psFailures: 1, containers: []fakeContainer{{"shop-app-1", "app", "False", "running"}}}
		installFake(t, f)
		if err := runWhole(t, &config.DockerConfig{ProjectName: "shop"}); err != nil {
			t.Fatalf("expected recovery after one transient failure, got %v", err)
		}
	})
	t.Run("persistent failure surfaced", func(t *testing.T) {
		f := &fakeDocker{project: "shop", psErr: errors.New("Cannot connect to the Docker daemon")}
		installFake(t, f)
		err := runWhole(t, &config.DockerConfig{ProjectName: "shop"})
		if err == nil || !strings.Contains(err.Error(), "Cannot connect to the Docker daemon") {
			t.Fatalf("want the probe failure surfaced, got %v", err)
		}
		if f.psCalls != probeRetries+1 {
			t.Errorf("ps attempts = %d, want %d", f.psCalls, probeRetries+1)
		}
	})
}
