package command

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands"

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
	nodes := buildTreeNodes(root, "", false)
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
	nodes := buildTreeNodes(root, "", false)
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
	nodes := buildTreeNodes(root, "", true)
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
	root := &usercommands.GroupNode{
		Commands: []*usercommands.CommandDef{
			{LocalName: "migrate", Type: usercommands.CommandTypeServiceExec},
		},
	}
	nodes := buildTreeNodes(root, "nonexistent", false)
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
	inner := &usercommands.GroupNode{
		ID:   "db",
		Name: "db",
		Commands: []*usercommands.CommandDef{
			{LocalName: "create", Type: usercommands.CommandTypeServiceExec, Private: true},
		},
	}
	root := &usercommands.GroupNode{Children: []*usercommands.GroupNode{inner}}
	nodes := buildTreeNodes(root, "", false)
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
	def := &usercommands.CommandDef{
		LocalName: "create",
		Type:      usercommands.CommandTypeServiceExec,
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
	printInspect(buf, def)
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
	printInspect(buf, def)
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
	printInspect(buf, def)
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
	printInspect(buf, def)
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
	printInspect(buf, def)
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
	// When no arg is given, selector is called with all public usercommands.
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Private: false})
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "app.install", LocalName: "install", Group: "app", Type: usercommands.CommandTypeShell, Private: false})
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.secret", LocalName: "secret", Group: "db", Type: usercommands.CommandTypeShell, Private: true})

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
	// When includePrivate is true and no arg, selector gets all usercommands.
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Private: false})
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.secret", LocalName: "secret", Group: "db", Type: usercommands.CommandTypeShell, Private: true})

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
	// When arg is a group prefix, selector is called with only those group usercommands.
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "services.main.migrate", LocalName: "migrate", Group: "services.main", Type: usercommands.CommandTypeServiceExec, Private: false})
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "services.main.seed", LocalName: "seed", Group: "services.main", Type: usercommands.CommandTypeServiceExec, Private: false})
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Private: false})

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
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Private: false})

	_, err := resolveCommandID(reg, []string{"nonexistent"}, false, noopSelector)
	if err == nil {
		t.Fatal("expected error for unknown arg, got nil")
	}
}

func TestResolveCommandID_noArg_emptyRegistry_error(t *testing.T) {
	// When no arg and no commands exist, return an error.
	reg := usercommands.NewEmptyRegistry()
	_, err := resolveCommandID(reg, []string{}, false, noopSelector)
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
	_, err := resolveCommandID(reg, []string{}, false, nonTTYSelector)
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
	_, err := resolveCommandID(reg, []string{"db"}, false, nonTTYSelector)
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

// --- commands parent command surface ---

func TestCommandCmd_AcceptsOptionalArg(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newCommandCmd(flags)
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
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newCommandCmd(flags)

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
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newCommandCmd(flags)
	if !slices.Contains(cmd.Aliases, "cmd") {
		t.Errorf("commands command should have 'cmd' alias, got %v", cmd.Aliases)
	}
}

func TestCommandCmd_StillHasListSubcommand(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newCommandCmd(flags)
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
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newCommandCmd(flags)
	for _, sub := range cmd.Commands() {
		if sub.Name() == "run" || sub.Name() == "inspect" {
			t.Errorf("subcommand %q should have been removed", sub.Name())
		}
	}
}

func TestCommandCmd_InspectWithoutID_Error(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newCommandCmd(flags)
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
	flags := &rootFlags{configPath: "devbox.yml"}
	parent := &cobra.Command{Use: "test"}
	parent.AddCommand(newCommandCmd(flags))
	parent.SetArgs([]string{"commands", "--inspect", "--set", "k=v", "db.up"})
	parent.SetOut(&testBuf{})
	parent.SetErr(&testBuf{})
	if err := parent.Execute(); err == nil {
		t.Fatal("expected mutual-exclusion error for --inspect + --set")
	}
}

func TestCommandCmd_InspectAndYes_MutuallyExclusive(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	parent := &cobra.Command{Use: "test"}
	parent.AddCommand(newCommandCmd(flags))
	parent.SetArgs([]string{"commands", "--inspect", "--yes", "db.up"})
	parent.SetOut(&testBuf{})
	parent.SetErr(&testBuf{})
	if err := parent.Execute(); err == nil {
		t.Fatal("expected mutual-exclusion error for --inspect + --yes")
	}
}

func TestCommandCmd_AliasDispatch(t *testing.T) {
	// Verify the 'cmd' alias resolves through cobra parent dispatch.
	flags := &rootFlags{configPath: "devbox.yml"}
	parent := &cobra.Command{Use: "devbox"}
	parent.AddCommand(newCommandCmd(flags))

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

	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newCommandCmd(flags)
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
	// Create a minimal temp project so config.LoadConfig and loadCommandRegistry
	// both succeed.
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
	origInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = origInteractive })
	ui.IsInteractiveFn = func(io.Reader) bool { return false }

	flags := &rootFlags{configPath: cfgPath}
	cmd := newCommandCmd(flags)
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
