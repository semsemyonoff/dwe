package randval

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		spec string
		want Spec
	}{
		{"hex", Spec{KindHex, 32}},
		{"hex:1", Spec{KindHex, 1}},
		{"hex:16", Spec{KindHex, 16}},
		{"hex:1024", Spec{KindHex, 1024}},
		{"base64url", Spec{KindBase64URL, 32}},
		{"base64url:1", Spec{KindBase64URL, 1}},
		{"base64url:1024", Spec{KindBase64URL, 1024}},
		{"uuid", Spec{KindUUID, 16}},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			got, err := Parse(tt.spec)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.spec, err)
			}
			if got != tt.want {
				t.Fatalf("Parse(%q) = %+v, want %+v", tt.spec, got, tt.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		spec    string
		wantSub string
	}{
		{"", "unknown kind"},
		{"sha256", "unknown kind"},
		{"HEX", "unknown kind"},
		{"hex:0", "out of range"},
		{"hex:1025", "out of range"},
		{"hex:-1", "not an integer"},
		{"hex:+32", "not an integer"},
		{"hex:x", "not an integer"},
		{"hex:", "not an integer"},
		{"base64url:1025", "out of range"},
		{"uuid:16", "uuid takes no byte count"},
		{"uuid:", "uuid takes no byte count"},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			_, err := Parse(tt.spec)
			if err == nil {
				t.Fatalf("Parse(%q): want error", tt.spec)
			}
			msg := err.Error()
			if !strings.Contains(msg, tt.wantSub) {
				t.Errorf("error %q does not contain %q", msg, tt.wantSub)
			}
			if !strings.Contains(msg, "hex[:N], base64url[:N] or uuid") {
				t.Errorf("error %q does not name the accepted forms", msg)
			}
		})
	}
}

// seq returns n bytes counting up from start, wrapping at 256.
func seq(start byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = start + byte(i)
	}
	return b
}

func TestGenerateHex(t *testing.T) {
	lowerHex := regexp.MustCompile(`^[0-9a-f]+$`)
	for _, n := range []int{1, 7, 32, 1024} {
		in := seq(0xf0, n)
		got, err := Generate(Spec{KindHex, n}, bytes.NewReader(in))
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if len(got) != 2*n {
			t.Errorf("n=%d: len %d, want %d", n, len(got), 2*n)
		}
		if !lowerHex.MatchString(got) {
			t.Errorf("n=%d: %q is not lowercase hex", n, got)
		}
		dec, err := hex.DecodeString(got)
		if err != nil || !bytes.Equal(dec, in) {
			t.Errorf("n=%d: %q does not decode back to the input (err %v)", n, got, err)
		}
	}
}

func TestGenerateBase64URL(t *testing.T) {
	urlAlphabet := regexp.MustCompile(`^[A-Za-z0-9_-]*=*$`)
	for _, n := range []int{1, 2, 3, 4, 32, 1024} {
		in := seq(0xfa, n)
		got, err := Generate(Spec{KindBase64URL, n}, bytes.NewReader(in))
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if want := 4 * ((n + 2) / 3); len(got) != want {
			t.Errorf("n=%d: len %d, want %d", n, len(got), want)
		}
		if !urlAlphabet.MatchString(got) {
			t.Errorf("n=%d: %q is outside the URL alphabet", n, got)
		}
		dec, err := base64.URLEncoding.DecodeString(got)
		if err != nil || !bytes.Equal(dec, in) {
			t.Errorf("n=%d: %q does not decode back to the input (err %v)", n, got, err)
		}
	}

	// 0xfb 0xff encodes to "+/8=" in the standard alphabet: pins both the URL
	// substitutions and the padding.
	got, err := Generate(Spec{KindBase64URL, 2}, bytes.NewReader([]byte{0xfb, 0xff}))
	if err != nil {
		t.Fatal(err)
	}
	if got != "-_8=" {
		t.Errorf("got %q, want %q", got, "-_8=")
	}
}

func TestGenerateUUID(t *testing.T) {
	format := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for _, fill := range []byte{0x00, 0xff, 0x5a} {
		in := bytes.Repeat([]byte{fill}, 16)
		got, err := Generate(Spec{Kind: KindUUID}, bytes.NewReader(in))
		if err != nil {
			t.Fatalf("fill %#x: %v", fill, err)
		}
		if !format.MatchString(got) {
			t.Errorf("fill %#x: %q is not a v4 UUID (version nibble 4, variant 10xx)", fill, got)
		}
	}

	got, err := Generate(Spec{KindUUID, 16}, bytes.NewReader(seq(0x00, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if want := "00010203-0405-4607-8809-0a0b0c0d0e0f"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGenerateShortReader(t *testing.T) {
	tests := []Spec{
		{KindHex, 32},
		{KindBase64URL, 32},
		{KindUUID, 16},
	}
	for _, s := range tests {
		t.Run(string(s.Kind), func(t *testing.T) {
			got, err := Generate(s, bytes.NewReader(make([]byte, 5)))
			if err == nil {
				t.Fatalf("want error, got %q", got)
			}
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("error %v does not wrap io.ErrUnexpectedEOF", err)
			}
		})
	}
}

func TestGenerateInvalidSpec(t *testing.T) {
	for _, s := range []Spec{{KindHex, 0}, {KindBase64URL, 1025}, {Kind("sha"), 16}} {
		if _, err := Generate(s, bytes.NewReader(make([]byte, 2048))); err == nil {
			t.Errorf("Generate(%+v): want error", s)
		}
	}
}
