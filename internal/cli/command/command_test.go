package command

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/semsemyonoff/devbox/internal/cli/cmdctx"
	"github.com/semsemyonoff/devbox/internal/core/project/config"
	"github.com/semsemyonoff/devbox/internal/core/ui/ask"
	"github.com/semsemyonoff/devbox/internal/core/ui/widgets"
	"github.com/semsemyonoff/devbox/internal/core/usercommands"
	"github.com/semsemyonoff/devbox/internal/core/usercommands/model"
	"github.com/semsemyonoff/devbox/internal/shared/i18n"

	"github.com/spf13/cobra"
)

// --- parseSetFlags ---

func TestParseSetFlags_empty(t *testing.T) {
	got, err := parseSetFlags(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestParseSetFlags_valid(t *testing.T) {
	got, err := parseSetFlags([]string{"key=value", "foo=bar"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["key"] != "value" {
		t.Errorf("key: want %q, got %q", "value", got["key"])
	}
	if got["foo"] != "bar" {
		t.Errorf("foo: want %q, got %q", "bar", got["foo"])
	}
}

func TestParseSetFlags_valueWithEquals(t *testing.T) {
	// The value itself can contain '='
	got, err := parseSetFlags([]string{"url=http://host:8080/path?a=b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["url"] != "http://host:8080/path?a=b" {
		t.Errorf("url: want full value with '=', got %q", got["url"])
	}
}

func TestParseSetFlags_emptyValue(t *testing.T) {
	got, err := parseSetFlags([]string{"key="})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["key"] != "" {
		t.Errorf("expected empty value for key, got %q", got["key"])
	}
}

func TestParseSetFlags_noEquals(t *testing.T) {
	_, err := parseSetFlags([]string{"noequals"})
	if err == nil {
		t.Fatal("expected error for missing '='")
	}
}

func TestParseSetFlags_emptyKey(t *testing.T) {
	_, err := parseSetFlags([]string{"=value"})
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

// --- buildTreeNodes / groupNodeToChildren ---

func TestBuildTreeNodes_emptyRegistry(t *testing.T) {
	// An empty root GroupNode should produce no nodes.
	root := &usercommands.GroupNode{}
	nodes := buildTreeNodes(root, "", false, i18n.NopTranslator{}, "")
	if len(nodes) != 0 {
		t.Errorf("expected empty nodes, got %d", len(nodes))
	}
}

func TestBuildTreeNodes_publicCommandsOnly(t *testing.T) {
	root := &usercommands.GroupNode{
		Commands: []*usercommands.CommandDef{
			{ID: "services.main.migrate", LocalName: "migrate", Type: usercommands.CommandTypeServiceExec, Private: false},
			{ID: "services.main.secret", LocalName: "secret", Type: usercommands.CommandTypeShell, Private: true},
		},
	}
	nodes := buildTreeNodes(root, "", false, i18n.NopTranslator{}, "")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node (public only), got %d", len(nodes))
	}
	if nodes[0].Label != "services.main.migrate" {
		t.Errorf("expected 'services.main.migrate', got %q", nodes[0].Label)
	}
}

func TestBuildTreeNodes_includePrivate(t *testing.T) {
	root := &usercommands.GroupNode{
		Commands: []*usercommands.CommandDef{
			{LocalName: "migrate", Type: usercommands.CommandTypeServiceExec, Private: false},
			{LocalName: "secret", Type: usercommands.CommandTypeShell, Private: true},
		},
	}
	nodes := buildTreeNodes(root, "", true, i18n.NopTranslator{}, "")
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes (including private), got %d", len(nodes))
	}
}

func TestBuildTreeNodes_nestedGroups(t *testing.T) {
	main := &usercommands.GroupNode{
		ID:   "services.main",
		Name: "main",
		Commands: []*usercommands.CommandDef{
			{ID: "services.main.migrate", LocalName: "migrate", Type: usercommands.CommandTypeServiceExec},
		},
	}
	services := &usercommands.GroupNode{
		ID:       "services",
		Name:     "services",
		Children: []*usercommands.GroupNode{main},
	}
	root := &usercommands.GroupNode{Children: []*usercommands.GroupNode{services}}

	nodes := buildTreeNodes(root, "", false, i18n.NopTranslator{}, "")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 top-level node, got %d", len(nodes))
	}
	servicesNode := nodes[0]
	if servicesNode.Label != "services" {
		t.Errorf("expected 'services', got %q", servicesNode.Label)
	}
	if len(servicesNode.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(servicesNode.Children))
	}
	mainNode := servicesNode.Children[0]
	if mainNode.Label != "main" {
		t.Errorf("expected 'main', got %q", mainNode.Label)
	}
	if len(mainNode.Children) != 1 {
		t.Fatalf("expected 1 command, got %d", len(mainNode.Children))
	}
	if mainNode.Children[0].Label != "services.main.migrate" {
		t.Errorf("expected 'services.main.migrate', got %q", mainNode.Children[0].Label)
	}
}

func TestBuildTreeNodes_groupFilterMissing(t *testing.T) {
	root := &usercommands.GroupNode{
		Commands: []*usercommands.CommandDef{
			{LocalName: "migrate", Type: usercommands.CommandTypeServiceExec},
		},
	}
	nodes := buildTreeNodes(root, "nonexistent", false, i18n.NopTranslator{}, "")
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes for missing group, got %d", len(nodes))
	}
}

func TestBuildTreeNodes_groupFilter(t *testing.T) {
	main := &usercommands.GroupNode{
		ID:   "services.main",
		Name: "main",
		Commands: []*usercommands.CommandDef{
			{ID: "services.main.migrate", LocalName: "migrate", Type: usercommands.CommandTypeServiceExec},
		},
	}
	services := &usercommands.GroupNode{
		ID:       "services",
		Name:     "services",
		Children: []*usercommands.GroupNode{main},
	}
	root := &usercommands.GroupNode{Children: []*usercommands.GroupNode{services}}

	// Filter to "services.main" — should return migrate directly.
	nodes := buildTreeNodes(root, "services.main", false, i18n.NopTranslator{}, "")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node for services.main filter, got %d", len(nodes))
	}
	if nodes[0].Label != "services.main.migrate" {
		t.Errorf("expected 'services.main.migrate', got %q", nodes[0].Label)
	}
}

