// Package bridgeproto defines the wire protocol shared by the host bridge
// daemon (internal/core/bridge) and the in-container shim client
// (internal/shared/bridgeclient): length-prefixed binary frames, the HELLO /
// ERROR payloads, token handling, and unix-socket peer-credential checks.
//
// This package is a leaf: it MUST NOT import anything from internal/core or
// internal/cli. Both sides of the bridge — including the standalone shim
// binary (cmd/dwe-shim) — link it, so it stays free of cobra, lipgloss, and
// any project-model knowledge.
//
// Frame layout, identical on both transports (unix socket and TCP):
//
//	[4 bytes payload length, big-endian uint32]
//	[1 byte frame type]
//	[payload]
package bridgeproto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// FrameType identifies the kind of a frame on the wire.
type FrameType byte

// Frame types. One connection carries exactly one command: HELLO opens it,
// EXIT or ERROR ends it. RESIZE is reserved — pty support is not planned.
const (
	FrameHello      FrameType = 0x01 // JSON Hello payload
	FrameStdin      FrameType = 0x02 // raw bytes → subprocess stdin
	FrameStdout     FrameType = 0x03 // raw bytes ← subprocess stdout
	FrameStderr     FrameType = 0x04 // raw bytes ← subprocess stderr
	FrameStdinClose FrameType = 0x05 // empty; EOF for subprocess stdin
	FrameSignal     FrameType = 0x06 // 1 byte signal number (SIGINT/SIGTERM)
	FrameResize     FrameType = 0x07 // 8 bytes rows+cols; reserved, unused in V1
	FrameExit       FrameType = 0x08 // 4 bytes big-endian int32 exit code
	FrameError      FrameType = 0x09 // JSON ErrorInfo payload
)

// String returns the lowercase wire name of the frame type for diagnostics.
func (t FrameType) String() string {
	switch t {
	case FrameHello:
		return "hello"
	case FrameStdin:
		return "stdin"
	case FrameStdout:
		return "stdout"
	case FrameStderr:
		return "stderr"
	case FrameStdinClose:
		return "stdin_close"
	case FrameSignal:
		return "signal"
	case FrameResize:
		return "resize"
	case FrameExit:
		return "exit"
	case FrameError:
		return "error"
	default:
		return fmt.Sprintf("unknown(0x%02x)", byte(t))
	}
}

// MaxPayload bounds a single frame payload to keep peer-controlled
// allocations in check. Streams larger than this are chunked into multiple
// STDIN/STDOUT/STDERR frames by the pumps.
const MaxPayload = 1 << 20 // 1 MiB

// ErrPayloadTooLarge is returned when a frame payload exceeds MaxPayload —
// by WriteFrame before writing, and by ReadFrame before allocating.
var ErrPayloadTooLarge = errors.New("bridgeproto: frame payload exceeds maximum")

// headerSize is the fixed frame prefix: 4-byte length + 1-byte type.
const headerSize = 5

// WriteFrame writes one frame to w. The header and payload go out in a
// single Write call, so concurrent WriteFrame calls on a net.Conn cannot
// interleave partial frames (net.Conn serializes individual Write calls).
func WriteFrame(w io.Writer, t FrameType, payload []byte) error {
	if len(payload) > MaxPayload {
		return fmt.Errorf("%w: %d bytes (max %d)", ErrPayloadTooLarge, len(payload), MaxPayload)
	}
	buf := make([]byte, headerSize+len(payload))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(payload)))
	buf[4] = byte(t)
	copy(buf[headerSize:], payload)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("bridgeproto: writing %s frame: %w", t, err)
	}
	return nil
}

// ReadFrame reads one frame from r. A clean end-of-stream before any header
// byte returns io.EOF; a stream truncated mid-frame returns
// io.ErrUnexpectedEOF. A declared payload length above MaxPayload returns
// ErrPayloadTooLarge without allocating.
func ReadFrame(r io.Reader) (FrameType, []byte, error) {
	var hdr [headerSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		// io.ReadFull yields io.EOF only when zero bytes were read.
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:4])
	if n > MaxPayload {
		return 0, nil, fmt.Errorf("%w: %d bytes (max %d)", ErrPayloadTooLarge, n, MaxPayload)
	}
	t := FrameType(hdr[4])
	if n == 0 {
		return t, nil, nil
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return 0, nil, err
	}
	return t, payload, nil
}

// EncodeExitPayload encodes a subprocess exit code as the 4-byte big-endian
// int32 payload of an EXIT frame.
func EncodeExitPayload(code int32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(code))
	return buf[:]
}

// DecodeExitPayload decodes the payload of an EXIT frame.
func DecodeExitPayload(payload []byte) (int32, error) {
	if len(payload) != 4 {
		return 0, fmt.Errorf("bridgeproto: exit payload must be 4 bytes, got %d", len(payload))
	}
	return int32(binary.BigEndian.Uint32(payload)), nil
}

// EncodeSignalPayload encodes a signal number as the 1-byte payload of a
// SIGNAL frame. V1 forwards only SIGINT and SIGTERM.
func EncodeSignalPayload(sig byte) []byte {
	return []byte{sig}
}

// DecodeSignalPayload decodes the payload of a SIGNAL frame.
func DecodeSignalPayload(payload []byte) (byte, error) {
	if len(payload) != 1 {
		return 0, fmt.Errorf("bridgeproto: signal payload must be 1 byte, got %d", len(payload))
	}
	return payload[0], nil
}
