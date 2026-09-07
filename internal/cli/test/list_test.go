package test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

func newListTestCmd() (*cobra.Command, *bytes.Buffer) {
	var out bytes.Buffer
	cmd := &cobra.Command{Use: "list"}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	return cmd, &out
}

func TestRunTestList_NoTestsDir(t *testing.T) {
	baseDir := t.TempDir()
	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out := newListTestCmd()

	if err := runTestList(cmd, flags); err != nil {
		t.Fatalf("runTestList: %v", err)
	}
	if out.String() != "" {
		t.Errorf("expected no output for an absent tests dir, got %q", out.String())
	}
}

func TestRunTestList_TextWithDescriptions(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "redis-off", "description: Deploy with redis disabled\n")
	writeScenarioFile(t, baseDir, "smoke", "description: Smoke test\n")

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out := newListTestCmd()

	if err := runTestList(cmd, flags); err != nil {
		t.Fatalf("runTestList: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "redis-off") || !strings.Contains(text, "Deploy with redis disabled") {
		t.Errorf("missing redis-off entry, got %q", text)
	}
	if !strings.Contains(text, "smoke") || !strings.Contains(text, "Smoke test") {
		t.Errorf("missing smoke entry, got %q", text)
	}
	// Sorted order: redis-off before smoke.
	if strings.Index(text, "redis-off") > strings.Index(text, "smoke") {
		t.Errorf("expected sorted order, got %q", text)
	}
}

func TestRunTestList_JSON(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "smoke", "description: Smoke test\n")

	flags := &cmdctx.RootFlags{Root: baseDir, Output: "json"}
	cmd, out := newListTestCmd()

	if err := runTestList(cmd, flags); err != nil {
		t.Fatalf("runTestList: %v", err)
	}
	var got testListJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out.String())
	}
	if len(got.Scenarios) != 1 || got.Scenarios[0].Name != "smoke" || got.Scenarios[0].Description != "Smoke test" {
		t.Errorf("unexpected JSON payload: %+v", got)
	}
}

func TestRunTestList_JSONEmpty(t *testing.T) {
	baseDir := t.TempDir()
	flags := &cmdctx.RootFlags{Root: baseDir, Output: "json"}
	cmd, out := newListTestCmd()

	if err := runTestList(cmd, flags); err != nil {
		t.Fatalf("runTestList: %v", err)
	}
	var got testListJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out.String())
	}
	if got.Scenarios == nil || len(got.Scenarios) != 0 {
		t.Errorf("expected an empty (non-nil) scenarios array, got %+v", got.Scenarios)
	}
}

func TestRunTestList_LoadErrorPropagates(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "broken", "bogus_field: true\n")

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, _ := newListTestCmd()

	if err := runTestList(cmd, flags); err == nil {
		t.Fatal("expected an error for a strict-decode failure, got nil")
	}
}

// writeProjectFile writes one file under baseDir, creating parent directories.
func writeProjectFile(t *testing.T, baseDir, rel, body string) {
	t.Helper()
	path := filepath.Join(baseDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// writeMinimalProject lays down the smallest project the cost profiler can
// load: one required app service, an empty compose base, no build, no
// pipelines.
func writeMinimalProject(t *testing.T, baseDir string) {
	t.Helper()
	writeProjectFile(t, baseDir, "workspace.yml", "project:\n  name: demo\ncompose:\n  base: compose.yaml\n")
	writeProjectFile(t, baseDir, "workspace/services/app/service.yml", "type: app\ncontainer: app\nrequired: true\n")
	writeProjectFile(t, baseDir, "compose.yaml", "services: {}\n")
}

// listProfiles runs `dwe test list --output json` and returns the decoded rows.
func listProfiles(t *testing.T, baseDir string) []testListEntryJSON {
	t.Helper()
	flags := &cmdctx.RootFlags{Root: baseDir, Output: "json"}
	cmd, out := newListTestCmd()
	if err := runTestList(cmd, flags); err != nil {
		t.Fatalf("runTestList: %v", err)
	}
	var got testListJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out.String())
	}
	return got.Scenarios
}