func TestBuildTreeNodes_privateGroupHiddenWhenAllPrivate(t *testing.T) {
	// A sub-group containing only private commands should be hidden in non-all mode.
	inner := &usercommands.GroupNode{
		ID:   "db",
		Name: "db",
		Commands: []*usercommands.CommandDef{
			{LocalName: "create", Type: usercommands.CommandTypeServiceExec, Private: true},
		},
	}
	root := &usercommands.GroupNode{Children: []*usercommands.GroupNode{inner}}
	nodes := buildTreeNodes(root, "", false, i18n.NopTranslator{}, "")
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes (private-only group should be hidden), got %d", len(nodes))
	}
}

// --- commandDefToTreeNode ---

func TestCommandDefToTreeNode_public(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:          "services.main.migrate",
		LocalName:   "migrate",
		Type:        usercommands.CommandTypeServiceExec,
		Description: "Run migrations",
		Private:     false,
	}
	node := commandDefToTreeNode(def, i18n.NopTranslator{}, "")
	if node.Label != "services.main.migrate" {
		t.Errorf("label: want %q, got %q", "services.main.migrate", node.Label)
	}
	if node.Desc != "Run migrations" {
		t.Errorf("desc: want %q, got %q", "Run migrations", node.Desc)
	}
	// Should have exactly 1 tag: the type
	if len(node.Tags) != 1 || node.Tags[0] != "service_exec" {
		t.Errorf("tags: want [service_exec], got %v", node.Tags)
	}
}

func TestCommandDefToTreeNode_private(t *testing.T) {
	def := &usercommands.CommandDef{
		LocalName: "create",
		Type:      usercommands.CommandTypeServiceExec,
		Private:   true,
	}
	node := commandDefToTreeNode(def, i18n.NopTranslator{}, "")
	if len(node.Tags) != 2 {
		t.Fatalf("expected 2 tags (private + type), got %v", node.Tags)
	}
	if node.Tags[0] != "private" {
		t.Errorf("first tag should be 'private', got %q", node.Tags[0])
	}
	if node.Tags[1] != "service_exec" {
		t.Errorf("second tag should be 'service_exec', got %q", node.Tags[1])
	}
}

// --- printInspect ---

func TestPrintCommandInspect_workflow(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:          "services.main.bootstrap",
		Type:        usercommands.CommandTypeWorkflow,
		Description: "Full bootstrap workflow",
		Steps: []usercommands.WorkflowStep{
			{Command: "services.main.composer-install"},
			{Command: "services.main.migrate"},
		},
	}
	buf := &testBuf{}
	printInspect(buf, def, nil, nil, i18n.NopTranslator{}, "")
	out := buf.String()
	if !contains(out, "services.main.bootstrap") {
		t.Errorf("output should contain command ID")
	}
	if !contains(out, "workflow") {
		t.Errorf("output should contain type")
	}
	if !contains(out, "Full bootstrap workflow") {
		t.Errorf("output should contain description")
	}
	if !contains(out, "services.main.composer-install") {
		t.Errorf("output should contain step references")
	}
}

func TestPrintCommandInspect_serviceExec(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:      "services.main.migrate",
		Type:    usercommands.CommandTypeServiceExec,
		Service: "app-main",
		Mode:    usercommands.ExecModeExecOrRun,
		Cmd:     "php artisan migrate",
	}
	buf := &testBuf{}
	printInspect(buf, def, nil, nil, i18n.NopTranslator{}, "")
	out := buf.String()
	if !contains(out, "service_exec") {
		t.Errorf("output should contain type: %s", out)
	}
	if !contains(out, "app-main") {
		t.Errorf("output should contain service name: %s", out)
	}
	if !contains(out, "exec-or-run") {
		t.Errorf("output should contain mode: %s", out)
	}
}

func TestPrintCommandInspect_withParams(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:   "services.main.cli",
		Type: usercommands.CommandTypeShell,
		Cmd:  "echo hello",
		Params: map[string]usercommands.ParamDef{
			"env": {Type: usercommands.ParamTypeString, Description: "target env", Required: true},
		},
	}
	buf := &testBuf{}
	printInspect(buf, def, nil, nil, i18n.NopTranslator{}, "")
	out := buf.String()
	if !contains(out, "Params") {
		t.Errorf("output should contain Params section: %s", out)
	}
	if !contains(out, "env") {
		t.Errorf("output should contain param name: %s", out)
	}
	if !contains(out, "required") {
		t.Errorf("output should indicate required param: %s", out)
	}
}

