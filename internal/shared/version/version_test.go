package version_test

import (
	"strings"
	"testing"

	"devbox-cli/internal/shared/version"
)

func TestInfoDefaults(t *testing.T) {
	info := version.Info()
	if !strings.Contains(info, "dev") {
		t.Errorf("expected Info() to contain default version 'dev', got: %s", info)
	}
	if !strings.Contains(info, "unknown") {
		t.Errorf("expected Info() to contain 'unknown' for unset fields, got: %s", info)
	}
}

func TestInfoFormat(t *testing.T) {
	version.Version = "1.2.3"
	version.Commit = "abc1234"
	version.Date = "2026-04-16T00:00:00Z"
	version.BuiltBy = "make"
	t.Cleanup(func() {
		version.Version = "dev"
		version.Commit = "unknown"
		version.Date = "unknown"
		version.BuiltBy = "unknown"
	})

	info := version.Info()
	expected := "version 1.2.3 (commit abc1234, built 2026-04-16T00:00:00Z by make)"
	if info != expected {
		t.Errorf("expected %q, got %q", expected, info)
	}
}
