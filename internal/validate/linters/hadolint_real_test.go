package linters

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestHadolintRealBinary exercises BuildArgs against the real hadolint binary,
// when available, and asserts that captured stdout parses as JSON. This guards
// against future hadolint flag-precedence drift.
func TestHadolintRealBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-binary smoke test in -short mode")
	}
	bin, err := exec.LookPath("hadolint")
	if err != nil {
		t.Skipf("hadolint not on PATH: %v", err)
	}

	a := NewHadolint()
	dockerfile, err := filepath.Abs(filepath.Join("testdata", "hadolint", "Dockerfile"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	args := a.BuildArgs([]string{dockerfile}, nil)

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run() // non-zero exit on findings is normal

	if stdout.Len() == 0 {
		t.Fatalf("real hadolint produced no stdout (stderr=%q)", stderr.String())
	}
	var arr []hadolintFinding
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &arr); err != nil {
		t.Fatalf("real hadolint stdout not JSON array: %v\nstdout=%q", err, stdout.String())
	}
}