func TestPrintCommandInspect_withConfirmation(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:               "db.reset",
		Type:             usercommands.CommandTypeShell,
		Cmd:              "echo reset",
		Confirmation:     true,
		ConfirmationText: "Drop database?",
	}
	buf := &testBuf{}
	printInspect(buf, def, nil, nil, i18n.NopTranslator{}, "")
	out := buf.String()
	if !contains(out, "confirmation") {
		t.Errorf("output should contain confirmation flag: %s", out)
	}
	if !contains(out, "Drop database?") {
		t.Errorf("output should contain confirmation text: %s", out)
	}
}

func TestPrintCommandInspect_withMessages(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:   "db.create",
		Type: usercommands.CommandTypeShell,
		Cmd:  "echo create",
		Messages: usercommands.CommandMessages{
			Success: "Database created.",
			Error:   "Database create failed.",
		},
	}
	buf := &testBuf{}
	printInspect(buf, def, nil, nil, i18n.NopTranslator{}, "")
	out := buf.String()
	if !contains(out, "Messages") {
		t.Errorf("output should contain Messages section: %s", out)
	}
	if !contains(out, "Database created.") {
		t.Errorf("output should contain success message: %s", out)
	}
	if !contains(out, "Database create failed.") {
		t.Errorf("output should contain error message: %s", out)
	}
}

func TestPrintCommandInspect_daemonStart_derivedFromLine(t *testing.T) {
	autoRemove := true
	def := &usercommands.CommandDef{
		ID:                "services.main.queue.start",
		Type:              usercommands.CommandTypeBuiltin,
		Cmd:               "docker_daemon_start",
		DerivedFromDaemon: "services.main.queue",
		SourceDaemon: &model.DaemonSpec{
			ContainerTemplate: "php_queue_${param.name}",
			OnAlreadyRunning:  "error",
			AutoRemove:        &autoRemove,
			StopTimeout:       "10s",
		},
		Params: map[string]usercommands.ParamDef{
			"name": {Type: usercommands.ParamTypeString, Default: "default"},
		},
		With: map[string]any{
			"service": "app-main",
			"user":    "www-data",
			"argv":    []any{"php", "artisan", "queue:listen"},
		},
	}

	// Without cfg: derived-from line + Daemon subsection, no Container subsection.
	buf := &testBuf{}
	printInspect(buf, def, nil, nil, i18n.NopTranslator{}, "")
	out := buf.String()
	if !contains(out, "derived from") || !contains(out, "daemon services.main.queue") {
		t.Errorf("expected 'derived from: daemon services.main.queue' line, got:\n%s", out)
	}
	if !contains(out, "Daemon") || !contains(out, "container_template") || !contains(out, "php_queue_${param.name}") {
		t.Errorf("expected Daemon subsection with container_template, got:\n%s", out)
	}
	if !contains(out, "on_already_running") || !contains(out, "auto_remove") || !contains(out, "stop_timeout") {
		t.Errorf("expected daemon structural fields, got:\n%s", out)
	}
	if !contains(out, "service") || !contains(out, "app-main") {
		t.Errorf("expected service field from With, got:\n%s", out)
	}
	if !contains(out, "argv") || !contains(out, "php artisan queue:listen") {
		t.Errorf("expected argv from With, got:\n%s", out)
	}
	if contains(out, "Container") {
		t.Errorf("Container subsection should be omitted when cfg is nil, got:\n%s", out)
	}

	// With cfg: includes the resolved container name from defaults.
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "my-proj"},
		Raw:     map[string]any{},
	}
	buf2 := &testBuf{}
	printInspect(buf2, def, cfg, nil, i18n.NopTranslator{}, "")
	out2 := buf2.String()
	if !contains(out2, "Container") {
		t.Errorf("expected Container subsection when cfg is non-nil, got:\n%s", out2)
	}
	if !contains(out2, "my-proj-php_queue_default") {
		t.Errorf("expected resolved container name 'my-proj-php_queue_default', got:\n%s", out2)
	}
}

// --- helpers ---

type testBuf struct {
	data []byte
}

func (b *testBuf) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *testBuf) String() string {
	return string(b.data)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}

// --- resolveCommandID ---

// noopSelector is a selectCommandFn that always returns the first item's ID.
// Used to test that the selector is called without actually running a TUI.
func noopSelector(defs []*usercommands.CommandDef, _ string) (string, error) {
	if len(defs) == 0 {
		return "", fmt.Errorf("no commands")
	}
	return defs[0].ID, nil
}

// captureSelector records the title and def IDs it was called with, then returns the first item.
type captureSelector struct {
	calledWith []string
	title      string
}

func (c *captureSelector) selector(defs []*usercommands.CommandDef, title string) (string, error) {
	c.title = title
	for _, d := range defs {
		c.calledWith = append(c.calledWith, d.ID)
	}
	if len(defs) == 0 {
		return "", fmt.Errorf("no commands")
	}
	return defs[0].ID, nil
}

func TestResolveCommandID_exactID(t *testing.T) {
	// When the arg matches a full command ID, return it directly without calling selector.
	selectorCalled := false
	mockSelector := func(defs []*usercommands.CommandDef, title string) (string, error) {
		selectorCalled = true
		return defs[0].ID, nil
	}
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Private: false})

	got, err := resolveCommandID(reg, []string{"db.up"}, false, "", mockSelector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "db.up" {
		t.Errorf("want %q, got %q", "db.up", got)
	}
	if selectorCalled {
		t.Error("selector should NOT be called for exact ID")
	}
}

