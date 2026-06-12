package bridgeproto

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFrameRoundTrip_AllTypes(t *testing.T) {
	tests := []struct {
		name    string
		typ     FrameType
		payload []byte
	}{
		{"hello json", FrameHello, []byte(`{"protocol_version":1}`)},
		{"stdin bytes", FrameStdin, []byte("line of input\n")},
		{"stdout bytes", FrameStdout, []byte("output")},
		{"stderr bytes", FrameStderr, []byte("warning: something")},
		{"stdin_close empty", FrameStdinClose, nil},
		{"signal one byte", FrameSignal, []byte{15}},
		{"resize eight bytes", FrameResize, []byte{0, 0, 0, 24, 0, 0, 0, 80}},
		{"exit four bytes", FrameExit, EncodeExitPayload(1)},
		{"error json", FrameError, []byte(`{"code":"auth_failed","message":"bad token"}`)},
		{"empty payload non-empty type", FrameStdout, []byte{}},
		{"max payload", FrameStdin, bytes.Repeat([]byte{0xab}, MaxPayload)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteFrame(&buf, tt.typ, tt.payload); err != nil {
				t.Fatalf("WriteFrame: %v", err)
			}
			typ, payload, err := ReadFrame(&buf)
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if typ != tt.typ {
				t.Errorf("type = %v, want %v", typ, tt.typ)
			}
			if !bytes.Equal(payload, tt.payload) {
				t.Errorf("payload mismatch: got %d bytes, want %d bytes", len(payload), len(tt.payload))
			}
		})
	}
}

func TestFrameSequence_ReadInOrder(t *testing.T) {
	var buf bytes.Buffer
	frames := []struct {
		typ     FrameType
		payload []byte
	}{
		{FrameHello, []byte(`{"protocol_version":1}`)},
		{FrameStdout, []byte("hello from host")},
		{FrameStderr, []byte("a warning")},
		{FrameExit, EncodeExitPayload(0)},
	}
	for _, f := range frames {
		if err := WriteFrame(&buf, f.typ, f.payload); err != nil {
			t.Fatalf("WriteFrame(%v): %v", f.typ, err)
		}
	}
	for i, want := range frames {
		typ, payload, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame #%d: %v", i, err)
		}
		if typ != want.typ || !bytes.Equal(payload, want.payload) {
			t.Errorf("frame #%d = (%v, %q), want (%v, %q)", i, typ, payload, want.typ, want.payload)
		}
	}
	if _, _, err := ReadFrame(&buf); !errors.Is(err, io.EOF) {
		t.Errorf("after last frame err = %v, want io.EOF", err)
	}
}

func TestWriteFrame_PayloadTooLarge(t *testing.T) {
	var buf bytes.Buffer
	err := WriteFrame(&buf, FrameStdin, make([]byte, MaxPayload+1))
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %d bytes despite oversized payload", buf.Len())
	}
}

func TestReadFrame_DeclaredLengthTooLarge(t *testing.T) {
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[:4], MaxPayload+1)
	hdr[4] = byte(FrameStdin)
	_, _, err := ReadFrame(bytes.NewReader(hdr[:]))
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
}

func TestReadFrame_EmptyStream(t *testing.T) {
	_, _, err := ReadFrame(bytes.NewReader(nil))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

func TestReadFrame_TruncatedHeader(t *testing.T) {
	_, _, err := ReadFrame(bytes.NewReader([]byte{0, 0, 0}))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReadFrame_TruncatedPayload(t *testing.T) {
	tests := []struct {
		name  string
		given int // payload bytes present out of 10 declared
	}{
		{"no payload bytes", 0},
		{"partial payload", 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			var hdr [5]byte
			binary.BigEndian.PutUint32(hdr[:4], 10)
			hdr[4] = byte(FrameStdout)
			buf.Write(hdr[:])
			buf.Write(make([]byte, tt.given))
			_, _, err := ReadFrame(&buf)
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
			}
		})
	}
}

func TestExitPayload_RoundTrip(t *testing.T) {
	for _, code := range []int32{0, 1, 2, 127, 130, 143, -1} {
		got, err := DecodeExitPayload(EncodeExitPayload(code))
		if err != nil {
			t.Fatalf("DecodeExitPayload(%d): %v", code, err)
		}
		if got != code {
			t.Errorf("round trip = %d, want %d", got, code)
		}
	}
}

func TestDecodeExitPayload_WrongLength(t *testing.T) {
	for _, payload := range [][]byte{nil, {0}, {0, 0, 0}, {0, 0, 0, 0, 0}} {
		if _, err := DecodeExitPayload(payload); err == nil {
			t.Errorf("DecodeExitPayload(%d bytes): expected error", len(payload))
		}
	}
}

func TestSignalPayload_RoundTrip(t *testing.T) {
	for _, sig := range []byte{2, 15} { // SIGINT, SIGTERM
		got, err := DecodeSignalPayload(EncodeSignalPayload(sig))
		if err != nil {
			t.Fatalf("DecodeSignalPayload(%d): %v", sig, err)
		}
		if got != sig {
			t.Errorf("round trip = %d, want %d", got, sig)
		}
	}
}

