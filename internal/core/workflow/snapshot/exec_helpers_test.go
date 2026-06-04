package snapshot

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/workflow/snapshot/meta"
)

func TestClassifyRunErr(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantStatus     string
		wantFailedStep string
	}{
		{
			name:           "nil error is ok",
			err:            nil,
			wantStatus:     meta.StatusOk,
			wantFailedStep: "",
		},
		{
			name:           "canceled is interrupted",
			err:            context.Canceled,
			wantStatus:     meta.StatusInterrupted,
			wantFailedStep: context.Canceled.Error(),
		},
		{
			name:           "deadline exceeded is interrupted",
			err:            context.DeadlineExceeded,
			wantStatus:     meta.StatusInterrupted,
			wantFailedStep: context.DeadlineExceeded.Error(),
		},
		{
			name:           "wrapped cancellation is interrupted",
			err:            fmt.Errorf("step foo: %w", context.Canceled),
			wantStatus:     meta.StatusInterrupted,
			wantFailedStep: "step foo: " + context.Canceled.Error(),
		},
		{
			name:           "other error is failed",
			err:            errors.New("boom"),
			wantStatus:     meta.StatusFailed,
			wantFailedStep: "boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotFailedStep := classifyRunErr(tt.err)
			if gotStatus != tt.wantStatus {
				t.Errorf("status = %q, want %q", gotStatus, tt.wantStatus)
			}
			if gotFailedStep != tt.wantFailedStep {
				t.Errorf("failedStep = %q, want %q", gotFailedStep, tt.wantFailedStep)
			}
		})
	}
}

func TestAbsOrSelf(t *testing.T) {
	// A relative path resolves to an absolute one.
	got := absOrSelf("some/rel/path")
	if !filepath.IsAbs(got) {
		t.Errorf("absOrSelf(rel) = %q, want absolute path", got)
	}

	// An already-absolute path is returned cleaned and unchanged.
	abs := filepath.Join(t.TempDir(), "snap")
	if got := absOrSelf(abs); got != abs {
		t.Errorf("absOrSelf(abs) = %q, want %q", got, abs)
	}
}