func TestResolveCommandID_noArg_callsSelector(t *testing.T) {
	// When no arg is given, selector is called with all public usercommands.
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Private: false})
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "app.install", LocalName: "install", Group: "app", Type: usercommands.CommandTypeShell, Private: false})
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.secret", LocalName: "secret", Group: "db", Type: usercommands.CommandTypeShell, Private: true})

	cs := &captureSelector{}
	_, err := resolveCommandID(reg, []string{}, false, "", cs.selector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only public commands should be in selector.
	if len(cs.calledWith) != 2 {
		t.Errorf("expected 2 public commands, got %d: %v", len(cs.calledWith), cs.calledWith)
	}
	for _, id := range cs.calledWith {
		if id == "db.secret" {
			t.Error("private command should not appear in selector")
		}
	}
}

func TestResolveCommandID_noArg_includePrivate(t *testing.T) {
	// When includePrivate is true and no arg, selector gets all usercommands.
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Private: false})
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.secret", LocalName: "secret", Group: "db", Type: usercommands.CommandTypeShell, Private: true})

	cs := &captureSelector{}
	_, err := resolveCommandID(reg, []string{}, true, "", cs.selector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cs.calledWith) != 2 {
		t.Errorf("expected 2 commands (include private), got %d: %v", len(cs.calledWith), cs.calledWith)
	}
}

func TestResolveCommandID_groupPrefix_callsFilteredSelector(t *testing.T) {
	// When arg is a group prefix, selector is called with only those group usercommands.
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "services.main.migrate", LocalName: "migrate", Group: "services.main", Type: usercommands.CommandTypeServiceExec, Private: false})
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "services.main.seed", LocalName: "seed", Group: "services.main", Type: usercommands.CommandTypeServiceExec, Private: false})
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Private: false})

	cs := &captureSelector{}
	_, err := resolveCommandID(reg, []string{"services.main"}, false, "", cs.selector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only services.main.* commands should be in selector.
	if len(cs.calledWith) != 2 {
		t.Errorf("expected 2 commands in group, got %d: %v", len(cs.calledWith), cs.calledWith)
	}
	for _, id := range cs.calledWith {
		if !contains(id, "services.main.") {
			t.Errorf("unexpected command in group selector: %q", id)
		}
	}
	// Title should include the group prefix.
	if !contains(cs.title, "services.main") {
		t.Errorf("title should contain group prefix, got %q", cs.title)
	}
}

func TestResolveCommandID_titlePrefixesProjectName(t *testing.T) {
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Private: false})
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.down", LocalName: "down", Group: "db", Type: usercommands.CommandTypeShell, Private: false})

	t.Run("no_arg", func(t *testing.T) {
		cs := &captureSelector{}
		if _, err := resolveCommandID(reg, []string{}, false, "laravel", cs.selector); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cs.title != "Devbox · laravel · Commands" {
			t.Errorf("title = %q, want %q", cs.title, "Devbox · laravel · Commands")
		}
	})

	t.Run("group_prefix", func(t *testing.T) {
		cs := &captureSelector{}
		if _, err := resolveCommandID(reg, []string{"db"}, false, "laravel", cs.selector); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cs.title != "Devbox · laravel · Commands (db)" {
			t.Errorf("title = %q, want %q", cs.title, "Devbox · laravel · Commands (db)")
		}
	})

	t.Run("empty_project_name", func(t *testing.T) {
		cs := &captureSelector{}
		if _, err := resolveCommandID(reg, []string{}, false, "", cs.selector); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cs.title != "Devbox · Commands" {
			t.Errorf("title = %q, want %q", cs.title, "Devbox · Commands")
		}
	})
}

func TestPrintRunHeader(t *testing.T) {
	cases := []struct {
		name string
		def  *usercommands.CommandDef
		want []string // substrings that must all be present
		not  []string // substrings that must NOT be present
	}{
		{
			name: "id_type_description",
			def: &usercommands.CommandDef{
				ID:          "db.cli",
				Type:        usercommands.CommandTypeServiceExec,
				Description: "Connect to the database",
			},
			want: []string{"db.cli", "[service_exec]", "Connect to the database"},
		},
		{
			name: "no_description",
			def:  &usercommands.CommandDef{ID: "app.up", Type: usercommands.CommandTypeShell},
			want: []string{"app.up", "[shell]"},
		},
		{
			name: "id_only",
			def:  &usercommands.CommandDef{ID: "lonely"},
			want: []string{"lonely"},
			not:  []string{"[", "]"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			printRunHeader(&buf, tc.def, i18n.NopTranslator{}, "")
			got := buf.String()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("output missing %q\n---\n%s", w, got)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(got, n) {
					t.Errorf("output unexpectedly contains %q\n---\n%s", n, got)
				}
			}
			if !strings.HasSuffix(got, "\n") {
				t.Errorf("output must end with newline")
			}
		})
	}
}

func TestResolveCommandID_unknownArg_error(t *testing.T) {
	// When arg is neither a command ID nor a group prefix, return an error.
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Private: false})

	_, err := resolveCommandID(reg, []string{"nonexistent"}, false, "", noopSelector)
	if err == nil {
		t.Fatal("expected error for unknown arg, got nil")
	}
}

func TestResolveCommandID_noArg_emptyRegistry_error(t *testing.T) {
	// When no arg and no commands exist, return an error.
	reg := usercommands.NewEmptyRegistry()
	_, err := resolveCommandID(reg, []string{}, false, "", noopSelector)
	if err == nil {
		t.Fatal("expected error for empty registry, got nil")
	}
}

