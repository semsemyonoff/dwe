//go:build unix

package tests

import (
	"path/filepath"
	"syscall"
	"testing"
)

// A FIFO must be skipped without being opened: opening one blocks until a
// writer appears, which would hang `dwe validate`.
func TestHostProjectName_FIFOSkipped(t *testing.T) {
	root := hpProject(t, map[string]string{
		"workspace/deploy.yml": "phases:\n  - name: deploy\n    steps:\n      - name: seed\n        type: shell\n        cmd: 'sh scripts/fifo.sh'\n",
		"scripts/.keep":        "",
	})
	if err := syscall.Mkfifo(filepath.Join(root, "scripts", "fifo.sh"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	if diags := runHP(root); len(diags) != 0 {
		t.Fatalf("want no diagnostics, got %v", hpLines(diags))
	}
}
