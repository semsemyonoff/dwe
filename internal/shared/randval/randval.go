// Package randval generates random string values from a small spec grammar:
// hex[:N], base64url[:N] and uuid. N counts bytes of entropy (default 32,
// 1..1024), not output characters, so the same N means the same strength in
// every encoding.
//
// The randomness source is injected: production passes crypto/rand.Reader,
// tests a fixed reader.
package randval

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Kind names an output encoding.
type Kind string

// Supported kinds.
const (
	KindHex       Kind = "hex"
	KindBase64URL Kind = "base64url"
	KindUUID      Kind = "uuid"
)

// Byte-count bounds for hex and base64url.
const (
	DefaultBytes = 32
	MinBytes     = 1
	MaxBytes     = 1024
)

const uuidBytes = 16

// acceptedForms is quoted by every Parse error so the user sees the grammar.
const acceptedForms = "want hex[:N], base64url[:N] or uuid (N = bytes of entropy, 1..1024, default 32)"

// Spec is a parsed generator spec. Bytes is the entropy read from the
// source; it is always 16 for uuid.
type Spec struct {
	Kind  Kind
	Bytes int
}

// Parse parses a spec string. Kinds are case-sensitive and N must be a
// decimal integer in [MinBytes, MaxBytes]; uuid takes no N.
func Parse(spec string) (Spec, error) {
	kind, n, hasN := strings.Cut(spec, ":")
	switch Kind(kind) {
	case KindHex, KindBase64URL:
		if !hasN {
			return Spec{Kind: Kind(kind), Bytes: DefaultBytes}, nil
		}
		bytes, err := strconv.Atoi(n)
		if err != nil {
			return Spec{}, fmt.Errorf("invalid spec %q: byte count %q is not an integer; %s", spec, n, acceptedForms)
		}
		if bytes < MinBytes || bytes > MaxBytes {
			return Spec{}, fmt.Errorf("invalid spec %q: byte count %d out of range; %s", spec, bytes, acceptedForms)
		}
		return Spec{Kind: Kind(kind), Bytes: bytes}, nil
	case KindUUID:
		if hasN {
			return Spec{}, fmt.Errorf("invalid spec %q: uuid takes no byte count; %s", spec, acceptedForms)
		}
		return Spec{Kind: KindUUID, Bytes: uuidBytes}, nil
	default:
		return Spec{}, fmt.Errorf("invalid spec %q: unknown kind %q; %s", spec, kind, acceptedForms)
	}
}

// Generate reads s.Bytes bytes from r and encodes them per s.Kind. hex is
// lowercase; base64url is padded because consumers such as Fernet keys
// reject the unpadded form; uuid is an RFC 9562 version 4 UUID.
func Generate(s Spec, r io.Reader) (string, error) {
	n := s.Bytes
	switch s.Kind {
	case KindHex, KindBase64URL:
		if n < MinBytes || n > MaxBytes {
			return "", fmt.Errorf("byte count %d out of range %d..%d", n, MinBytes, MaxBytes)
		}
	case KindUUID:
		n = uuidBytes
	default:
		return "", fmt.Errorf("unknown kind %q", s.Kind)
	}

	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("read %d random bytes: %w", n, err)
	}

	switch s.Kind {
	case KindHex:
		return hex.EncodeToString(buf), nil
	case KindBase64URL:
		return base64.URLEncoding.EncodeToString(buf), nil
	default:
		return formatUUIDv4(buf), nil
	}
}

func formatUUIDv4(b []byte) string {
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // variant 10xx
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