func TestResolveCommandID_nonInteractiveSelector_noArg_returnsError(t *testing.T) {
	// When a non-TTY selector is passed and no exact ID is given, it returns an error.
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Private: false})

	nonTTYSelector := func(_ []*usercommands.CommandDef, _ string) (string, error) {
		return "", fmt.Errorf("no exact command ID given; pass a full command ID or run in an interactive terminal")
	}
	_, err := resolveCommandID(reg, []string{}, false, "", nonTTYSelector)
	if err == nil {
		t.Fatal("expected error from non-TTY selector, got nil")
	}
}

func TestResolveCommandID_nonInteractiveSelector_groupPrefix_returnsError(t *testing.T) {
	// When a non-TTY selector is passed and a group prefix is given (not exact ID), it returns an error.
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Private: false})

	nonTTYSelector := func(_ []*usercommands.CommandDef, _ string) (string, error) {
		return "", fmt.Errorf("no exact command ID given; pass a full command ID or run in an interactive terminal")
	}
	_, err := resolveCommandID(reg, []string{"db"}, false, "", nonTTYSelector)
	if err == nil {
		t.Fatal("expected error from non-TTY selector for group prefix, got nil")
	}
}

func TestResolveCommandID_nonInteractiveSelector_exactID_succeeds(t *testing.T) {
	// When an exact ID is given, the selector is never called even in non-TTY mode.
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Private: false})

	selectorCalled := false
	nonTTYSelector := func(_ []*usercommands.CommandDef, _ string) (string, error) {
		selectorCalled = true
		return "", fmt.Errorf("not interactive")
	}
	got, err := resolveCommandID(reg, []string{"db.up"}, false, "", nonTTYSelector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "db.up" {
		t.Errorf("want %q, got %q", "db.up", got)
	}
	if selectorCalled {
		t.Error("selector must not be called for exact ID")
	}
}

// --- commands parent command surface ---

func TestCommandCmd_AcceptsOptionalArg(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := NewCmd("", flags)
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("0 args should be accepted: %v", err)
	}
	if err := cmd.Args(cmd, []string{"db.up"}); err != nil {
		t.Errorf("1 arg should be accepted: %v", err)
	}
	if err := cmd.Args(cmd, []string{"db.up", "extra"}); err == nil {
		t.Error("2 args should be rejected")
	}
}

func TestCommandCmd_HasFlags(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := NewCmd("", flags)

	cases := []struct {
		name      string
		flag      string
		shorthand string
		kind      string
	}{
		{name: "yes", flag: "yes", shorthand: "y", kind: "bool"},
		{name: "inspect", flag: "inspect", shorthand: "i", kind: "bool"},
		{name: "set", flag: "set", shorthand: "", kind: "stringArray"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := cmd.Flags().Lookup(tc.flag)
			if f == nil {
				t.Fatalf("flag %q not found", tc.flag)
			}
			if f.Shorthand != tc.shorthand {
				t.Errorf("flag %q shorthand: want %q, got %q", tc.flag, tc.shorthand, f.Shorthand)
			}
			if f.Value.Type() != tc.kind {
				t.Errorf("flag %q type: want %q, got %q", tc.flag, tc.kind, f.Value.Type())
			}
		})
	}
}

func TestCommandCmd_HasCmdAlias(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := NewCmd("", flags)
	if !slices.Contains(cmd.Aliases, "cmd") {
		t.Errorf("commands command should have 'cmd' alias, got %v", cmd.Aliases)
	}
}

func TestCommandCmd_StillHasListSubcommand(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := NewCmd("", flags)
	var found *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "list" {
			found = sub
			break
		}
	}
	if found == nil {
		t.Fatal("commands command should still have a 'list' subcommand")
	}
}

func TestCommandCmd_NoRunOrInspectSubcommand(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := NewCmd("", flags)
	for _, sub := range cmd.Commands() {
		if sub.Name() == "run" || sub.Name() == "inspect" {
			t.Errorf("subcommand %q should have been removed", sub.Name())
		}
	}
}

