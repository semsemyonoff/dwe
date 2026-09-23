package usercommands_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// loadIndexRegistry writes files (relative path → YAML) under a temp commands
// dir and loads it through the production loader.
func loadIndexRegistry(t *testing.T, files map[string]string) *usercommands.Registry {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := usercommands.LoadRegistry(dir)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	return reg
}

// markerTranslator prefixes every command description and group string with
// "tr:" and records which lookups were consulted.
type markerTranslator struct {
	i18n.NopTranslator
	calls []string
}

func (m *markerTranslator) CommandDescription(_, id, fallback string) string {
	m.calls = append(m.calls, "cmd:"+id)
	return "tr:" + fallback
}

func (m *markerTranslator) GroupTitle(_, id, fallback string) string {
	m.calls = append(m.calls, "title:"+id)
	return "tr:" + fallback
}

func (m *markerTranslator) GroupDescription(_, id, fallback string) string {
	m.calls = append(m.calls, "desc:"+id)
	return "tr:" + fallback
}

func groupByID(groups []model.CommandGroupSummary, id string) (model.CommandGroupSummary, bool) {
	for _, g := range groups {
		if g.ID == id {
			return g, true
		}
	}
	return model.CommandGroupSummary{}, false
}

func TestCommandIndex_NilRegistry(t *testing.T) {
	cmds, groups := usercommands.CommandIndex(nil, i18n.NopTranslator{}, "")
	if cmds != nil || groups != nil {
		t.Fatalf("nil registry: got %v / %v, want nil / nil", cmds, groups)
	}
}

func TestCommandIndex_Commands(t *testing.T) {
	reg := loadIndexRegistry(t, map[string]string{
		"app.yml": `
commands:
  zeta:
    type: shell
    description: Last by id
    cmd: echo z
  secret:
    type: shell
    description: Private one
    cmd: echo s
    private: true
  exec:
    type: service_exec
    description: Plain service
    service: app-main
    cmd: echo e
  override:
    type: service_exec
    description: Runner wins
    service: app-main
    cmd: echo o
    runner:
      service: app-worker
`,
		"queue.yml": `
commands:
  worker:
    type: daemon
    description: Queue worker
    service: app-main
    argv: [php, artisan, queue:work]
    daemon:
      container_template: "{project}-queue"
`,
	})

	tr := &markerTranslator{}
	cmds, _ := usercommands.CommandIndex(reg, tr, "ru")

	// A daemon's synthetics carry no Service of their own (the builtin
	// validator rejects it); the index must still attribute them.
	synthetics := 0
	for _, c := range cmds {
		if !strings.HasPrefix(c.ID, "queue.worker.") {
			continue
		}
		synthetics++
		if c.Service != "app-main" {
			t.Errorf("%s Service = %q, want %q (daemon source service)", c.ID, c.Service, "app-main")
		}
	}
	if synthetics != 4 {
		t.Errorf("daemon synthetics in index = %d, want 4", synthetics)
	}
	cmds = slices.DeleteFunc(cmds, func(c model.CommandSummary) bool { return c.Group != "app" })

	ids := make([]string, 0, len(cmds))
	for _, c := range cmds {
		ids = append(ids, c.ID)
	}
	if want := []string{"app.exec", "app.override", "app.zeta"}; !slices.Equal(ids, want) {
		t.Fatalf("ids = %v, want %v (private excluded, sorted by ID)", ids, want)
	}

	byID := map[string]model.CommandSummary{}
	for _, c := range cmds {
		byID[c.ID] = c
	}

	tests := []struct {
		id          string
		service     string
		typ         string
		description string
	}{
		{id: "app.exec", service: "app-main", typ: "service_exec", description: "tr:Plain service"},
		{id: "app.override", service: "app-worker", typ: "service_exec", description: "tr:Runner wins"},
		{id: "app.zeta", service: "", typ: "shell", description: "tr:Last by id"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := byID[tt.id]
			if got.Service != tt.service {
				t.Errorf("Service = %q, want %q", got.Service, tt.service)
			}
			if got.Type != tt.typ {
				t.Errorf("Type = %q, want %q", got.Type, tt.typ)
			}
			if got.Description != tt.description {
				t.Errorf("Description = %q, want %q (translator must be consulted)", got.Description, tt.description)
			}
			if got.Group != "app" {
				t.Errorf("Group = %q, want %q", got.Group, "app")
			}
			if !slices.Contains(tr.calls, "cmd:"+tt.id) {
				t.Errorf("translator not consulted for %s; calls = %v", tt.id, tr.calls)
			}
		})
	}
}