func TestRunTestList_CostProfileMinimalProject(t *testing.T) {
	baseDir := t.TempDir()
	writeMinimalProject(t, baseDir)
	writeScenarioFile(t, baseDir, "smoke", "description: Smoke\n")

	rows := listProfiles(t, baseDir)
	if len(rows) != 1 {
		t.Fatalf("expected 1 scenario, got %d", len(rows))
	}
	p := rows[0].CostProfile
	if p == nil {
		t.Fatal("expected a cost profile for a loadable project")
	}
	if p.EnabledServices != 1 {
		t.Errorf("enabled_services = %d, want 1", p.EnabledServices)
	}
	if len(p.BuildServices) != 0 {
		t.Errorf("build_services = %v, want none", p.BuildServices)
	}
	if len(p.ExternalImages) != 0 {
		t.Errorf("external_images = %v, want none", p.ExternalImages)
	}
	if p.MaxStartPeriodSeconds != 0 {
		t.Errorf("max_start_period_seconds = %v, want 0", p.MaxStartPeriodSeconds)
	}
	if p.SharedVolumes != 0 {
		t.Errorf("shared_volumes = %d, want 0", p.SharedVolumes)
	}
	if len(p.IsolationFindings) != 0 {
		t.Errorf("isolation_findings = %v, want none", p.IsolationFindings)
	}
	// The built-in default deploy pipeline runs only dwe's own subcommands.
	if p.HostSteps != 0 {
		t.Errorf("host_steps = %d, want 0", p.HostSteps)
	}
}

func TestRunTestList_CostProfileHeavyProject(t *testing.T) {
	baseDir := t.TempDir()
	writeProjectFile(t, baseDir, "workspace.yml", "project:\n  name: demo\ncompose:\n  base: compose.yaml\n")
	writeProjectFile(t, baseDir, "workspace/services/app/service.yml",
		"type: app\ncontainer: app\nrequired: true\ncompose:\n  - compose/app.yml\n")
	writeProjectFile(t, baseDir, "compose.yaml", `services:
  db:
    image: postgres:16
    healthcheck:
      start_period: 30s
  cache:
    image: redis:7
    healthcheck:
      start_period: 5s
volumes:
  composer-cache:
    external: true
`)
	writeProjectFile(t, baseDir, "compose/app.yml", `services:
  app:
    build: .
    image: demo-app:dev
`)
	writeProjectFile(t, baseDir, "workspace/docker.yml", `resources:
  volumes:
    composer:
      name: composer-cache
      shared: true
    project_data:
      name: data
`)
	writeProjectFile(t, baseDir, "workspace/services/app/deploy.yml", `phases:
  - name: build
    steps:
      - name: sync
        type: shell
        cmd: "true"
      - name: fanout
        parallel:
          steps:
            - name: nested-shell
              type: shell
              cmd: "true"
            - name: nested-quiet
              type: dwe
              cmd: "info"
      - name: gated
        type: dwe
        cmd: "info"
        when: {type: shell, cmd: "true"}
`)
	writeScenarioFile(t, baseDir, "smoke", `description: Smoke
steps:
  - name: probe
    type: shell
    cmd: "true"
`)

	rows := listProfiles(t, baseDir)
	p := rows[0].CostProfile
	if p == nil {
		t.Fatal("expected a cost profile")
	}
	if got := p.BuildServices; len(got) != 1 || got[0] != "app" {
		t.Errorf("build_services = %v, want [app]", got)
	}
	// demo-app:dev is excluded — the app service builds it locally.
	want := []string{"postgres:16", "redis:7"}
	if !reflect.DeepEqual(p.ExternalImages, want) {
		t.Errorf("external_images = %v, want %v", p.ExternalImages, want)
	}
	// max, not sum: 30s and 5s in parallel.
	if p.MaxStartPeriodSeconds != 30 {
		t.Errorf("max_start_period_seconds = %v, want 30", p.MaxStartPeriodSeconds)
	}
	if p.SharedVolumes != 1 {
		t.Errorf("shared_volumes = %d, want 1", p.SharedVolumes)
	}
	// docker.yml declares composer-cache shared: true, so the profile still
	// lists the finding — it reports facts, not verdicts — but marks it.
	if len(p.IsolationFindings) != 1 || p.IsolationFindings[0].Kind != "external_volume" ||
		p.IsolationFindings[0].Resource != "composer-cache" || !p.IsolationFindings[0].Shared {
		t.Errorf("isolation_findings = %+v, want one shared external_volume composer-cache", p.IsolationFindings)
	}
	// One scenario step + three service deploy steps (a flat shell step, a
	// shell step nested in a parallel group, and a step whose `when:` is shell).
	if p.HostSteps != 4 {
		t.Errorf("host_steps = %d, want 4", p.HostSteps)
	}
}