func TestCommandCmd_InspectWithoutID_Error(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := NewCmd("", flags)
	if err := cmd.Flags().Set("inspect", "true"); err != nil {
		t.Fatal(err)
	}
	err := cmd.RunE(cmd, []string{})
	if err == nil {
		t.Fatal("expected error when --inspect is set without an id")
	}
	if !contains(err.Error(), "id required with --inspect") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCommandCmd_InspectAndSet_MutuallyExclusive(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	parent := &cobra.Command{Use: "test"}
	parent.AddCommand(NewCmd("", flags))
	parent.SetArgs([]string{"commands", "--inspect", "--set", "k=v", "db.up"})
	parent.SetOut(&testBuf{})
	parent.SetErr(&testBuf{})
	if err := parent.Execute(); err == nil {
		t.Fatal("expected mutual-exclusion error for --inspect + --set")
	}
}

func TestCommandCmd_InspectAndYes_MutuallyExclusive(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	parent := &cobra.Command{Use: "test"}
	parent.AddCommand(NewCmd("", flags))
	parent.SetArgs([]string{"commands", "--inspect", "--yes", "db.up"})
	parent.SetOut(&testBuf{})
	parent.SetErr(&testBuf{})
	if err := parent.Execute(); err == nil {
		t.Fatal("expected mutual-exclusion error for --inspect + --yes")
	}
}

func TestCommandCmd_AliasDispatch(t *testing.T) {
	// Verify the 'cmd' alias resolves through cobra parent dispatch.
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	parent := &cobra.Command{Use: "devbox"}
	parent.AddCommand(NewCmd("", flags))

	parent.SetArgs([]string{"cmd", "--help"})
	parent.SetOut(&testBuf{})
	parent.SetErr(&testBuf{})
	if err := parent.Execute(); err != nil {
		t.Fatalf("alias dispatch failed: %v", err)
	}
}

// --- signal-aware context ------------------------------------------------

// TestCommandCmd_InspectSkipsSignalSetup asserts that the inspect route never
// calls notifyContext — only the run route registers signal handlers.
func TestCommandCmd_InspectSkipsSignalSetup(t *testing.T) {
	origNotify := notifyContext
	t.Cleanup(func() { notifyContext = origNotify })

	var calls int
	notifyContext = func(parent context.Context, sigs ...os.Signal) (context.Context, context.CancelFunc) {
		calls++
		return context.WithCancel(parent)
	}

	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := NewCmd("", flags)
	if err := cmd.Flags().Set("inspect", "true"); err != nil {
		t.Fatal(err)
	}
	// Unknown ID is fine; we only care that notifyContext was not called.
	_ = cmd.RunE(cmd, []string{"does.not.exist"})

	if calls != 0 {
		t.Errorf("notifyContext must not be called in inspect route; got %d calls", calls)
	}
}

// TestCommandCmd_SignalAwareContext verifies that the run route wraps the
// cobra context with notifyContext(SIGINT, SIGTERM) and forwards the wrapped
// context to runUserCommand.
func TestCommandCmd_SignalAwareContext(t *testing.T) {
	// Minimal temp project so config + registry loads succeed.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdDir := filepath.Join(dir, "devbox", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "db.yml"), []byte("commands:\n  up:\n    type: shell\n    cmd: echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Stub notifyContext to capture the signals and hand back a cancellable ctx.
	origNotify := notifyContext
	t.Cleanup(func() { notifyContext = origNotify })
	signalsCh := make(chan []os.Signal, 1)
	cancelCh := make(chan context.CancelFunc, 1)
	notifyContext = func(parent context.Context, sigs ...os.Signal) (context.Context, context.CancelFunc) {
		cp := make([]os.Signal, len(sigs))
		copy(cp, sigs)
		signalsCh <- cp
		ctx, cancel := context.WithCancel(parent)
		cancelCh <- cancel
		return ctx, cancel
	}

	// Stub runUserCommand to capture the ctx it receives and block until released.
	origRun := runUserCommand
	t.Cleanup(func() { runUserCommand = origRun })
	receivedCtx := make(chan context.Context, 1)
	releaseCh := make(chan struct{})
	runUserCommand = func(ctx context.Context, rc usercommands.RunContext) error {
		receivedCtx <- ctx
		<-releaseCh
		return nil
	}

	// Non-TTY so no selector fires; pass exact ID as arg.
	origInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = origInteractive })
	widgets.IsInteractiveFn = func(io.Reader) bool { return false }

	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}
	cmd := NewCmd("", flags)
	cmd.SetContext(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.RunE(cmd, []string{"db.up"})
	}()

	// 1. Assert notifyContext was called with exactly SIGINT and SIGTERM.
	select {
	case sigs := <-signalsCh:
		want := []os.Signal{syscall.SIGINT, syscall.SIGTERM}
		if len(sigs) != len(want) {
			t.Errorf("signal count: want %d, got %d (%v)", len(want), len(sigs), sigs)
		} else {
			for i, s := range want {
				if sigs[i] != s {
					t.Errorf("sig[%d]: want %v, got %v", i, s, sigs[i])
				}
			}
		}
	case <-time.After(3 * time.Second):
		close(releaseCh)
		t.Fatal("notifyContext was not called within timeout")
	}

	cancel := <-cancelCh

	// 2. The ctx forwarded to runUserCommand is the wrapped one.
	var runCtx context.Context
	select {
	case runCtx = <-receivedCtx:
	case <-time.After(3 * time.Second):
		close(releaseCh)
		t.Fatal("runUserCommand was not called within timeout")
	}

	// 3. Cancelling via the seam propagates to the runner's ctx.
	cancel()
	select {
	case <-runCtx.Done():
		// expected
	case <-time.After(time.Second):
		close(releaseCh)
		t.Fatal("ctx not cancelled after notifyContext cancel")
	}

	// 4. Let runUserCommand complete and collect RunE result.
	close(releaseCh)
	if err := <-errCh; err != nil {
		t.Fatalf("RunE returned error: %v", err)
	}
}

// --- buildAskFields ---------------------------------------------------------

func makeDef(params map[string]model.ParamDef) *usercommands.CommandDef {
	return &usercommands.CommandDef{
		ID:     "test.cmd",
		Params: params,
	}
}

func TestBuildAskFields_InputWidget(t *testing.T) {
	def := makeDef(map[string]model.ParamDef{
		"name": {Type: model.ParamTypeString},
	})
	fields, err := buildAskFields(def, map[string]string{}, map[string]string{}, i18n.NopTranslator{}, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(fields))
	}
	if fields[0].Kind != ask.FieldInput {
		t.Errorf("expected FieldInput, got %v", fields[0].Kind)
	}
	if fields[0].Key != "name" {
		t.Errorf("expected key=name, got %q", fields[0].Key)
	}
}

