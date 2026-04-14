package render

import (
	"strings"
	"testing"
)

func TestWriteTree_empty(t *testing.T) {
	w, buf := newBufWriter()
	w.WriteTree(nil)
	if buf.String() != "" {
		t.Errorf("WriteTree(nil) produced output: %q", buf.String())
	}
}

func TestWriteTree_singleNode(t *testing.T) {
	w, buf := newBufWriter()
	w.WriteTree([]*TreeNode{
		{Label: "migrate", Tags: []string{"service_exec"}, Desc: "Run migrations"},
	})
	got := buf.String()
	if !strings.Contains(got, "migrate") {
		t.Errorf("expected label in output: %q", got)
	}
	if !strings.Contains(got, "[service_exec]") {
		t.Errorf("expected tag in output: %q", got)
	}
	if !strings.Contains(got, "Run migrations") {
		t.Errorf("expected desc in output: %q", got)
	}
}

func TestWriteTree_noTagsNoDesc(t *testing.T) {
	w, buf := newBufWriter()
	w.WriteTree([]*TreeNode{
		{Label: "services"},
	})
	got := buf.String()
	if got != "services\n" {
		t.Errorf("expected bare label line, got: %q", got)
	}
}

func TestWriteTree_nested(t *testing.T) {
	w, buf := newBufWriter()
	w.WriteTree([]*TreeNode{
		{
			Label: "services",
			Children: []*TreeNode{
				{
					Label: "main",
					Children: []*TreeNode{
						{Label: "migrate", Tags: []string{"service_exec"}, Desc: "Run migrations"},
						{Label: "bootstrap", Tags: []string{"workflow"}},
					},
				},
			},
		},
	})
	got := buf.String()
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d:\n%s", len(lines), got)
	}
	if lines[0] != "services" {
		t.Errorf("line 0: want %q, got %q", "services", lines[0])
	}
	if lines[1] != "  main" {
		t.Errorf("line 1: want %q, got %q", "  main", lines[1])
	}
	if !strings.HasPrefix(lines[2], "    migrate") {
		t.Errorf("line 2: want prefix %q, got %q", "    migrate", lines[2])
	}
	if !strings.HasPrefix(lines[3], "    bootstrap") {
		t.Errorf("line 3: want prefix %q, got %q", "    bootstrap", lines[3])
	}
}

func TestWriteTree_multipleTags(t *testing.T) {
	w, buf := newBufWriter()
	w.WriteTree([]*TreeNode{
		{Label: "up", Tags: []string{"private", "command"}},
	})
	got := buf.String()
	if !strings.Contains(got, "[private, command]") {
		t.Errorf("expected combined tags: %q", got)
	}
}

func TestWriteTree_multipleRoots(t *testing.T) {
	w, buf := newBufWriter()
	w.WriteTree([]*TreeNode{
		{Label: "db"},
		{Label: "services"},
	})
	got := buf.String()
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), got)
	}
	if lines[0] != "db" {
		t.Errorf("line 0: want %q, got %q", "db", lines[0])
	}
	if lines[1] != "services" {
		t.Errorf("line 1: want %q, got %q", "services", lines[1])
	}
}