func TestRunTestList_CostProfileUnloadableConfig(t *testing.T) {
	baseDir := t.TempDir()
	// No workspace.yml at all — `list` must still work on a project whose
	// config does not load, just without a profile.
	writeScenarioFile(t, baseDir, "smoke", "description: Smoke\n")

	rows := listProfiles(t, baseDir)
	if len(rows) != 1 {
		t.Fatalf("expected 1 scenario, got %d", len(rows))
	}
	if rows[0].CostProfile != nil {
		t.Errorf("expected no cost profile for an unloadable config, got %+v", rows[0].CostProfile)
	}
	if rows[0].Name != "smoke" {
		t.Errorf("scenario name = %q, want smoke", rows[0].Name)
	}
}

func TestRunTestList_CostProfileDiffersByScenarioServices(t *testing.T) {
	baseDir := t.TempDir()
	writeProjectFile(t, baseDir, "workspace.yml", "project:\n  name: demo\ncompose:\n  base: compose.yaml\n")
	writeProjectFile(t, baseDir, "workspace/services/app/service.yml", "type: app\ncontainer: app\nrequired: true\n")
	writeProjectFile(t, baseDir, "workspace/services/redis/service.yml",
		"type: infra\ncontainer: redis\ncompose:\n  - compose/redis.yml\n")
	writeProjectFile(t, baseDir, "workspace/defaults.yml", "services:\n  redis:\n    enabled: true\n")
	writeProjectFile(t, baseDir, "compose.yaml", "services: {}\n")
	writeProjectFile(t, baseDir, "compose/redis.yml", "services:\n  redis:\n    image: redis:7\n")
	writeScenarioFile(t, baseDir, "full", "description: Everything on\n")
	writeScenarioFile(t, baseDir, "redis-off", "description: Redis off\nenv:\n  services:\n    disable: [redis]\n")

	rows := listProfiles(t, baseDir)
	if len(rows) != 2 {
		t.Fatalf("expected 2 scenarios, got %d", len(rows))
	}
	byName := map[string]*testCostProfileJSON{}
	for _, r := range rows {
		byName[r.Name] = r.CostProfile
	}
	full, off := byName["full"], byName["redis-off"]
	if full == nil || off == nil {
		t.Fatalf("expected a profile per scenario, got %+v", byName)
	}
	if full.EnabledServices != 2 {
		t.Errorf("full enabled_services = %d, want 2", full.EnabledServices)
	}
	if off.EnabledServices != 1 {
		t.Errorf("redis-off enabled_services = %d, want 1", off.EnabledServices)
	}
	// The disabled service's compose overlay leaves the chain with it.
	if len(full.ExternalImages) != 1 || full.ExternalImages[0] != "redis:7" {
		t.Errorf("full external_images = %v, want [redis:7]", full.ExternalImages)
	}
	if len(off.ExternalImages) != 0 {
		t.Errorf("redis-off external_images = %v, want none", off.ExternalImages)
	}
}