func TestDecodeSignalPayload_WrongLength(t *testing.T) {
	for _, payload := range [][]byte{nil, {}, {2, 15}} {
		if _, err := DecodeSignalPayload(payload); err == nil {
			t.Errorf("DecodeSignalPayload(%d bytes): expected error", len(payload))
		}
	}
}

func TestFrameType_String(t *testing.T) {
	tests := []struct {
		typ  FrameType
		want string
	}{
		{FrameHello, "hello"},
		{FrameStdin, "stdin"},
		{FrameStdout, "stdout"},
		{FrameStderr, "stderr"},
		{FrameStdinClose, "stdin_close"},
		{FrameSignal, "signal"},
		{FrameResize, "resize"},
		{FrameExit, "exit"},
		{FrameError, "error"},
		{FrameType(0xff), "unknown(0xff)"},
	}
	for _, tt := range tests {
		if got := tt.typ.String(); got != tt.want {
			t.Errorf("FrameType(%#x).String() = %q, want %q", byte(tt.typ), got, tt.want)
		}
	}
}

func TestHello_RoundTrip(t *testing.T) {
	in := Hello{
		ProtocolVersion: ProtocolVersion,
		Token:           "deadbeef",
		Argv:            []string{"commands", "lint"},
		Env:             []string{"PATH=/usr/bin", "HOME=/home/dev"},
		Cwd:             "/Users/foo/projects/my-proj/src",
		TTY:             false,
		Term:            "xterm-256color",
		Winsize:         &Winsize{Rows: 24, Cols: 80},
	}
	payload, err := EncodeHello(in)
	if err != nil {
		t.Fatalf("EncodeHello: %v", err)
	}
	got, err := DecodeHello(payload)
	if err != nil {
		t.Fatalf("DecodeHello: %v", err)
	}
	if got.ProtocolVersion != in.ProtocolVersion || got.Token != in.Token ||
		got.Cwd != in.Cwd || got.TTY != in.TTY || got.Term != in.Term {
		t.Errorf("scalar fields mismatch: got %+v, want %+v", got, in)
	}
	if len(got.Argv) != 2 || got.Argv[0] != "commands" || got.Argv[1] != "lint" {
		t.Errorf("argv = %v, want %v", got.Argv, in.Argv)
	}
	if len(got.Env) != 2 || got.Env[0] != in.Env[0] || got.Env[1] != in.Env[1] {
		t.Errorf("env = %v, want %v", got.Env, in.Env)
	}
	if got.Winsize == nil || got.Winsize.Rows != 24 || got.Winsize.Cols != 80 {
		t.Errorf("winsize = %+v, want %+v", got.Winsize, in.Winsize)
	}
}

func TestHello_OmitsEmptyOptionalFields(t *testing.T) {
	payload, err := EncodeHello(Hello{
		ProtocolVersion: ProtocolVersion,
		Argv:            []string{"status"},
		Cwd:             "/p",
	})
	if err != nil {
		t.Fatalf("EncodeHello: %v", err)
	}
	s := string(payload)
	for _, field := range []string{`"token"`, `"term"`, `"winsize"`} {
		if strings.Contains(s, field) {
			t.Errorf("payload contains %s, want omitted: %s", field, s)
		}
	}
}

func TestDecodeHello_TolerantOfUnknownFields(t *testing.T) {
	got, err := DecodeHello([]byte(`{"protocol_version":1,"argv":["status"],"cwd":"/p","tty":false,"future_field":42}`))
	if err != nil {
		t.Fatalf("DecodeHello: %v", err)
	}
	if got.ProtocolVersion != 1 || got.Cwd != "/p" {
		t.Errorf("got %+v", got)
	}
}

func TestDecodeHello_InvalidJSON(t *testing.T) {
	if _, err := DecodeHello([]byte("not json")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestErrorInfo_RoundTrip(t *testing.T) {
	codes := []string{
		ErrCodeAuthFailed,
		ErrCodeVersionMismatch,
		ErrCodeCwdOutsideProject,
		ErrCodeTTYUnsupported,
		ErrCodeDaemonShuttingDown,
	}
	for _, code := range codes {
		payload, err := EncodeError(ErrorInfo{Code: code, Message: "detail"})
		if err != nil {
			t.Fatalf("EncodeError(%s): %v", code, err)
		}
		got, err := DecodeError(payload)
		if err != nil {
			t.Fatalf("DecodeError(%s): %v", code, err)
		}
		if got.Code != code || got.Message != "detail" {
			t.Errorf("round trip = %+v, want code %q", got, code)
		}
	}
}

func TestDecodeError_InvalidJSON(t *testing.T) {
	if _, err := DecodeError([]byte("{")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestHello_WireFieldNames(t *testing.T) {
	payload, err := EncodeHello(Hello{
		ProtocolVersion: 1,
		Token:           "t",
		Argv:            []string{"a"},
		Env:             []string{"K=V"},
		Cwd:             "/p",
		Term:            "xterm",
		Winsize:         &Winsize{Rows: 1, Cols: 2},
	})
	if err != nil {
		t.Fatalf("EncodeHello: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	for _, key := range []string{"protocol_version", "token", "argv", "env", "cwd", "tty", "term", "winsize"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("wire payload missing field %q: %s", key, payload)
		}
	}
}
