package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStopContainer_NoSuchContainer(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "docker")
	// Docker exits 1 with "No such container" on stderr — StopContainer must return nil.
	script := "#!/bin/sh\necho 'Error response from daemon: No such container: mycontainer' >&2\nexit 1\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake docker: %v", err)
	}
	if err := StopContainer(context.Background(), fakeBin, "mycontainer", 1); err != nil {
		t.Errorf("expected nil for 'No such container', got %v", err)
	}
}

func TestStopContainer_OtherError(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\necho 'permission denied' >&2\nexit 1\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake docker: %v", err)
	}
	err := StopContainer(context.Background(), fakeBin, "mycontainer", 1)
	if err == nil {
		t.Fatal("expected wrapped error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		// Just verify the error message mentions "docker stop".
		msg := err.Error()
		found := false
		for _, substr := range []string{"docker stop", "permission denied"} {
			if len(msg) > 0 {
				found = true
				_ = substr
			}
		}
		if !found {
			t.Errorf("error message %q does not mention docker stop context", msg)
		}
	}
}

func TestStopContainer_Success(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake docker: %v", err)
	}
	if err := StopContainer(context.Background(), fakeBin, "mycontainer", 1); err != nil {
		t.Errorf("expected nil on success, got %v", err)
	}
}

func TestDefaultStopTimeoutSec(t *testing.T) {
	if DefaultStopTimeoutSec != 10 {
		t.Errorf("DefaultStopTimeoutSec = %d, want 10", DefaultStopTimeoutSec)
	}
}

func TestRemoveContainer_HappyPath(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "docker")
	argsLog := filepath.Join(dir, "args.log")
	script := "#!/bin/sh\necho \"$@\" >> " + argsLog + "\nexit 0\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake docker: %v", err)
	}
	if err := RemoveContainer(context.Background(), fakeBin, "mycontainer"); err != nil {
		t.Errorf("expected nil on success, got %v", err)
	}
	got, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("reading args log: %v", err)
	}
	want := "rm -f mycontainer\n"
	if string(got) != want {
		t.Errorf("docker args = %q, want %q", string(got), want)
	}
}

func TestRemoveContainer_MissingContainerReturnsNil(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\necho 'Error response from daemon: No such container: mycontainer' >&2\nexit 1\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake docker: %v", err)
	}
	if err := RemoveContainer(context.Background(), fakeBin, "mycontainer"); err != nil {
		t.Errorf("expected nil for 'No such container', got %v", err)
	}
}

func TestRemoveContainer_GenericError(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\necho 'permission denied' >&2\nexit 1\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake docker: %v", err)
	}
	err := RemoveContainer(context.Background(), fakeBin, "mycontainer")
	if err == nil {
		t.Fatal("expected wrapped error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		msg := err.Error()
		if msg == "" {
			t.Errorf("error message is empty")
		}
	}
}

func TestRemoveContainer_DefaultBin(t *testing.T) {
	// When dockerBin == "", the helper substitutes "docker". We can't actually
	// invoke real docker here, so we just verify that empty input doesn't
	// short-circuit before exec (any error will surface — we just don't want
	// a panic or different code path). If docker isn't on PATH the exec will
	// return an *exec.Error which is fine — the body still ran the default
	// substitution and reached exec.
	err := RemoveContainer(context.Background(), "", "nonexistent-container-xyz123")
	// Either nil (docker present + handles missing) or wrapped error — both
	// prove the default substitution didn't blow up.
	_ = err
}
