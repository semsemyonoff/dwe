package logs

import (
	"testing"
	"time"
)

func TestParseLine(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)

	tests := []struct {
		name       string
		stream     string
		raw        string
		wantTs     string // "" → fallback (any valid RFC3339Nano close to now)
		wantMsg    string
		wantStream string
	}{
		{
			name:       "valid RFC3339Nano with nanoseconds",
			stream:     "stdout",
			raw:        "2026-05-29T07:30:00.000000000Z hello world",
			wantTs:     "2026-05-29T07:30:00.000000000Z",
			wantMsg:    "hello world",
			wantStream: "stdout",
		},
		{
			name:       "valid RFC3339Nano without nanoseconds",
			stream:     "stdout",
			raw:        "2026-05-29T07:30:00Z hello world",
			wantTs:     "2026-05-29T07:30:00Z",
			wantMsg:    "hello world",
			wantStream: "stdout",
		},
		{
			name:       "stderr stream attribution",
			stream:     "stderr",
			raw:        "2026-05-29T07:30:00.000000000Z error message",
			wantTs:     "2026-05-29T07:30:00.000000000Z",
			wantMsg:    "error message",
			wantStream: "stderr",
		},
		{
			name:       "malformed timestamp falls back to current time",
			stream:     "stdout",
			raw:        "not-a-timestamp message content",
			wantTs:     "",
			wantMsg:    "not-a-timestamp message content",
			wantStream: "stdout",
		},
		{
			name:       "no whitespace in line falls back",
			stream:     "stdout",
			raw:        "notimestamp",
			wantTs:     "",
			wantMsg:    "notimestamp",
			wantStream: "stdout",
		},
		{
			name:       "empty message after timestamp",
			stream:     "stdout",
			raw:        "2026-05-29T07:30:00.000000000Z ",
			wantTs:     "2026-05-29T07:30:00.000000000Z",
			wantMsg:    "",
			wantStream: "stdout",
		},
		{
			name:       "CRLF stripped from raw before parsing",
			stream:     "stdout",
			raw:        "2026-05-29T07:30:00.000000000Z message\r\n",
			wantTs:     "2026-05-29T07:30:00.000000000Z",
			wantMsg:    "message",
			wantStream: "stdout",
		},
		{
			name:       "LF stripped from raw before parsing",
			stream:     "stdout",
			raw:        "2026-05-29T07:30:00.000000000Z message\n",
			wantTs:     "2026-05-29T07:30:00.000000000Z",
			wantMsg:    "message",
			wantStream: "stdout",
		},
		{
			name:       "message with spaces preserved",
			stream:     "stdout",
			raw:        "2026-05-29T07:30:00.000000000Z word1 word2 word3",
			wantTs:     "2026-05-29T07:30:00.000000000Z",
			wantMsg:    "word1 word2 word3",
			wantStream: "stdout",
		},
		{
			name:       "empty raw line falls back",
			stream:     "stderr",
			raw:        "",
			wantTs:     "",
			wantMsg:    "",
			wantStream: "stderr",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseLine(tc.stream, tc.raw)

			if got.Stream != tc.wantStream {
				t.Errorf("Stream = %q, want %q", got.Stream, tc.wantStream)
			}
			if got.Msg != tc.wantMsg {
				t.Errorf("Msg = %q, want %q", got.Msg, tc.wantMsg)
			}
			if tc.wantTs == "" {
				parsed, err := time.Parse(time.RFC3339Nano, got.Ts)
				if err != nil {
					t.Errorf("Ts fallback %q not parseable as RFC3339Nano: %v", got.Ts, err)
				} else if parsed.Before(before) {
					t.Errorf("Ts fallback %q is earlier than test start", got.Ts)
				}
			} else if got.Ts != tc.wantTs {
				t.Errorf("Ts = %q, want %q", got.Ts, tc.wantTs)
			}
		})
	}
}
