package bridgeproto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// tokenBytes is the raw entropy of a bridge token (256 bits).
const tokenBytes = 32

// GenerateToken returns a fresh 256-bit token as 64 lowercase hex characters.
func GenerateToken() (string, error) {
	var b [tokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("bridgeproto: generating token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// WriteTokenFile writes the token to path with mode 0600, followed by a
// trailing newline for shell friendliness. The file is read inside containers
// via the read-only bridge mount.
func WriteTokenFile(path, token string) error {
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("bridgeproto: writing token file: %w", err)
	}
	return nil
}

// ReadTokenFile reads a token written by WriteTokenFile, trimming surrounding
// whitespace.
func ReadTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("bridgeproto: reading token file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// TokenEqual compares two tokens in constant time. Empty tokens never match
// anything — including another empty token — so a missing token file can
// never authenticate a connection.
func TokenEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
