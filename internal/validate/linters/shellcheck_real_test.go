package linters

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestShellcheckRealBinary exercises BuildArgs against the real shellcheck
// binary, when available, and asserts that captured stdout parses as JSON.
// This guards against future shellcheck flag-precedence drift.
func TestShellcheckRealBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-binary smoke test in -short mode")
	}
	bin, err := exec.LookPath("shellcheck")
	if err != nil {
		t.Skipf("shellcheck not on PATH: %v", err)
	}

	a := NewShellcheck()
	script, err := filepath.Abs(filepath.Join("testdata", "shellcheck", "sample.sh"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	args := a.BuildArgs([]string{script}, nil)

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run() // non-zero exit on findings is normal

	if stdout.Len() == 0 {
		t.Fatalf("real shellcheck produced no stdout (stderr=%q)", stderr.String())
	}
	var arr []shellcheckComment
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &arr); err != nil {
		t.Fatalf("real shellcheck stdout not JSON array: %v\nstdout=%q", err, stdout.String())
	}
}