func TestRunTestList_CostProfileRequiredServiceStaysEnabled(t *testing.T) {
	baseDir := t.TempDir()
	writeMinimalProject(t, baseDir)
	writeScenarioFile(t, baseDir, "smoke", "description: Smoke\nenv:\n  services:\n    disable: [app]\n")

	rows := listProfiles(t, baseDir)
	if p := rows[0].CostProfile; p == nil || p.EnabledServices != 1 {
		t.Errorf("a required service must stay enabled, got %+v", p)
	}
}

func TestRunTestList_TextOutputCarriesNoProfile(t *testing.T) {
	baseDir := t.TempDir()
	writeMinimalProject(t, baseDir)
	writeScenarioFile(t, baseDir, "smoke", "description: Smoke\n")

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out := newListTestCmd()
	if err := runTestList(cmd, flags); err != nil {
		t.Fatalf("runTestList: %v", err)
	}
	if got, want := out.String(), "smoke                    Smoke\n"; got != want {
		t.Errorf("human output changed:\n got %q\nwant %q", got, want)
	}
}

// TestRunTestList_CostProfileOmitsBlockingIsolationFindings pins the Blocking
// filter: a container_name / raw host port aborts the run before deploy, so it
// is not part of an "is this safe to run unattended" decision, while the
// shared-resource kinds around it must still be reported.
func TestRunTestList_CostProfileOmitsBlockingIsolationFindings(t *testing.T) {
	baseDir := t.TempDir()
	writeProjectFile(t, baseDir, "workspace.yml", "project:\n  name: demo\ncompose:\n  base: compose.yaml\n")
	writeProjectFile(t, baseDir, "workspace/services/app/service.yml", "type: app\ncontainer: app\nrequired: true\n")
	writeProjectFile(t, baseDir, "compose.yaml", `services:
  app:
    image: nginx:1
    container_name: pinned-app
    ports:
      - "8080:80"
volumes:
  composer-cache:
    external: true
`)
	writeScenarioFile(t, baseDir, "smoke", "description: Smoke\n")

	p := listProfiles(t, baseDir)[0].CostProfile
	if p == nil {
		t.Fatal("expected a cost profile")
	}
	kinds := make([]string, 0, len(p.IsolationFindings))
	for _, f := range p.IsolationFindings {
		kinds = append(kinds, f.Kind)
	}
	if !reflect.DeepEqual(kinds, []string{"external_volume"}) {
		t.Errorf("isolation_findings kinds = %v, want [external_volume] only", kinds)
	}
	if p.IsolationFindings[0].Shared {
		t.Errorf("no docker.yml declares this volume shared, got %+v", p.IsolationFindings[0])
	}
}

// TestRunTestList_CostProfileSharedKeyOmitted pins the omitempty half of the
// contract: an unacknowledged finding serializes exactly as it did before the
// Shared field existed, so existing consumers see byte-identical output.
func TestRunTestList_CostProfileSharedKeyOmitted(t *testing.T) {
	baseDir := t.TempDir()
	writeProjectFile(t, baseDir, "workspace.yml", "project:\n  name: demo\ncompose:\n  base: compose.yaml\n")
	writeProjectFile(t, baseDir, "workspace/services/app/service.yml", "type: app\ncontainer: app\nrequired: true\n")
	writeProjectFile(t, baseDir, "compose.yaml", "services: {}\nvolumes:\n  data:\n    external: true\n")
	writeScenarioFile(t, baseDir, "smoke", "description: Smoke\n")

	flags := &cmdctx.RootFlags{Root: baseDir, Output: "json"}
	cmd, out := newListTestCmd()
	if err := runTestList(cmd, flags); err != nil {
		t.Fatalf("runTestList: %v", err)
	}
	if !strings.Contains(out.String(), `"kind":"external_volume"`) {
		t.Fatalf("expected an external_volume finding in %s", out.String())
	}
	if strings.Contains(out.String(), `"shared"`) {
		t.Errorf("shared must be omitted when false, got %s", out.String())
	}
}