func TestCommandIndex_Groups(t *testing.T) {
	var direct, sub strings.Builder
	direct.WriteString("group:\n  title: Admin\n  description: Admin tasks\ncommands:\n")
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		direct.WriteString("  " + n + ":\n    type: shell\n    cmd: echo " + n + "\n")
	}
	sub.WriteString("group:\n  description: Linters\ncommands:\n")
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		sub.WriteString("  " + n + ":\n    type: shell\n    cmd: echo " + n + "\n")
	}

	reg := loadIndexRegistry(t, map[string]string{
		"admin.yml":      direct.String(),
		"admin/lint.yml": sub.String(),
		"db.yml": `
group:
  title: Database
commands:
  up:
    type: shell
    cmd: echo up
  migrate:
    type: shell
    cmd: echo migrate
  seed:
    type: shell
    cmd: echo seed
    private: true
`,
		"internal.yml": `
group:
  title: Internal
  description: Only private commands
commands:
  a:
    type: shell
    cmd: echo a
    private: true
`,
		"tools.yml": `
commands:
  fmt:
    type: shell
    cmd: echo fmt
`,
	})

	tr := &markerTranslator{}
	_, groups := usercommands.CommandIndex(reg, tr, "ru")

	ids := make([]string, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.ID)
	}
	// internal is private-only and must be omitted; order is by ID.
	if want := []string{"admin", "admin.lint", "db", "tools"}; !slices.Equal(ids, want) {
		t.Fatalf("group ids = %v, want %v", ids, want)
	}

	tests := []struct {
		name        string
		id          string
		count       int
		title       string
		description string
	}{
		// GroupNode.Commands holds 3 here (private included); reg.List gives 2.
		{name: "private excluded from count", id: "db", count: 2, title: "tr:db", description: "tr:"},
		{name: "direct plus subgroup", id: "admin", count: 13, title: "tr:admin", description: "tr:Admin tasks"},
		{name: "nested id keeps dotted form", id: "admin.lint", count: 5, title: "tr:lint", description: "tr:Linters"},
		{name: "no group header falls back to segment", id: "tools", count: 1, title: "tr:tools", description: "tr:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, ok := groupByID(groups, tt.id)
			if !ok {
				t.Fatalf("group %q missing", tt.id)
			}
			if g.Count != tt.count {
				t.Errorf("Count = %d, want %d", g.Count, tt.count)
			}
			// The title fallback is the node Name (last id segment), not
			// Meta.Title — mirrors the text listing.
			if g.Title != tt.title {
				t.Errorf("Title = %q, want %q", g.Title, tt.title)
			}
			if g.Description != tt.description {
				t.Errorf("Description = %q, want %q", g.Description, tt.description)
			}
			if !slices.Contains(tr.calls, "title:"+tt.id) || !slices.Contains(tr.calls, "desc:"+tt.id) {
				t.Errorf("translator not consulted for group %s; calls = %v", tt.id, tr.calls)
			}
		})
	}
}

// magentoFiles mirrors a real project: a hub file at services/magento.yml plus
// leaf files under services/magento/, and no services.yml — so the registry's
// ensureGroup materializes a meta-less "services" ancestor.
func magentoFiles() map[string]string {
	return map[string]string{
		"services/magento.yml": `
group:
  description: Magento developer tasks
commands:
  shell:
    type: service_exec
    service: app-magento
    cmd: bash
`,
		"services/magento/cache.yml": `
group:
  description: Cache tools
commands:
  flush:
    type: service_exec
    service: app-magento
    cmd: bin/magento cache:flush
`,
		"services/magento/index.yml": `
commands:
  reindex:
    type: service_exec
    service: app-magento
    cmd: bin/magento indexer:reindex
`,
	}
}

func TestCommandIndex_SyntheticAncestorSkipped(t *testing.T) {
	reg := loadIndexRegistry(t, magentoFiles())
	_, groups := usercommands.CommandIndex(reg, i18n.NopTranslator{}, "")

	if _, ok := groupByID(groups, "services"); ok {
		t.Errorf("synthetic ancestor %q must not be emitted; groups = %+v", "services", groups)
	}
	hub, ok := groupByID(groups, "services.magento")
	if !ok {
		t.Fatalf("services.magento missing; groups = %+v", groups)
	}
	if hub.Description != "Magento developer tasks" {
		t.Errorf("hub Description = %q", hub.Description)
	}
	if hub.Count != 3 {
		t.Errorf("hub Count = %d, want 3", hub.Count)
	}
	for _, id := range []string{"services.magento.cache", "services.magento.index"} {
		if _, ok := groupByID(groups, id); !ok {
			t.Errorf("subgroup %q missing; groups = %+v", id, groups)
		}
	}
}

