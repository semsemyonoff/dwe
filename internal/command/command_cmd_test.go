package command

import (
	"fmt"
	"testing"

	"devbox-cli/internal/commands"
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
	root := &commands.GroupNode{}
	nodes := buildTreeNodes(root, "", false)
	if len(nodes) != 0 {
		t.Errorf("expected empty nodes, got %d", len(nodes))
	}
}

func TestBuildTreeNodes_publicCommandsOnly(t *testing.T) {
	root := &commands.GroupNode{
		Commands: []*commands.CommandDef{
			{ID: "services.main.migrate", LocalName: "migrate", Type: commands.CommandTypeServiceExec, Private: false},
			{ID: "services.main.secret", LocalName: "secret", Type: commands.CommandTypeCommand, Private: true},
		},
	}
	nodes := buildTreeNodes(root, "", false)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node (public only), got %d", len(nodes))
	}
	if nodes[0].Label != "services.main.migrate" {
		t.Errorf("expected 'services.main.migrate', got %q", nodes[0].Label)
	}
}

func TestBuildTreeNodes_includePrivate(t *testing.T) {
	root := &commands.GroupNode{
		Commands: []*commands.CommandDef{
			{LocalName: "migrate", Type: commands.CommandTypeServiceExec, Private: false},
			{LocalName: "secret", Type: commands.CommandTypeCommand, Private: true},
		},
	}
	nodes := buildTreeNodes(root, "", true)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes (including private), got %d", len(nodes))
	}
}

func TestBuildTreeNodes_nestedGroups(t *testing.T) {
	main := &commands.GroupNode{
		ID:   "services.main",
		Name: "main",
		Commands: []*commands.CommandDef{
			{ID: "services.main.migrate", LocalName: "migrate", Type: commands.CommandTypeServiceExec},
		},
	}
	services := &commands.GroupNode{
		ID:       "services",
		Name:     "services",
		Children: []*commands.GroupNode{main},
	}
	root := &commands.GroupNode{Children: []*commands.GroupNode{services}}

	nodes := buildTreeNodes(root, "", false)
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
	root := &commands.GroupNode{
		Commands: []*commands.CommandDef{
			{LocalName: "migrate", Type: commands.CommandTypeServiceExec},
		},
	}
	nodes := buildTreeNodes(root, "nonexistent", false)
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes for missing group, got %d", len(nodes))
	}
}

func TestBuildTreeNodes_groupFilter(t *testing.T) {
	main := &commands.GroupNode{
		ID:   "services.main",
		Name: "main",
		Commands: []*commands.CommandDef{
			{ID: "services.main.migrate", LocalName: "migrate", Type: commands.CommandTypeServiceExec},
		},
	}
	services := &commands.GroupNode{
		ID:       "services",
		Name:     "services",
		Children: []*commands.GroupNode{main},
	}
	root := &commands.GroupNode{Children: []*commands.GroupNode{services}}

	// Filter to "services.main" — should return migrate directly.
	nodes := buildTreeNodes(root, "services.main", false)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node for services.main filter, got %d", len(nodes))
	}
	if nodes[0].Label != "services.main.migrate" {
		t.Errorf("expected 'services.main.migrate', got %q", nodes[0].Label)
	}
}

func TestBuildTreeNodes_privateGroupHiddenWhenAllPrivate(t *testing.T) {
	// A sub-group containing only private commands should be hidden in non-all mode.
	inner := &commands.GroupNode{
		ID:   "db",
		Name: "db",
		Commands: []*commands.CommandDef{
			{LocalName: "create", Type: commands.CommandTypeServiceExec, Private: true},
		},
	}
	root := &commands.GroupNode{Children: []*commands.GroupNode{inner}}
	nodes := buildTreeNodes(root, "", false)
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes (private-only group should be hidden), got %d", len(nodes))
	}
}

// --- commandDefToTreeNode ---

