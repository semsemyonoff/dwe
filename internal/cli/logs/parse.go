package logs

import (
	"strings"
	"time"
)

// logLineJSON is the NDJSON envelope emitted by `dwe logs --output json`.
// One object per line; no array wrapper (streaming-friendly shape).
type logLineJSON struct {
	Ts     string `json:"ts"`     // RFC3339Nano timestamp
	Stream string `json:"stream"` // "stdout" | "stderr"
	Msg    string `json:"msg"`    // log line with trailing newline stripped
}

// parseLine converts one raw line from `docker logs --timestamps` output into a
// logLineJSON record. stream must be "stdout" or "stderr".
//
// docker --timestamps format: "2006-01-02T15:04:05.999999999Z07:00 <message>"
// The first whitespace-delimited token is the timestamp. If it does not parse
// as RFC3339Nano, the current time is used as a fallback so no line is dropped.
func parseLine(stream, raw string) logLineJSON {
	raw = strings.TrimRight(raw, "\r\n")
	idx := strings.IndexAny(raw, " \t")
	if idx > 0 {
		ts := raw[:idx]
		if _, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			return logLineJSON{
				Ts:     ts,
				Stream: stream,
				Msg:    raw[idx+1:],
			}
		}
	}
	return logLineJSON{
		Ts:     time.Now().UTC().Format(time.RFC3339Nano),
		Stream: stream,
		Msg:    raw,
	}
}
