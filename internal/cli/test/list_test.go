package test

import (
	"bytes"
	"encoding/json"
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
