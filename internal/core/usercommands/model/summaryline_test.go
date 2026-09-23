package model

import "testing"

func TestSummaryLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", " \t\n \r\n  ", ""},
		{"single line", "Run migrations", "Run migrations"},
		{"single line padded", "  Run migrations  ", "Run migrations"},
		{"literal block keeps first line", "Run migrations\nUsage:\n  dwe cmd db.migrate\n", "Run migrations"},
		{"leading blank lines", "\n\n   \nRun migrations\nmore", "Run migrations"},
		{"crlf", "Run migrations\r\nUsage: x\r\n", "Run migrations"},
		{"lone cr", "Run migrations\rUsage: x", "Run migrations"},
		{"leading crlf blanks", "\r\n\r\n  Run migrations\r\n", "Run migrations"},
		{"internal tab becomes space", "Run\tmigrations", "Run migrations"},
		{"trailing text after blank line", "Run migrations\n\nExamples follow", "Run migrations"},
		{"unicode", "Запустить миграции\nПример", "Запустить миграции"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SummaryLine(tt.in); got != tt.want {
				t.Errorf("SummaryLine(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