// TestRunTestList_CostProfileEnableOverlay covers the env.services.enable half
// of the scenario overlay — the form the scaffolded smoke.yml documents.
func TestRunTestList_CostProfileEnableOverlay(t *testing.T) {
	baseDir := t.TempDir()
	writeProjectFile(t, baseDir, "workspace.yml", "project:\n  name: demo\ncompose:\n  base: compose.yaml\n")
	writeProjectFile(t, baseDir, "workspace/services/app/service.yml", "type: app\ncontainer: app\nrequired: true\n")
	writeProjectFile(t, baseDir, "workspace/services/redis/service.yml",
		"type: infra\ncontainer: redis\ncompose:\n  - compose/redis.yml\n")
	writeProjectFile(t, baseDir, "compose.yaml", "services: {}\n")
	writeProjectFile(t, baseDir, "compose/redis.yml", "services:\n  redis:\n    image: redis:7\n")
	writeScenarioFile(t, baseDir, "off", "description: Default\n")
	writeScenarioFile(t, baseDir, "on", "description: Redis on\nenv:\n  services:\n    enable: [redis]\n")

	byName := map[string]*testCostProfileJSON{}
	for _, r := range listProfiles(t, baseDir) {
		byName[r.Name] = r.CostProfile
	}
	off, on := byName["off"], byName["on"]
	if off == nil || on == nil {
		t.Fatalf("expected a profile per scenario, got %+v", byName)
	}
	if off.EnabledServices != 1 || len(off.ExternalImages) != 0 {
		t.Errorf("default profile = %+v, want 1 service and no images", off)
	}
	if on.EnabledServices != 2 {
		t.Errorf("enabled overlay enabled_services = %d, want 2", on.EnabledServices)
	}
	if !reflect.DeepEqual(on.ExternalImages, []string{"redis:7"}) {
		t.Errorf("enabled overlay external_images = %v, want [redis:7]", on.ExternalImages)
	}
}