func TestCommandDefToTreeNode_public(t *testing.T) {
	def := &commands.CommandDef{
		ID:          "services.main.migrate",
		LocalName:   "migrate",
		Type:        commands.CommandTypeServiceExec,
		Description: "Run migrations",
		Private:     false,
	}
	node := commandDefToTreeNode(def)
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
	def := &commands.CommandDef{
		LocalName: "create",
		Type:      commands.CommandTypeServiceExec,
		Private:   true,
	}
	node := commandDefToTreeNode(def)
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

// --- printCommandInspect ---

func TestPrintCommandInspect_workflow(t *testing.T) {
	def := &commands.CommandDef{
		ID:          "services.main.bootstrap",
		Type:        commands.CommandTypeWorkflow,
		Description: "Full bootstrap workflow",
		Steps: []commands.WorkflowStep{
			{Command: "services.main.composer-install"},
			{Command: "services.main.migrate"},
		},
	}
	buf := &testBuf{}
	printCommandInspect(buf, def)
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
	def := &commands.CommandDef{
		ID:      "services.main.migrate",
		Type:    commands.CommandTypeServiceExec,
		Service: "app-main",
		Mode:    commands.ExecModeExecOrRun,
		Run:     "php artisan migrate",
	}
	buf := &testBuf{}
	printCommandInspect(buf, def)
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
	def := &commands.CommandDef{
		ID:   "services.main.cli",
		Type: commands.CommandTypeCommand,
		Run:  "echo hello",
		Params: map[string]commands.ParamDef{
			"env": {Type: commands.ParamTypeString, Description: "target env", Required: true},
		},
	}
	buf := &testBuf{}
	printCommandInspect(buf, def)
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
func noopSelector(defs []*commands.CommandDef, _ string) (string, error) {
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

func (c *captureSelector) selector(defs []*commands.CommandDef, title string) (string, error) {
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
	mockSelector := func(defs []*commands.CommandDef, title string) (string, error) {
		selectorCalled = true
		return defs[0].ID, nil
	}
	reg := commands.NewEmptyRegistry()
	reg.AddCommandForTest(&commands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: commands.CommandTypeCommand, Private: false})

	got, err := resolveCommandID(reg, []string{"db.up"}, false, mockSelector)
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
	// When no arg is given, selector is called with all public commands.
	reg := commands.NewEmptyRegistry()
	reg.AddCommandForTest(&commands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: commands.CommandTypeCommand, Private: false})
	reg.AddCommandForTest(&commands.CommandDef{ID: "app.install", LocalName: "install", Group: "app", Type: commands.CommandTypeCommand, Private: false})
	reg.AddCommandForTest(&commands.CommandDef{ID: "db.secret", LocalName: "secret", Group: "db", Type: commands.CommandTypeCommand, Private: true})

	cs := &captureSelector{}
	_, err := resolveCommandID(reg, []string{}, false, cs.selector)
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
	// When includePrivate is true and no arg, selector gets all commands.
	reg := commands.NewEmptyRegistry()
	reg.AddCommandForTest(&commands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: commands.CommandTypeCommand, Private: false})
	reg.AddCommandForTest(&commands.CommandDef{ID: "db.secret", LocalName: "secret", Group: "db", Type: commands.CommandTypeCommand, Private: true})

	cs := &captureSelector{}
	_, err := resolveCommandID(reg, []string{}, true, cs.selector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cs.calledWith) != 2 {
		t.Errorf("expected 2 commands (include private), got %d: %v", len(cs.calledWith), cs.calledWith)
	}
}

func TestResolveCommandID_groupPrefix_callsFilteredSelector(t *testing.T) {
	// When arg is a group prefix, selector is called with only those group commands.
	reg := commands.NewEmptyRegistry()
	reg.AddCommandForTest(&commands.CommandDef{ID: "services.main.migrate", LocalName: "migrate", Group: "services.main", Type: commands.CommandTypeServiceExec, Private: false})
	reg.AddCommandForTest(&commands.CommandDef{ID: "services.main.seed", LocalName: "seed", Group: "services.main", Type: commands.CommandTypeServiceExec, Private: false})
	reg.AddCommandForTest(&commands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: commands.CommandTypeCommand, Private: false})

	cs := &captureSelector{}
	_, err := resolveCommandID(reg, []string{"services.main"}, false, cs.selector)
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

