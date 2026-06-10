package bridgeproto

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion is the bridge wire protocol version carried in HELLO.
// The daemon rejects a mismatch with ErrCodeVersionMismatch; the shim then
// asks the user to re-run `dwe deploy` to refresh the materialized shims.
const ProtocolVersion = 1

// ERROR frame codes (the `code` field of ErrorInfo).
const (
	// ErrCodeAuthFailed — TCP token mismatch or unix peer uid mismatch.
	ErrCodeAuthFailed = "auth_failed"
	// ErrCodeVersionMismatch — HELLO protocol_version differs from the daemon's.
	ErrCodeVersionMismatch = "version_mismatch"
	// ErrCodeCwdOutsideProject — translated cwd resolves outside the project root.
	ErrCodeCwdOutsideProject = "cwd_outside_project"
	// ErrCodeTTYUnsupported — HELLO requested a tty; the bridge never allocates one.
	ErrCodeTTYUnsupported = "tty_unsupported"
	// ErrCodeDaemonShuttingDown — connection arrived during graceful shutdown.
	ErrCodeDaemonShuttingDown = "daemon_shutting_down"
)

// Winsize is the terminal size reported in HELLO. Informational only in V1
// (the shim always sends tty: false; RESIZE frames are reserved).
type Winsize struct {
	Rows uint32 `json:"rows"`
	Cols uint32 `json:"cols"`
}

// Hello is the JSON payload of the HELLO frame — the first frame on every
// connection, sent by the shim.
type Hello struct {
	// ProtocolVersion must equal the package ProtocolVersion constant.
	ProtocolVersion int `json:"protocol_version"`
	// Token authenticates TCP connections; ignored on unix (peercred there).
	Token string `json:"token,omitempty"`
	// Argv is the dwe argument vector, pass-through and never translated.
	Argv []string `json:"argv"`
	// Env is the forwarded environment in KEY=VALUE form, already stripped of
	// bridge-internal variables by the shim (the daemon re-filters).
	Env []string `json:"env"`
	// Cwd is the working directory, already translated to a host path.
	Cwd string `json:"cwd"`
	// TTY is always false from the shim; true is rejected with
	// ErrCodeTTYUnsupported.
	TTY bool `json:"tty"`
	// Term carries $TERM for diagnostics.
	Term string `json:"term,omitempty"`
	// Winsize carries the client terminal size when known.
	Winsize *Winsize `json:"winsize,omitempty"`
}

// EncodeHello marshals a Hello into a HELLO frame payload.
func EncodeHello(h Hello) ([]byte, error) {
	data, err := json.Marshal(h)
	if err != nil {
		return nil, fmt.Errorf("bridgeproto: encoding hello: %w", err)
	}
	return data, nil
}

// DecodeHello unmarshals a HELLO frame payload. Unknown fields are tolerated
// for forward compatibility within a protocol version.
func DecodeHello(payload []byte) (Hello, error) {
	var h Hello
	if err := json.Unmarshal(payload, &h); err != nil {
		return Hello{}, fmt.Errorf("bridgeproto: decoding hello: %w", err)
	}
	return h, nil
}

// ErrorInfo is the JSON payload of the ERROR frame.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// EncodeError marshals an ErrorInfo into an ERROR frame payload.
func EncodeError(e ErrorInfo) ([]byte, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("bridgeproto: encoding error frame: %w", err)
	}
	return data, nil
}

// DecodeError unmarshals an ERROR frame payload.
func DecodeError(payload []byte) (ErrorInfo, error) {
	var e ErrorInfo
	if err := json.Unmarshal(payload, &e); err != nil {
		return ErrorInfo{}, fmt.Errorf("bridgeproto: decoding error frame: %w", err)
	}
	return e, nil
}
