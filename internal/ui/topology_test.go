package ui

import (
	"strings"
	"testing"
)

// --- ParseComposeTopology ---

const composeConfigYAML = `
services:
  nginx:
    image: nginx
    depends_on:
      app-main:
        condition: service_healthy
  app-main:
    image: myapp
    depends_on:
      db:
        condition: service_healthy
      redis:
        condition: service_started
  db:
    image: postgres
  redis:
    image: redis
  adminer:
    image: adminer
    depends_on:
      db:
        condition: service_started
`

func TestParseComposeTopology_MapForm(t *testing.T) {
	deps, err := ParseComposeTopology([]byte(composeConfigYAML))
	if err != nil {
		t.Fatalf("ParseComposeTopology error: %v", err)
	}

	// nginx depends on app-main
	checkDeps(t, deps, "nginx", []string{"app-main"})
	// app-main depends on db and redis (sorted)
	checkDeps(t, deps, "app-main", []string{"db", "redis"})
	// db and redis have no dependencies
	checkDeps(t, deps, "db", []string{})
	checkDeps(t, deps, "redis", []string{})
	// adminer depends on db
	checkDeps(t, deps, "adminer", []string{"db"})
}

const composeConfigSequenceYAML = `
services:
  app:
    image: myapp
    depends_on:
      - db
      - redis
  db:
    image: postgres
  redis:
    image: redis
`

func TestParseComposeTopology_SequenceForm(t *testing.T) {
	deps, err := ParseComposeTopology([]byte(composeConfigSequenceYAML))
	if err != nil {
		t.Fatalf("ParseComposeTopology error: %v", err)
	}
	checkDeps(t, deps, "app", []string{"db", "redis"})
}

func TestParseComposeTopology_NoDependencies(t *testing.T) {
	data := []byte(`
services:
  nginx:
    image: nginx
  db:
    image: postgres
`)
	deps, err := ParseComposeTopology(data)
	if err != nil {
		t.Fatalf("ParseComposeTopology error: %v", err)
	}
	checkDeps(t, deps, "nginx", []string{})
	checkDeps(t, deps, "db", []string{})
}

func TestParseComposeTopology_EmptyInput(t *testing.T) {
	deps, err := ParseComposeTopology([]byte(""))
	if err != nil {
		t.Fatalf("ParseComposeTopology empty input error: %v", err)
	}
	if len(deps) != 0 {
		t.Errorf("expected empty map, got %v", deps)
	}
}

func TestParseComposeTopology_InvalidYAML(t *testing.T) {
	// Indentation error makes YAML unmarshalable into the expected struct.
	// Use a tab character which is invalid YAML indentation.
	_, err := ParseComposeTopology([]byte("services:\n\t  bad_indent: {"))
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

// --- RenderTopology ---

func TestRenderTopology_Empty(t *testing.T) {
	result := RenderTopology(nil, nil, nil)
	if result != "" {
		t.Errorf("expected empty string for nil deps, got %q", result)
	}
}

func TestRenderTopology_SingleService(t *testing.T) {
	deps := map[string][]string{
		"nginx": {},
	}
	result := RenderTopology(deps, nil, nil)
	if !strings.Contains(result, "nginx") {
		t.Errorf("expected 'nginx' in output, got:\n%s", result)
	}
}

func TestRenderTopology_RootIdentification(t *testing.T) {
	// nginx is a root (nothing depends on it); db is not (app-main depends on it)
	deps := map[string][]string{
		"nginx":    {"app-main"},
		"app-main": {"db"},
		"db":       {},
	}
	result := RenderTopology(deps, nil, nil)

	// nginx must appear as a root node (before app-main, db in tree hierarchy)
	nginxIdx := strings.Index(result, "nginx")
	appIdx := strings.Index(result, "app-main")
	dbIdx := strings.Index(result, "db")

	if nginxIdx < 0 || appIdx < 0 || dbIdx < 0 {
		t.Fatalf("missing nodes in output:\n%s", result)
	}
	if nginxIdx > appIdx {
		t.Errorf("nginx should appear before app-main in output:\n%s", result)
	}
	if appIdx > dbIdx {
		t.Errorf("app-main should appear before db in output:\n%s", result)
	}
}

func TestRenderTopology_StatusAnnotation(t *testing.T) {
	deps := map[string][]string{
		"nginx":    {"app-main"},
		"app-main": {},
	}
	status := map[string]NodeStatus{
		"nginx":    NodeRunning,
		"app-main": NodeStopped,
	}
	result := RenderTopology(deps, status, nil)

	if !strings.Contains(result, "running") {
		t.Errorf("expected 'running' annotation in output:\n%s", result)
	}
	if !strings.Contains(result, "stopped") {
		t.Errorf("expected 'stopped' annotation in output:\n%s", result)
	}
}

func TestRenderTopology_DisabledAnnotation(t *testing.T) {
	deps := map[string][]string{
		"nginx":   {"app"},
		"app":     {},
		"adminer": {},
	}
	status := map[string]NodeStatus{
		"nginx":   NodeRunning,
		"app":     NodeRunning,
		"adminer": NodeDisabled,
	}
	result := RenderTopology(deps, status, nil)
	if !strings.Contains(result, "disabled") {
		t.Errorf("expected 'disabled' annotation for adminer in output:\n%s", result)
	}
	if !strings.Contains(result, "adminer") {
		t.Errorf("expected 'adminer' in output:\n%s", result)
	}
}

func TestRenderTopology_UnknownStatusNoAnnotation(t *testing.T) {
	deps := map[string][]string{
		"nginx": {},
	}
	// No status map — NodeUnknown (default)
	result := RenderTopology(deps, nil, nil)
	if strings.Contains(result, "running") || strings.Contains(result, "stopped") {
		t.Errorf("unexpected status annotation when status is nil:\n%s", result)
	}
	if !strings.Contains(result, "nginx") {
		t.Errorf("expected 'nginx' in output:\n%s", result)
	}
}

func TestRenderTopology_DiamondDependency(t *testing.T) {
	// app-main and app-second both depend on db; nginx depends on app-main
	deps := map[string][]string{
		"nginx":      {"app-main"},
		"app-main":   {"db", "redis"},
		"app-second": {"db", "redis", "app-main"},
		"db":         {},
		"redis":      {},
	}
	result := RenderTopology(deps, nil, nil)

	// nginx and app-second are roots (nothing depends on them)
	if !strings.Contains(result, "nginx") {
		t.Errorf("expected 'nginx' in output:\n%s", result)
	}
	if !strings.Contains(result, "app-second") {
		t.Errorf("expected 'app-second' in output:\n%s", result)
	}
	// db must appear (possibly multiple times as a dep)
	if !strings.Contains(result, "db") {
		t.Errorf("expected 'db' in output:\n%s", result)
	}
}

// checkDeps is a test helper that asserts deps[name] == want (sorted comparison).
func checkDeps(t *testing.T, deps map[string][]string, name string, want []string) {
	t.Helper()
	got, ok := deps[name]
	if !ok {
		t.Errorf("service %q not in topology map", name)
		return
	}
	if len(got) != len(want) {
		t.Errorf("service %q deps: got %v, want %v", name, got, want)
		return
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("service %q deps[%d]: got %q, want %q", name, i, got[i], w)
		}
	}
}