func TestBuildAskFields_ConfirmWidget(t *testing.T) {
	def := makeDef(map[string]model.ParamDef{
		"verbose": {Type: model.ParamTypeBool},
	})
	fields, err := buildAskFields(def, map[string]string{}, map[string]string{}, i18n.NopTranslator{}, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(fields))
	}
	if fields[0].Kind != ask.FieldConfirm {
		t.Errorf("expected FieldConfirm, got %v", fields[0].Kind)
	}
}

func TestBuildAskFields_SelectWidget(t *testing.T) {
	def := makeDef(map[string]model.ParamDef{
		"env": {
			Type:    model.ParamTypeString,
			Widget:  model.WidgetSelect,
			Options: &model.ParamOptions{Static: []model.OptionItem{{Value: "dev", Label: "Dev"}, {Value: "prod", Label: "Prod"}}},
		},
	})
	opts := map[string][]model.OptionItem{
		"env": {{Value: "dev", Label: "Dev"}, {Value: "prod", Label: "Prod"}},
	}
	fields, err := buildAskFields(def, map[string]string{"env": "dev"}, map[string]string{}, i18n.NopTranslator{}, "", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(fields))
	}
	if fields[0].Kind != ask.FieldSelect {
		t.Errorf("expected FieldSelect, got %v", fields[0].Kind)
	}
	if len(fields[0].Options) != 2 {
		t.Errorf("expected 2 options, got %d", len(fields[0].Options))
	}
	if fields[0].Options[0].Label != "Dev" {
		t.Errorf("expected label=Dev, got %q", fields[0].Options[0].Label)
	}
}

func TestBuildAskFields_MultiselectWidget_DefaultSplit(t *testing.T) {
	def := makeDef(map[string]model.ParamDef{
		"tags": {
			Type:   model.ParamTypeString,
			Widget: model.WidgetMultiselect,
			Options: &model.ParamOptions{
				Static: []model.OptionItem{
					{Value: "a", Label: "A"},
					{Value: "b", Label: "B"},
					{Value: "c", Label: "C"},
				},
			},
		},
	})
	opts := map[string][]model.OptionItem{
		"tags": {{Value: "a", Label: "A"}, {Value: "b", Label: "B"}, {Value: "c", Label: "C"}},
	}
	// Default is "a b" — should split into ["a", "b"] as pre-selected values.
	fields, err := buildAskFields(def, map[string]string{"tags": "a b"}, map[string]string{}, i18n.NopTranslator{}, "", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(fields))
	}
	if fields[0].Kind != ask.FieldMultiselect {
		t.Errorf("expected FieldMultiselect, got %v", fields[0].Kind)
	}
	if len(fields[0].Defaults) != 2 || fields[0].Defaults[0] != "a" || fields[0].Defaults[1] != "b" {
		t.Errorf("expected Defaults=[a b], got %v", fields[0].Defaults)
	}
}

func TestBuildAskFields_MultiselectWidget_CustomSeparatorSplit(t *testing.T) {
	def := makeDef(map[string]model.ParamDef{
		"tags": {
			Type:      model.ParamTypeString,
			Widget:    model.WidgetMultiselect,
			Separator: ",",
			Options:   &model.ParamOptions{Static: []model.OptionItem{{Value: "a"}, {Value: "b"}}},
		},
	})
	opts := map[string][]model.OptionItem{
		"tags": {{Value: "a", Label: "a"}, {Value: "b", Label: "b"}},
	}
	fields, err := buildAskFields(def, map[string]string{"tags": "a,b"}, map[string]string{}, i18n.NopTranslator{}, "", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields[0].Defaults) != 2 || fields[0].Defaults[0] != "a" || fields[0].Defaults[1] != "b" {
		t.Errorf("expected Defaults=[a b] with comma sep, got %v", fields[0].Defaults)
	}
}

func TestBuildAskFields_SelectEmptyOptions_Required_Error(t *testing.T) {
	def := makeDef(map[string]model.ParamDef{
		"env": {
			Type:     model.ParamTypeString,
			Widget:   model.WidgetSelect,
			Required: true,
			Options:  &model.ParamOptions{From: "envs"},
		},
	})
	_, err := buildAskFields(def, map[string]string{}, map[string]string{}, i18n.NopTranslator{}, "", map[string][]model.OptionItem{"env": {}})
	if err == nil {
		t.Fatal("expected error for required select with empty options")
	}
}

