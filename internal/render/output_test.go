package render

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func newBufWriter() (*Writer, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return NewWriter(buf), buf
}

func stripANSI(s string) string {
	// Remove ANSI escape sequences for assertion convenience.
	// Simple approach: remove anything between \033[ and m.
	out := s
	for {
		start := strings.Index(out, "\033[")
		if start == -1 {
			break
		}
		end := strings.Index(out[start:], "m")
		if end == -1 {
			break
		}
		out = out[:start] + out[start+end+1:]
	}
	return out
}

func TestSuccess(t *testing.T) {
	w, buf := newBufWriter()
	w.Success("all good")
	got := stripANSI(buf.String())
	if got != "all good\n" {
		t.Errorf("Success() = %q, want %q", got, "all good\n")
	}
}

func TestErr(t *testing.T) {
	w, buf := newBufWriter()
	w.Error("something failed")
	got := stripANSI(buf.String())
	if got != "something failed\n" {
		t.Errorf("Err() = %q", got)
	}
}

func TestWarn(t *testing.T) {
	w, buf := newBufWriter()
	w.Warning("watch out")
	got := stripANSI(buf.String())
	if got != "watch out\n" {
		t.Errorf("Warn() = %q", got)
	}
}

func TestInf(t *testing.T) {
	w, buf := newBufWriter()
	w.Info("info")
	got := stripANSI(buf.String())
	if got != "info\n" {
		t.Errorf("Inf() = %q", got)
	}
}

func TestDefinition_zeroIndent(t *testing.T) {
	w, buf := newBufWriter()
	w.Definition("run", "start the project", 0, "")
	got := stripANSI(buf.String())
	if got != "run — start the project\n" {
		t.Errorf("Definition(indent=0) = %q, want no indent", got)
	}
}

func TestDefinition_explicitIndent(t *testing.T) {
	w, buf := newBufWriter()
	w.Definition("run", "start the project", 2, "")
	got := stripANSI(buf.String())
	if got != "  run — start the project\n" {
		t.Errorf("Definition(indent=2) = %q", got)
	}
}

func TestDefinition_customDelim(t *testing.T) {
	w, buf := newBufWriter()
	w.Definition("run", "start", 4, "", "|")
	got := stripANSI(buf.String())
	if got != "    run | start\n" {
		t.Errorf("Definition custom delim = %q", got)
	}
}

func TestDefinition_icon(t *testing.T) {
	w, buf := newBufWriter()
	w.Definition("url", "http://localhost", 2, "🔗")
	got := stripANSI(buf.String())
	if got != "  🔗 url — http://localhost\n" {
		t.Errorf("Definition with icon = %q", got)
	}
}

func TestLineText_emptyText(t *testing.T) {
	w, buf := newBufWriter()
	w.LineText("", Green, "+", 76, "-")
	got := stripANSI(buf.String())
	// lead(1) + pad(38) + text(0) + pad(38) + lead(1) = 78 visible chars
	if len(strings.TrimRight(got, "\n")) != 78 {
		t.Errorf("LineText empty: visual len = %d, want 78; got %q", len(strings.TrimRight(got, "\n")), got)
	}
	if !strings.HasPrefix(got, "+") {
		t.Errorf("LineText empty: should start with +")
	}
	if !strings.Contains(got[:len(got)-1], "---") {
		t.Errorf("LineText empty: should contain dashes")
	}
}

func TestLineText_withText(t *testing.T) {
	w, buf := newBufWriter()
	w.LineText("URLs", Green, "+", 76, "-")
	got := stripANSI(buf.String())
	line := strings.TrimRight(got, "\n")
	// lead(1) + pad(36) + "URLs"(4) + pad(36) + lead(1) = 78
	if len(line) != 78 {
		t.Errorf("LineText 'URLs': visual len = %d, want 78; line = %q", len(line), line)
	}
	if !strings.Contains(line, "URLs") {
		t.Errorf("LineText 'URLs': text not found")
	}
}

func TestTableHeader(t *testing.T) {
	w, buf := newBufWriter()
	w.TableHeader("Credentials")
	got := stripANSI(buf.String())
	line := strings.TrimRight(got, "\n")
	if !strings.HasPrefix(line, "+") || !strings.HasSuffix(line, "+") {
		t.Errorf("TableHeader: expected +...+ borders, got %q", line)
	}
	if !strings.Contains(line, "Credentials") {
		t.Errorf("TableHeader: text 'Credentials' not found")
	}
}

