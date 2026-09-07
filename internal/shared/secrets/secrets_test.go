package secrets

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// testIdentity mints a throwaway identity for a test.
func testIdentity(t *testing.T) Identity {
	t.Helper()
	id, err := Keygen()
	if err != nil {
		t.Fatalf("Keygen: %v", err)
	}
	return id
}

func TestIsMarker(t *testing.T) {
	id := testIdentity(t)
	real, err := Encrypt("hunter2", id.Recipient())
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"generated marker", real, true},
		{"minimal payload", "ENC[age:a]", true},
		{"base64 padding", "ENC[age:YWJj=]", true},
		{"empty payload", "ENC[age:]", false},
		{"empty string", "", false},
		{"prefix only", "ENC[age:", false},
		{"missing close", "ENC[age:YWJj", false},
		{"trailing junk", real + " ", false},
		{"leading junk", "x" + real, false},
		{"inside text", "token is " + real + " here", false},
		{"other bracket content", "ENC[gpg:YWJj]", false},
		{"plain text containing ENC[", "see ENC[ in the docs", false},
		{"illegal payload char", "ENC[age:abc def]", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsMarker(tc.in); got != tc.want {
				t.Fatalf("IsMarker(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestContainsMarker(t *testing.T) {
	id := testIdentity(t)
	real, err := Encrypt("hunter2", id.Recipient())
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"whole scalar", real, true},
		{"embedded in a rendered line", "TOKEN=" + real + "\nOTHER=1\n", true},
		{"two markers", real + " " + real, true},
		{"plain text", "TOKEN=hunter2", false},
		{"prefix only", "ENC[age:", false},
		{"empty payload", "ENC[age:]", false},
		{"empty string", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ContainsMarker(tc.in); got != tc.want {
				t.Fatalf("ContainsMarker(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	id := testIdentity(t)
	for _, plain := range []string{"hunter2", "", "многобайтный пароль", "with 'quote' and \"dquote\"", strings.Repeat("x", 5000)} {
		marker, err := Encrypt(plain, id.Recipient())
		if err != nil {
			t.Fatalf("Encrypt(%q): %v", plain, err)
		}
		if !IsMarker(marker) {
			t.Fatalf("Encrypt(%q) produced a non-marker: %q", plain, marker)
		}
		if strings.Contains(marker, plain) && plain != "" {
			t.Fatalf("marker leaks the plaintext: %q", marker)
		}
		got, err := Decrypt(marker, id)
		if err != nil {
			t.Fatalf("Decrypt(%q): %v", plain, err)
		}
		if got != plain {
			t.Fatalf("round trip: got %q, want %q", got, plain)
		}
	}
}

func TestEncryptDecryptNotDeterministic(t *testing.T) {
	id := testIdentity(t)
	a, err := Encrypt("same", id.Recipient())
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	b, err := Encrypt("same", id.Recipient())
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if a == b {
		t.Fatalf("two encryptions of the same value produced identical markers")
	}
	for _, m := range []string{a, b} {
		plain, err := Decrypt(m, id)
		if err != nil || plain != "same" {
			t.Fatalf("Decrypt = %q, %v; want %q, nil", plain, err, "same")
		}
	}
}

func TestEncryptBytesRoundTrip(t *testing.T) {
	id := testIdentity(t)
	payload := []byte("{\n  \"type\": \"service_account\"\n}\n")
	ct, err := EncryptBytes(payload, id.Recipient())
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	if !strings.HasPrefix(string(ct), "age-encryption.org/v1") {
		t.Fatalf("EncryptBytes did not produce a native age file: %q", string(ct[:min(len(ct), 32)]))
	}
	got, err := DecryptBytes(ct, id)
	if err != nil {
		t.Fatalf("DecryptBytes: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("DecryptBytes = %q, want %q", got, payload)
	}
}

func TestEncryptRejectsBadRecipient(t *testing.T) {
	if _, err := Encrypt("x", ""); err == nil {
		t.Fatalf("Encrypt with an empty recipient: expected error")
	}
	if _, err := Encrypt("x", "age1nonsense"); err == nil {
		t.Fatalf("Encrypt with a malformed recipient: expected error")
	}
	if _, err := EncryptBytes([]byte("x"), "not-a-recipient"); err == nil {
		t.Fatalf("EncryptBytes with a malformed recipient: expected error")
	}
}

func TestDecryptErrors(t *testing.T) {
	id := testIdentity(t)
	other := testIdentity(t)
	marker, err := Encrypt("hunter2", id.Recipient())
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Truncate the base64 payload to half its length: still base64, no longer
	// a valid age file.
	payload := marker[len(MarkerPrefix) : len(marker)-1]
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	truncated := MarkerPrefix + base64.StdEncoding.EncodeToString(raw[:len(raw)/2]) + "]"

	cases := []struct {
		name   string
		marker string
		id     Identity
		want   error
	}{
		{"not a marker", "plain value", id, ErrCorrupt},
		{"invalid base64", "ENC[age:====]", id, ErrCorrupt},
		{"truncated payload", truncated, id, ErrCorrupt},
		{"wrong identity", marker, other, ErrWrongIdentity},
		{"zero identity", marker, Identity{}, ErrNoIdentity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decrypt(tc.marker, tc.id)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Decrypt error = %v, want %v", err, tc.want)
			}
			if got != "" {
				t.Fatalf("Decrypt returned %q on error", got)
			}
		})
	}
}

func TestDecryptBytesErrors(t *testing.T) {
	id := testIdentity(t)
	other := testIdentity(t)
	ct, err := EncryptBytes([]byte("payload"), id.Recipient())
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	if _, err := DecryptBytes(ct, other); !errors.Is(err, ErrWrongIdentity) {
		t.Fatalf("DecryptBytes with a foreign identity: got %v, want ErrWrongIdentity", err)
	}
	if _, err := DecryptBytes([]byte("not an age file"), id); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("DecryptBytes on garbage: got %v, want ErrCorrupt", err)
	}
	if _, err := DecryptBytes(ct, Identity{}); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("DecryptBytes with a zero identity: got %v, want ErrNoIdentity", err)
	}
}

func TestMarkerPayloadOpensWithPlainAge(t *testing.T) {
	// The documented escape hatch: base64 -d | age -d -i key. Proven here by
	// decoding the payload by hand and feeding it to DecryptBytes.
	id := testIdentity(t)
	marker, err := Encrypt("hunter2", id.Recipient())
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(marker[len(MarkerPrefix) : len(marker)-1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	plain, err := DecryptBytes(raw, id)
	if err != nil {
		t.Fatalf("DecryptBytes on the decoded payload: %v", err)
	}
	if string(plain) != "hunter2" {
		t.Fatalf("got %q, want %q", plain, "hunter2")
	}
}

// CheckMarker is the identity-free damage check: it must accept anything
// Encrypt produced and reject payloads that are not age files, so a machine
// with no key can still tell "I have no key" from "this value is broken".
func TestCheckMarker(t *testing.T) {
	id := testIdentity(t)
	real, err := Encrypt("hunter2", id.Recipient())
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	truncated := MarkerPrefix + base64.StdEncoding.EncodeToString(
		[]byte("age-encryption.org/v1\n-> X25519 tru")) + "]"

	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"generated marker", real, false},
		// Truncation is invisible without a key: the header is intact, so
		// CheckMarker must pass it and leave the failure to the decrypt.
		{"truncated age file", truncated, false},
		{"not a marker", "plain value", true},
		{"invalid base64", "ENC[age:YWJj=====]", true},
		{"valid base64, not an age file", "ENC[age:" + base64.StdEncoding.EncodeToString([]byte("nope")) + "]", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckMarker(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("CheckMarker(%q) = nil, want an error", tc.in)
				}
				if !errors.Is(err, ErrCorrupt) {
					t.Fatalf("CheckMarker(%q) error %v does not wrap ErrCorrupt", tc.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckMarker(%q) = %v, want nil", tc.in, err)
			}
		})
	}
}
