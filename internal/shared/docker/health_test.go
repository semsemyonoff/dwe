package docker

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"devbox-cli/internal/shared/render"
)

func discardWriter() *render.Writer {
	return render.NewWriter(io.Discard)
}

func capturingWriter() (*render.Writer, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return render.NewWriter(buf), buf
}

func TestWaitContainersHealthy_allHealthy(t *testing.T) {
	getHealth := func(id string) (string, error) { return "healthy", nil }
	err := WaitContainersHealthy([]string{"c1", "c2"}, getHealth, 3, time.Millisecond, discardWriter())
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestWaitContainersHealthy_unhealthyReturnsError(t *testing.T) {
	getHealth := func(id string) (string, error) { return "unhealthy", nil }
	err := WaitContainersHealthy([]string{"c1"}, getHealth, 3, time.Millisecond, discardWriter())
	if err == nil {
		t.Error("expected error for unhealthy container, got nil")
	}
	if !strings.Contains(err.Error(), "unhealthy") {
		t.Errorf("expected 'unhealthy' in error, got %v", err)
	}
}

func TestWaitContainersHealthy_noHealthcheckSkipped(t *testing.T) {
	getHealth := func(id string) (string, error) { return "none", nil }
	err := WaitContainersHealthy([]string{"c1", "c2"}, getHealth, 3, time.Millisecond, discardWriter())
	if err != nil {
		t.Errorf("expected nil for no-healthcheck containers, got %v", err)
	}
}

func TestWaitContainersHealthy_startingThenHealthy(t *testing.T) {
	calls := 0
	getHealth := func(id string) (string, error) {
		calls++
		if calls < 3 {
			return "starting", nil
		}
		return "healthy", nil
	}
	err := WaitContainersHealthy([]string{"c1"}, getHealth, 5, time.Millisecond, discardWriter())
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if calls < 3 {
		t.Errorf("expected at least 3 calls, got %d", calls)
	}
}

func TestWaitContainersHealthy_timeout(t *testing.T) {
	getHealth := func(id string) (string, error) { return "starting", nil }
	err := WaitContainersHealthy([]string{"c1"}, getHealth, 2, time.Millisecond, discardWriter())
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected 'timeout' in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "2 attempts") {
		t.Errorf("expected '2 attempts' in error, got %v", err)
	}
}

func TestWaitContainersHealthy_mixedNoHealthcheckAndHealthy(t *testing.T) {
	getHealth := func(id string) (string, error) {
		if id == "c1" {
			return "none", nil
		}
		return "healthy", nil
	}
	err := WaitContainersHealthy([]string{"c1", "c2"}, getHealth, 3, time.Millisecond, discardWriter())
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestWaitContainersHealthy_noHealthcheckWarningEmittedOnce(t *testing.T) {
	w, buf := capturingWriter()
	callCount := 0
	getHealth := func(id string) (string, error) {
		callCount++
		return "none", nil
	}
	err := WaitContainersHealthy([]string{"c1"}, getHealth, 3, time.Millisecond, w)
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	output := buf.String()
	warningCount := strings.Count(output, "has no healthcheck")
	if warningCount != 1 {
		t.Errorf("expected warning to be emitted once, got %d times. Output: %q", warningCount, output)
	}
}

func TestWaitContainersHealthy_inspectError(t *testing.T) {
	getHealth := func(id string) (string, error) {
		return "", fmt.Errorf("connection refused")
	}
	err := WaitContainersHealthy([]string{"c1"}, getHealth, 3, time.Millisecond, discardWriter())
	if err == nil {
		t.Error("expected error when inspect fails, got nil")
	}
	if !strings.Contains(err.Error(), "inspecting container") {
		t.Errorf("expected 'inspecting container' in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("expected 'connection refused' in error, got %v", err)
	}
}

func TestWaitContainersHealthy_emptyStatusTreatedAsNone(t *testing.T) {
	getHealth := func(id string) (string, error) { return "", nil }
	err := WaitContainersHealthy([]string{"c1"}, getHealth, 3, time.Millisecond, discardWriter())
	if err != nil {
		t.Errorf("expected nil for empty status (treated as none), got %v", err)
	}
}

func TestWaitContainersHealthy_successMessage(t *testing.T) {
	w, buf := capturingWriter()
	getHealth := func(id string) (string, error) { return "healthy", nil }
	err := WaitContainersHealthy([]string{"c1"}, getHealth, 3, time.Millisecond, w)
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "all containers healthy") {
		t.Errorf("expected success message, output: %q", output)
	}
}

func TestHealthStatus(t *testing.T) {
	// Since HealthStatus shells out to docker, we can't easily test it without a running docker daemon.
	// However, we can test the function signature and basic error handling.
	// For a real integration test, this would need a docker daemon or fake executable.

	// Test with a non-existent container (will fail to run docker)
	status, err := HealthStatus("docker", "nonexistent-container-id")
	// This will fail with an error from docker not finding the container
	if err == nil {
		t.Error("expected error for nonexistent container")
	}
	if status != "" {
		t.Errorf("expected empty status on error, got %q", status)
	}
}

func TestHealthStatusWithFakeDocker(t *testing.T) {
	// We can test the HealthStatus wrapper by using a test fake executable
	// in testdata/ or by accepting that it requires actual Docker.
	// For now, this is a placeholder for the integration-style test that would
	// need a mock docker binary or actual docker running.

	// In a real scenario, we'd set up a test helper that:
	// 1. Creates a fake docker executable in testdata/
	// 2. Points PATH to it
	// 3. Calls HealthStatus
	// 4. Verifies the output and command arguments

	t.Skip("requires docker daemon or mock binary setup")
}
