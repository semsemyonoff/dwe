package deploy

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/command/cmdctx"

	"github.com/spf13/cobra"
)

// makeMinimalProject writes a bare v2 devbox.yml so config + (empty)
// registry loads succeed. Returns the project root.
func makeMinimalProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	yml := "schema_version: \"2\"\nproject:\n  name: testproject\n  prefix: devbox\n"
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRunDeployPlan_DefaultFormatRendersTitle verifies the table-format
// branch writes the "Deploy plan" section title.
func TestRunDeployPlan_DefaultFormatRendersTitle(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "devbox.yml")}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{}); err != nil {
		t.Fatalf("runDeployPlan: %v", err)
	}
	// RenderSectionTitle wraps the plain text in ANSI styling but the
	// payload is always present.
	if !strings.Contains(buf.String(), "Deploy plan") {
		t.Errorf("output missing 'Deploy plan': %q", buf.String())
	}
}

// TestRunDeployPlan_ShellFormat verifies --format shell emits the shell
// header (which is distinct from the table-format title path).
func TestRunDeployPlan_ShellFormat(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "devbox.yml")}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{Format: "shell"}); err != nil {
		t.Fatalf("runDeployPlan(shell): %v", err)
	}
	if !strings.Contains(buf.String(), "# Deploy plan") {
		t.Errorf("shell output missing '# Deploy plan' header: %q", buf.String())
	}
}

// TestRunDeployPlan_UnknownServiceErrors verifies the explicit guard for
// unknown service names. (Without it, an empty plan would be returned with
// no error.)
func TestRunDeployPlan_UnknownServiceErrors(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "devbox.yml")}

	err := runDeployPlan(context.Background(), &cobra.Command{}, flags, deployPlanOpts{ServiceName: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown service, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("err = %v, want one mentioning the unknown service name", err)
	}
}