// A header-only services.yml gives the ancestor non-zero meta but still no
// printable text and no direct commands; a zero-GroupMeta skip would let it
// through, become the shallowest group and swallow sibling hubs.
func TestCommandIndex_HeaderOnlyAncestorSkipped(t *testing.T) {
	files := magentoFiles()
	files["services.yml"] = "group:\n  bridge:\n    enabled: true\n"
	reg := loadIndexRegistry(t, files)

	_, groups := usercommands.CommandIndex(reg, i18n.NopTranslator{}, "")
	if _, ok := groupByID(groups, "services"); ok {
		t.Errorf("header-only ancestor %q must not be emitted; groups = %+v", "services", groups)
	}
	if _, ok := groupByID(groups, "services.magento"); !ok {
		t.Errorf("services.magento missing; groups = %+v", groups)
	}
}

// TestCommandIndex_DeclaredVersusVisible pins the intended contract: the
// builder reports DECLARED state and never evaluates hide:. llms-txt runs
// ApplyVisibility before calling it (a live document); template packs do not,
// because a file on disk must not flip with the state of the Docker stack.
// "Fixing" the divergence by calling ApplyVisibility inside CommandIndex would
// make a generated AGENTS.md depend on Docker state.
func TestCommandIndex_DeclaredVersusVisible(t *testing.T) {
	reg := loadIndexRegistry(t, map[string]string{
		"ops.yml": `
group:
  description: Ops tasks
commands:
  hidden:
    type: shell
    cmd: echo h
    hide: '{{ true }}'
  shown:
    type: shell
    cmd: echo s
`,
	})

	cmds, groups := usercommands.CommandIndex(reg, i18n.NopTranslator{}, "")
	ops, ok := groupByID(groups, "ops")
	if !ok || ops.Count != 2 || len(cmds) != 2 {
		t.Fatalf("declared: ops=%+v ok=%v cmds=%d, want Count 2 and 2 commands", ops, ok, len(cmds))
	}

	if err := reg.ApplyVisibility(nil, t.TempDir()); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}
	cmds, groups = usercommands.CommandIndex(reg, i18n.NopTranslator{}, "")
	ops, ok = groupByID(groups, "ops")
	if !ok || ops.Count != 1 || len(cmds) != 1 {
		t.Fatalf("after ApplyVisibility: ops=%+v ok=%v cmds=%d, want Count 1 and 1 command", ops, ok, len(cmds))
	}
}

// TestCommandIndex_Summary: the index carries the full description for packs
// that render a section, plus the one-line summary for packs and llms-txt
// lines. Daemon synthetics and the daemon's group inherit it naturally.
func TestCommandIndex_Summary(t *testing.T) {
	reg := loadIndexRegistry(t, map[string]string{
		"db.yml": `
group:
  description: |
    Database tasks
    Second group line
commands:
  migrate:
    type: shell
    description: |

      Run migrations
      Usage: dwe cmd db.migrate
    cmd: echo m
  worker:
    type: daemon
    description: |
      Queue worker
      Starts php artisan queue:work
    service: app-main
    argv: [php, artisan, queue:work]
    daemon:
      container_template: "{project}-queue"
`,
	})
	cmds, groups := usercommands.CommandIndex(reg, i18n.NopTranslator{}, "")

	byID := map[string]model.CommandSummary{}
	for _, c := range cmds {
		byID[c.ID] = c
	}
	m := byID["db.migrate"]
	if m.Summary != "Run migrations" || !strings.Contains(m.Description, "Usage:") {
		t.Errorf("db.migrate: Summary=%q Description=%q", m.Summary, m.Description)
	}
	if s := byID["db.worker.start"].Summary; s != "Queue worker" {
		t.Errorf("daemon synthetic summary = %q, want %q", s, "Queue worker")
	}

	for id, want := range map[string]string{"db": "Database tasks", "db.worker": "Queue worker"} {
		g, ok := groupByID(groups, id)
		if !ok {
			t.Fatalf("group %s missing: %+v", id, groups)
		}
		if g.Summary != want || !strings.Contains(g.Description, "\n") {
			t.Errorf("group %s: Summary=%q Description=%q", id, g.Summary, g.Description)
		}
	}
}