func TestBuildAskFields_SelectEmptyOptions_Optional_Skipped(t *testing.T) {
	def := makeDef(map[string]model.ParamDef{
		"env": {
			Type:    model.ParamTypeString,
			Widget:  model.WidgetSelect,
			Options: &model.ParamOptions{Static: []model.OptionItem{}},
		},
	})
	fields, err := buildAskFields(def, map[string]string{}, map[string]string{}, i18n.NopTranslator{}, "", map[string][]model.OptionItem{"env": {}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 0 {
		t.Errorf("expected 0 fields (optional empty options skipped), got %d", len(fields))
	}
}

func TestBuildAskFields_SetEscapeHatch_SkipsField(t *testing.T) {
	// When a --set value is provided for a select with empty options, the field is skipped
	// (escape hatch: user bypasses the form).
	def := makeDef(map[string]model.ParamDef{
		"env": {
			Type:     model.ParamTypeString,
			Widget:   model.WidgetSelect,
			Required: true,
			Options:  &model.ParamOptions{From: "envs"},
		},
	})
	fields, err := buildAskFields(
		def,
		map[string]string{"env": "staging"}, // prefilled
		map[string]string{"env": "staging"}, // provided via --set
		i18n.NopTranslator{}, "",
		map[string][]model.OptionItem{"env": {}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 0 {
		t.Errorf("expected field skipped via --set escape hatch, got %d fields", len(fields))
	}
}

type mockTranslator struct {
	optionLabels map[string]string // key format: "commandID:paramName:optionValue"
}

func (m *mockTranslator) CommandDescription(_, _, fallback string) string {
	return fallback
}

func (m *mockTranslator) CommandConfirmationText(_, _, fallback string) string {
	return fallback
}

func (m *mockTranslator) ParamDescription(_, _, _, fallback string) string {
	return fallback
}

func (m *mockTranslator) GroupTitle(_, _, fallback string) string {
	return fallback
}

func (m *mockTranslator) GroupDescription(_, _, fallback string) string {
	return fallback
}

func (m *mockTranslator) T(_, _, fallback string) string {
	return fallback
}

func (m *mockTranslator) CommandSuccessMessage(_, _, fallback string) string {
	return fallback
}

func (m *mockTranslator) CommandErrorMessage(_, _, fallback string) string {
	return fallback
}

func (m *mockTranslator) ParamOptionLabel(_, commandID, paramName, optionValue, fallback string) string {
	key := commandID + ":" + paramName + ":" + optionValue
	if translated, ok := m.optionLabels[key]; ok {
		return translated
	}
	return fallback
}

func TestBuildAskFields_OptionLabelTranslation(t *testing.T) {
	def := makeDef(map[string]model.ParamDef{
		"env": {
			Type:    model.ParamTypeString,
			Widget:  model.WidgetSelect,
			Options: &model.ParamOptions{Static: []model.OptionItem{{Value: "prod", Label: "Production"}}},
		},
	})
	opts := map[string][]model.OptionItem{
		"env": {{Value: "prod", Label: "Production"}},
	}

	translator := &mockTranslator{
		optionLabels: map[string]string{
			"test.cmd:env:prod": "Продакшн",
		},
	}

	fields, err := buildAskFields(def, map[string]string{}, map[string]string{}, translator, "en", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(fields))
	}
	if len(fields[0].Options) != 1 {
		t.Errorf("expected 1 option, got %d", len(fields[0].Options))
	}
	// The label should be translated to the value from the mockTranslator
	if fields[0].Options[0].Label != "Продакшн" {
		t.Errorf("expected translated label 'Продакшн', got %q", fields[0].Options[0].Label)
	}
}

// --- mergeAnswers -----------------------------------------------------------

func TestMergeAnswers_InputSelect_Passthrough(t *testing.T) {
	defs := map[string]model.ParamDef{
		"env":  {Type: model.ParamTypeString},
		"name": {Type: model.ParamTypeString},
	}
	res := ask.NewResultForTest(map[string]any{"env": "prod"})
	prev := map[string]string{"env": "dev", "name": "alice"}

	out := mergeAnswers(res, defs, prev)
	if out["env"] != "prod" {
		t.Errorf("env: expected prod, got %q", out["env"])
	}
	if out["name"] != "alice" {
		t.Errorf("name: expected alice (unchanged), got %q", out["name"])
	}
}

func TestMergeAnswers_Multiselect_DefaultSeparator(t *testing.T) {
	defs := map[string]model.ParamDef{
		"tags": {Type: model.ParamTypeString, Widget: model.WidgetMultiselect},
	}
	res := ask.NewResultForTest(map[string]any{"tags": []string{"a", "b", "c"}})
	out := mergeAnswers(res, defs, map[string]string{})
	if out["tags"] != "a b c" {
		t.Errorf("expected 'a b c', got %q", out["tags"])
	}
}

func TestMergeAnswers_Multiselect_CustomSeparator(t *testing.T) {
	defs := map[string]model.ParamDef{
		"tags": {Type: model.ParamTypeString, Widget: model.WidgetMultiselect, Separator: ","},
	}
	res := ask.NewResultForTest(map[string]any{"tags": []string{"x", "y"}})
	out := mergeAnswers(res, defs, map[string]string{})
	if out["tags"] != "x,y" {
		t.Errorf("expected 'x,y', got %q", out["tags"])
	}
}

func TestMergeAnswers_Confirm_TrueString(t *testing.T) {
	defs := map[string]model.ParamDef{
		"verbose": {Type: model.ParamTypeBool},
	}
	res := ask.NewResultForTest(map[string]any{"verbose": true})
	out := mergeAnswers(res, defs, map[string]string{})
	if out["verbose"] != "true" {
		t.Errorf("expected 'true', got %q", out["verbose"])
	}
}

func TestMergeAnswers_Confirm_FalseString(t *testing.T) {
	defs := map[string]model.ParamDef{
		"verbose": {Type: model.ParamTypeBool},
	}
	res := ask.NewResultForTest(map[string]any{"verbose": false})
	out := mergeAnswers(res, defs, map[string]string{})
	if out["verbose"] != "false" {
		t.Errorf("expected 'false', got %q", out["verbose"])
	}
}

func TestMergeAnswers_SkippedFieldPreservesDefault(t *testing.T) {
	// Fields not in the form result (skipped by buildAskFields) are preserved
	// from prevValues unchanged.
	defs := map[string]model.ParamDef{
		"env": {Type: model.ParamTypeString},
	}
	// Result has no "env" key — field was skipped.
	res := ask.NewResultForTest(map[string]any{})
	prev := map[string]string{"env": "staging"}
	out := mergeAnswers(res, defs, prev)
	if out["env"] != "staging" {
		t.Errorf("expected env=staging preserved from prev, got %q", out["env"])
	}
}