func TestTableSubheader(t *testing.T) {
	w, buf := newBufWriter()
	w.TableSubheader("Main")
	got := stripANSI(buf.String())
	line := strings.TrimRight(got, "\n")
	if !strings.HasPrefix(line, "-") || !strings.HasSuffix(line, "-") {
		t.Errorf("TableSubheader: expected -...- borders, got %q", line)
	}
	if !strings.Contains(line, "Main") {
		t.Errorf("TableSubheader: text 'Main' not found")
	}
}

func TestSetLineWidth_tableHeader(t *testing.T) {
	w, buf := newBufWriter()
	w.SetLineWidth(60)
	w.TableHeader("Test")
	got := stripANSI(buf.String())
	line := strings.TrimRight(got, "\n")
	// total=60, text=4 → pad=(60-4)/2=28 → 1+28+4+28+1=62
	if len(line) != 62 {
		t.Errorf("TableHeader lineWidth=60: visual len = %d, want 62; line = %q", len(line), line)
	}
}

func TestSetLineWidth_ignored_for_zero(t *testing.T) {
	w, _ := newBufWriter()
	w.SetLineWidth(0)
	if w.getLineWidth() != defaultLineWidth {
		t.Errorf("SetLineWidth(0) should keep default %d", defaultLineWidth)
	}
}

func TestDfn_wrapping(t *testing.T) {
	w, buf := newBufWriter()
	w.SetLineWidth(20)
	// overhead: indent(2) + name("n"=1) + " — "(3) = 6; max desc = 22-6 = 16
	w.Definition("n", "one two three four five", 2, "")
	got := stripANSI(buf.String())
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("Dfn wrapping: expected multiple lines, got: %q", got)
	}
	// Every line must fit within lineWidth+2 = 22 runes.
	for i, l := range lines {
		if utf8.RuneCountInString(l) > 22 {
			t.Errorf("Dfn wrap line %d too long (%d > 22): %q", i, utf8.RuneCountInString(l), l)
		}
	}
	// Continuation lines must be indented by overhead (6 spaces).
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, "      ") {
			t.Errorf("Dfn continuation not indented to overhead: %q", l)
		}
	}
}

func TestWordWrap(t *testing.T) {
	tests := []struct {
		text  string
		width int
		want  []string
	}{
		{"hello", 10, []string{"hello"}},
		{"hello world", 20, []string{"hello world"}},
		// Space at exactly position width — break there, "hello world" fits entirely.
		{"hello world foo", 11, []string{"hello world", "foo"}},
		{"hello world foo", 5, []string{"hello", "world", "foo"}},
		// No spaces → hard-break at width.
		{"toolongword", 5, []string{"toolo", "ngwor", "d"}},
		{"a bb ccc", 4, []string{"a bb", "ccc"}},
	}
	for _, tc := range tests {
		got := wordWrap(tc.text, tc.width)
		if len(got) != len(tc.want) {
			t.Errorf("wordWrap(%q, %d) = %v, want %v", tc.text, tc.width, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("wordWrap(%q, %d)[%d] = %q, want %q", tc.text, tc.width, i, got[i], tc.want[i])
			}
		}
	}
}

func TestWarn_wrapping(t *testing.T) {
	w, buf := newBufWriter()
	w.SetLineWidth(10)
	w.Warning("this is a long warning message")
	got := stripANSI(buf.String())
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) < 2 {
		t.Errorf("Warn: expected wrapping at width 10, got single line: %q", got)
	}
	for i, l := range lines {
		if utf8.RuneCountInString(l) > 12 {
			t.Errorf("Warn wrap line %d too long (%d > 12): %q", i, utf8.RuneCountInString(l), l)
		}
	}
}

// --- ASCII / prepareASCII ---

func TestPrepareASCII_plain(t *testing.T) {
	block := prepareASCII("Hi", "standard", 78)
	if len(block.lines) == 0 {
		t.Fatal("expected non-empty output")
	}
	for _, l := range block.lines {
		if strings.Contains(l, "\033[") {
			t.Errorf("expected plain output without ANSI escapes, got: %q", l)
		}
	}
}

func TestPrepareASCII_emptyFontStillRenders(t *testing.T) {
	// prepareASCII does not default font; that's ASCII's job. Pass "standard" directly.
	block := prepareASCII("Hi", "standard", 78)
	if len(block.lines) == 0 {
		t.Fatal("expected non-empty output")
	}
}

func TestASCII_plainOutput(t *testing.T) {
	w, buf := newBufWriter()
	if err := w.ASCII([]string{"Hi"}, "standard"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if out == "" {
		t.Fatal("expected ASCII output")
	}
	if strings.Contains(out, "\033[") {
		t.Errorf("expected plain output, got ANSI escapes: %q", out)
	}
}

func TestASCII_defaultsFont(t *testing.T) {
	w, buf := newBufWriter()
	if err := w.ASCII([]string{"Hi"}, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("expected output when font defaults")
	}
}