// TestRunTestList_CostProfileHostStepsByCommandTarget pins the type: command
// classification: a service-scoped command runs in the container and must not
// count, while a host command and an unresolvable reference both must.
func TestRunTestList_CostProfileHostStepsByCommandTarget(t *testing.T) {
	cases := []struct {
		name    string
		command string
		steps   string
		want    int
	}{
		{
			name:    "service scoped command is not a host step",
			command: "commands:\n  migrate:\n    type: service_exec\n    service: app\n    cmd: \"true\"\n",
			steps:   "  - name: run\n    type: command\n    cmd: test.migrate\n",
			want:    0,
		},
		{
			name:    "host command counts",
			command: "commands:\n  seed:\n    type: shell\n    cmd: \"true\"\n",
			steps:   "  - name: run\n    type: command\n    cmd: test.seed\n",
			want:    1,
		},
		{
			name:    "workflow inherits its sub-steps",
			command: "commands:\n  inner:\n    type: shell\n    cmd: \"true\"\n  flow:\n    type: workflow\n    steps:\n      - command: test.inner\n",
			steps:   "  - name: run\n    type: command\n    cmd: test.flow\n",
			want:    1,
		},
		{
			name:    "builtin shell step counts",
			command: "",
			steps:   "  - name: run\n    type: builtin\n    cmd: shell\n    with:\n      command: \"true\"\n",
			want:    1,
		},
		{
			name:    "non-shell builtin does not count",
			command: "",
			steps:   "  - name: note\n    type: builtin\n    cmd: message\n    with:\n      text: hi\n",
			want:    0,
		},
		{
			// argv_append_from is valid on a container command, but the
			// expression itself runs sh -c on the HOST in the project root
			// (runio.AppendArgvFrom). Classifying it container-side would
			// report 0 for a scenario that runs project-authored host code.
			name:    "argv_append_from on a service command still counts",
			command: "commands:\n  lint:\n    type: service_exec\n    service: app\n    argv: [ruff, check]\n    argv_append_from: \"git ls-files '*.py'\"\n",
			steps:   "  - name: run\n    type: command\n    cmd: test.lint\n",
			want:    1,
		},
		{
			// A workflow sub-step's cmd: when: dispatches to condition.EvalCmd
			// — sh -c in the project root — exactly like a pipeline step's
			// shell when:, which is already counted.
			name:    "workflow sub-step cmd: when: counts even with container sub-steps",
			command: "commands:\n  inner:\n    type: service_exec\n    service: app\n    cmd: \"true\"\n  flow:\n    type: workflow\n    steps:\n      - command: test.inner\n        when: \"cmd: test -f ./scripts/x\"\n",
			steps:   "  - name: run\n    type: command\n    cmd: test.flow\n",
			want:    1,
		},
		{
			// `when:` stays valid on a parallel CONTAINER, and it dispatches to
			// the same host sh -c. Flattening that dropped the container itself,
			// so its when: went uncounted — an undercount in the unsafe
			// direction for the field that gates an unattended run.
			name:    "workflow parallel container cmd: when: counts",
			command: "commands:\n  inner:\n    type: service_exec\n    service: app\n    cmd: \"true\"\n  other:\n    type: service_exec\n    service: app\n    cmd: \"true\"\n  flow:\n    type: workflow\n    steps:\n      - name: group\n        when: \"cmd: test -f ./scripts/x\"\n        parallel:\n          steps:\n            - command: test.inner\n            - command: test.other\n",
			steps:   "  - name: run\n    type: command\n    cmd: test.flow\n",
			want:    1,
		},
		{
			name:    "workflow parallel sub-step cmd: when: counts",
			command: "commands:\n  inner:\n    type: service_exec\n    service: app\n    cmd: \"true\"\n  other:\n    type: service_exec\n    service: app\n    cmd: \"true\"\n  flow:\n    type: workflow\n    steps:\n      - name: group\n        parallel:\n          steps:\n            - command: test.inner\n              when: \"cmd: test -f ./scripts/x\"\n            - command: test.other\n",
			steps:   "  - name: run\n    type: command\n    cmd: test.flow\n",
			want:    1,
		},
		{
			name:    "workflow parallel container without a shell when: does not count",
			command: "commands:\n  inner:\n    type: service_exec\n    service: app\n    cmd: \"true\"\n  other:\n    type: service_exec\n    service: app\n    cmd: \"true\"\n  flow:\n    type: workflow\n    steps:\n      - name: group\n        parallel:\n          steps:\n            - command: test.inner\n            - command: test.other\n",
			steps:   "  - name: run\n    type: command\n    cmd: test.flow\n",
			want:    0,
		},
		{
			name:    "workflow sub-step builtin predicate when: does not count",
			command: "commands:\n  inner:\n    type: service_exec\n    service: app\n    cmd: \"true\"\n  flow:\n    type: workflow\n    steps:\n      - command: test.inner\n        when: \"file_exists ./scripts/x\"\n",
			steps:   "  - name: run\n    type: command\n    cmd: test.flow\n",
			want:    0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseDir := t.TempDir()
			writeMinimalProject(t, baseDir)
			if tc.command != "" {
				writeProjectFile(t, baseDir, "workspace/commands/test.yml", tc.command)
			}
			writeScenarioFile(t, baseDir, "smoke", "description: Smoke\nsteps:\n"+tc.steps)

			p := listProfiles(t, baseDir)[0].CostProfile
			if p == nil {
				t.Fatal("expected a cost profile")
			}
			if p.HostSteps != tc.want {
				t.Errorf("host_steps = %d, want %d", p.HostSteps, tc.want)
			}
		})
	}
}