func TestResolveCommandID_unknownArg_error(t *testing.T) {
	// When arg is neither a command ID nor a group prefix, return an error.
	reg := commands.NewEmptyRegistry()
	reg.AddCommandForTest(&commands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: commands.CommandTypeCommand, Private: false})

	_, err := resolveCommandID(reg, []string{"nonexistent"}, false, noopSelector)
	if err == nil {
		t.Fatal("expected error for unknown arg, got nil")
	}
}

func TestResolveCommandID_noArg_emptyRegistry_error(t *testing.T) {
	// When no arg and no commands exist, return an error.
	reg := commands.NewEmptyRegistry()
	_, err := resolveCommandID(reg, []string{}, false, noopSelector)
	if err == nil {
		t.Fatal("expected error for empty registry, got nil")
	}
}

func TestResolveCommandID_nonInteractiveSelector_noArg_returnsError(t *testing.T) {
	// When a non-TTY selector is passed and no exact ID is given, it returns an error.
	reg := commands.NewEmptyRegistry()
	reg.AddCommandForTest(&commands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: commands.CommandTypeCommand, Private: false})

	nonTTYSelector := func(_ []*commands.CommandDef, _ string) (string, error) {
		return "", fmt.Errorf("no exact command ID given; pass a full command ID or run in an interactive terminal")
	}
	_, err := resolveCommandID(reg, []string{}, false, nonTTYSelector)
	if err == nil {
		t.Fatal("expected error from non-TTY selector, got nil")
	}
}

func TestResolveCommandID_nonInteractiveSelector_groupPrefix_returnsError(t *testing.T) {
	// When a non-TTY selector is passed and a group prefix is given (not exact ID), it returns an error.
	reg := commands.NewEmptyRegistry()
	reg.AddCommandForTest(&commands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: commands.CommandTypeCommand, Private: false})

	nonTTYSelector := func(_ []*commands.CommandDef, _ string) (string, error) {
		return "", fmt.Errorf("no exact command ID given; pass a full command ID or run in an interactive terminal")
	}
	_, err := resolveCommandID(reg, []string{"db"}, false, nonTTYSelector)
	if err == nil {
		t.Fatal("expected error from non-TTY selector for group prefix, got nil")
	}
}

func TestResolveCommandID_nonInteractiveSelector_exactID_succeeds(t *testing.T) {
	// When an exact ID is given, the selector is never called even in non-TTY mode.
	reg := commands.NewEmptyRegistry()
	reg.AddCommandForTest(&commands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: commands.CommandTypeCommand, Private: false})

	selectorCalled := false
	nonTTYSelector := func(_ []*commands.CommandDef, _ string) (string, error) {
		selectorCalled = true
		return "", fmt.Errorf("not interactive")
	}
	got, err := resolveCommandID(reg, []string{"db.up"}, false, nonTTYSelector)
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

// --- commands run/inspect accept optional arg ---

func TestCommandRunCmd_AcceptsOptionalArg(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newCommandRunCmd(flags)
	// 0 args OK
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("0 args should be accepted: %v", err)
	}
	// 1 arg OK
	if err := cmd.Args(cmd, []string{"db.up"}); err != nil {
		t.Errorf("1 arg should be accepted: %v", err)
	}
	// 2 args should fail
	if err := cmd.Args(cmd, []string{"db.up", "extra"}); err == nil {
		t.Error("2 args should be rejected")
	}
}

func TestCommandInspectCmd_AcceptsOptionalArg(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newCommandInspectCmd(flags)
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
