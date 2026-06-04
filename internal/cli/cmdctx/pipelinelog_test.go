package cmdctx

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	pipeline "github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

func TestWarnSilentLog(t *testing.T) {
	const logPath = "/tmp/dwe-run.log"
	tests := []struct {
		name       string
		err        error
		logEnabled bool
		wantHint   bool
	}{
		{"silent error with logging on emits hint", pipeline.ErrSilent, true, true},
		{"silent error wrapped still emits hint", fmt.Errorf("step failed: %w", pipeline.ErrSilent), true, true},
		{"silent error with logging off is silent", pipeline.ErrSilent, false, false},
		{"non-silent error never emits hint", errors.New("boom"), true, false},
		{"nil error never emits hint", nil, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder
			WarnSilentLog(render.NewWriter(&buf), tt.err, tt.logEnabled, logPath)
			out := buf.String()
			gotHint := strings.Contains(out, logPath)
			if gotHint != tt.wantHint {
				t.Fatalf("WarnSilentLog hint = %v (output %q), want %v", gotHint, out, tt.wantHint)
			}
			if !tt.wantHint && out != "" {
				t.Errorf("expected no output, got %q", out)
			}
		})
	}
}
