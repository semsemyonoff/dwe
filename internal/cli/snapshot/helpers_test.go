package snapshot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// TestRequireSnapshotConfig verifies the nil-config guard returns an
// operation-named error and passes a non-nil config through.
func TestRequireSnapshotConfig(t *testing.T) {
	t.Run("nil → error naming operation and path", func(t *testing.T) {
		err := requireSnapshotConfig(nil, "restore", "/proj")
		if err == nil {
			t.Fatal("expected error for nil snapCfg")
		}
		msg := err.Error()
		if !strings.Contains(msg, "snapshot restore:") {
			t.Errorf("error %q missing operation prefix", msg)
		}
		if !strings.Contains(msg, config.SnapshotConfigPath("/proj")) {
			t.Errorf("error %q missing snapshot.yml path", msg)
		}
	})
	t.Run("non-nil → nil", func(t *testing.T) {
		if err := requireSnapshotConfig(&config.SnapshotConfig{}, "remove", "/proj"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// TestSignalAwareContext verifies the parent-default and that the returned
// context derives from a supplied parent.
func TestSignalAwareContext(t *testing.T) {
	t.Run("nil parent defaults to background", func(t *testing.T) {
		// Deliberately exercise the nil-parent default path.
		var nilParent context.Context
		ctx, stop := signalAwareContext(nilParent)
		defer stop()
		if ctx == nil {
			t.Fatal("expected non-nil context")
		}
		if err := ctx.Err(); err != nil {
			t.Errorf("fresh context already done: %v", err)
		}
	})
	t.Run("derives from parent cancellation", func(t *testing.T) {
		parent, cancelParent := context.WithCancel(context.Background())
		ctx, stop := signalAwareContext(parent)
		defer stop()
		cancelParent()
		<-ctx.Done()
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Errorf("ctx.Err() = %v, want context.Canceled", ctx.Err())
		}
	})
}

// TestInstallSnapshotNotifier verifies the silent path is an inert no-op (no
// userconfig load, no panic) and the returned closure is callable.
func TestInstallSnapshotNotifier(t *testing.T) {
	var runErr error
	finalize := installSnapshotNotifier("/nonexistent", "snapshot:create", "proj", true, &runErr, func(error) bool {
		return false
	})
	if finalize == nil {
		t.Fatal("expected non-nil finalize closure")
	}
	// Must not panic when invoked.
	finalize()
}