// TestRunTestList_CostProfileHostStepsFromValidateChecks pins the third host
// channel: the runner spawns `dwe validate` and `dwe deploy run` in the copy,
// and both execute workspace/validate.yml checks. A check running project shell
// is unsandboxed host code exactly like a pipeline step, so it must close the
// gate; a probing builtin must not, and the services: gate still applies.
func TestRunTestList_CostProfileHostStepsFromValidateChecks(t *testing.T) {
	cases := []struct {
		name     string
		validate string
		command  string
		want     int
	}{
		{
			name:     "shell builtin check counts",
			validate: "checks:\n  - id: ssh-key\n    description: SSH key present\n    stages: [deploy]\n    type: builtin\n    cmd: shell\n    with:\n      command: \"test -f ~/.ssh/id_ed25519\"\n",
			want:     1,
		},
		{
			name:     "probing builtin check does not count",
			validate: "checks:\n  - id: env\n    description: env present\n    stages: [deploy]\n    type: builtin\n    cmd: file_exists\n    with:\n      path: .env\n",
			want:     0,
		},
		{
			name:     "command check counts",
			validate: "checks:\n  - id: seed\n    description: seed data\n    stages: [deploy]\n    type: command\n    cmd: test.seed\n",
			command:  "commands:\n  seed:\n    type: shell\n    cmd: \"true\"\n",
			want:     1,
		},
		{
			name:     "check gated on an enabled service counts",
			validate: "checks:\n  - id: ssh-key\n    description: SSH key present\n    stages: [deploy]\n    type: builtin\n    cmd: shell\n    services: [app]\n    with:\n      command: \"true\"\n",
			want:     1,
		},
		{
			name:     "check gated on an absent service does not count",
			validate: "checks:\n  - id: ssh-key\n    description: SSH key present\n    stages: [deploy]\n    type: builtin\n    cmd: shell\n    services: [worker]\n    with:\n      command: \"true\"\n",
			want:     0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseDir := t.TempDir()
			writeMinimalProject(t, baseDir)
			writeProjectFile(t, baseDir, "workspace/validate.yml", tc.validate)
			if tc.command != "" {
				writeProjectFile(t, baseDir, "workspace/commands/test.yml", tc.command)
			}
			writeScenarioFile(t, baseDir, "smoke", "description: Smoke\n")

			p := listProfiles(t, baseDir)[0].CostProfile
			if p == nil {
				t.Fatal("expected a cost profile")
			}
			if p.HostSteps != tc.want {
				t.Errorf("host_steps = %d, want %d", p.HostSteps, tc.want)
			}
		})
	}
}

// TestRunTestList_CostProfileUnknownCommandCountsAsHost pins the safe direction
// of the unknown case: a reference the registry cannot resolve closes the gate.
func TestRunTestList_CostProfileUnknownCommandCountsAsHost(t *testing.T) {
	baseDir := t.TempDir()
	writeMinimalProject(t, baseDir)
	writeScenarioFile(t, baseDir, "smoke", "description: Smoke\nsteps:\n  - name: run\n    type: command\n    cmd: nope\n")

	p := listProfiles(t, baseDir)[0].CostProfile
	if p == nil {
		t.Fatal("expected a cost profile")
	}
	if p.HostSteps != 1 {
		t.Errorf("host_steps = %d, want 1 for an unresolvable command", p.HostSteps)
	}
}

// TestRunTestList_CostProfileDegradesOnBrokenProjectState covers the remaining
// nil-degrading branches of newCostProfiler: `list` must keep listing while any
// of the files it reads is mid-edit.
func TestRunTestList_CostProfileDegradesOnBrokenProjectState(t *testing.T) {
	cases := []struct{ name, rel, body string }{
		{"docker.yml", "workspace/docker.yml", "resources: [not, a, mapping]\n"},
		{"project deploy.yml", "workspace/deploy.yml", "phases: {not: a list}\n"},
		{"service deploy.yml", "workspace/services/app/deploy.yml", "phases: {not: a list}\n"},
		{"validate.yml", "workspace/validate.yml", "checks: {not: a list}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseDir := t.TempDir()
			writeMinimalProject(t, baseDir)
			writeProjectFile(t, baseDir, tc.rel, tc.body)
			writeScenarioFile(t, baseDir, "smoke", "description: Smoke\n")

			rows := listProfiles(t, baseDir)
			if len(rows) != 1 {
				t.Fatalf("expected 1 scenario, got %d", len(rows))
			}
			if rows[0].CostProfile != nil {
				t.Errorf("expected no profile for a broken %s, got %+v", tc.rel, rows[0].CostProfile)
			}
		})
	}
}
