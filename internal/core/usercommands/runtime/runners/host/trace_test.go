package host

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// TestRunner_TraceEchoesSpawnedArgv pins the `-v` echo of a type: shell
// command — including an argv_append_from expression, which is host code too —
// and that the default level emits nothing.
func TestRunner_TraceEchoesSpawnedArgv(t *testing.T) {
	tests := []struct {
		name  string
		def   model.CommandDef
		level trace.Level
		want  []string
	}{
		{
			name:  "cmd at verbose",
			def:   model.CommandDef{Type: model.CommandTypeShell, Cmd: "echo hi"},
			level: trace.LevelVerbose,
			want:  []string{"$ sh -c 'echo hi'"},
		},
		{
			name:  "argv with argv_append_from at verbose",
			def:   model.CommandDef{Type: model.CommandTypeShell, Argv: []string{"echo"}, ArgvAppendFrom: "echo a.txt"},
			level: trace.LevelVerbose,
			want:  []string{"$ sh -c 'echo a.txt'", "$ echo a.txt"},
		},
		{
			name:  "cmd at default level",
			def:   model.CommandDef{Type: model.CommandTypeShell, Cmd: "echo hi"},
			level: trace.LevelOff,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			trace.Configure(&buf, tt.level)
			t.Cleanup(func() { trace.Configure(nil, trace.LevelOff) })

			def := tt.def
			var stdout bytes.Buffer
			rc := spec.RunContext{
				Cmd:         &def,
				Render:      &tpl.RenderContext{},
				ProjectRoot: t.TempDir(),
				Stdout:      &stdout,
				Stderr:      &bytes.Buffer{},
			}
			if err := (&Runner{}).Run(context.Background(), rc); err != nil {
				t.Fatalf("Run: %v", err)
			}
			var got []string
			if s := strings.TrimRight(buf.String(), "\n"); s != "" {
				got = strings.Split(s, "\n")
			}
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Fatalf("trace lines:\n got %q\nwant %q", got, tt.want)
			}
			if strings.Contains(stdout.String(), "$ ") {
				t.Fatalf("trace echo leaked into the child's stdout: %q", stdout.String())
			}
		})
	}
}
