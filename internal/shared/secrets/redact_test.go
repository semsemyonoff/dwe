package secrets

import (
	"slices"
	"strings"
	"testing"
)

func TestRedactorReplacesKnownValues(t *testing.T) {
	r := NewRedactor([]string{"hunter2", "s3cr3t-token"})

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain occurrence", "psql --password hunter2", "psql --password ***"},
		{"twice on one line", "hunter2 hunter2", "*** ***"},
		{"inside a quoted argument", "sh -c 'echo hunter2'", "sh -c 'echo ***'"},
		{"two different values", "a=hunter2 b=s3cr3t-token", "a=*** b=***"},
		{"no occurrence", "docker ps --all", "docker ps --all"},
		{"empty line", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.Redact(tc.in); got != tc.want {
				t.Fatalf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactorSkipsShortValues(t *testing.T) {
	r := NewRedactor([]string{"1", "ab", "abc", "abcd"})
	if got := r.Redact("1 ab abc abcd"); got != "1 ab abc ***" {
		t.Fatalf("Redact = %q, want %q", got, "1 ab abc ***")
	}
	if vals := r.Values(); !slices.Equal(vals, []string{"abcd"}) {
		t.Fatalf("Values = %v, want [abcd]", vals)
	}
}

func TestRedactorNestedValueReplacedOnce(t *testing.T) {
	// "secret-token" contains "token": longest-first ordering means the line
	// collapses to a single placeholder instead of "secret-***".
	r := NewRedactor([]string{"token", "secret-token"})
	if got := r.Redact("value=secret-token"); got != "value=***" {
		t.Fatalf("Redact = %q, want %q", got, "value=***")
	}
	if got := r.Redact("value=token"); got != "value=***" {
		t.Fatalf("Redact = %q, want %q", got, "value=***")
	}
}

func TestRedactorDeduplicatesAndOrders(t *testing.T) {
	r := NewRedactor([]string{"bbbb", "aaaaaa", "bbbb"})
	if vals := r.Values(); !slices.Equal(vals, []string{"aaaaaa", "bbbb"}) {
		t.Fatalf("Values = %v, want [aaaaaa bbbb]", vals)
	}
}

func TestRedactorEmptyAndNil(t *testing.T) {
	var nilRedactor *Redactor
	if got := nilRedactor.Redact("docker ps"); got != "docker ps" {
		t.Fatalf("nil Redact = %q", got)
	}
	if vals := nilRedactor.Values(); vals != nil {
		t.Fatalf("nil Values = %v, want nil", vals)
	}
	empty := NewRedactor(nil)
	if got := empty.Redact("docker ps"); got != "docker ps" {
		t.Fatalf("empty Redact = %q", got)
	}
}

func TestRedactorMultiByteValue(t *testing.T) {
	// Three runes, six bytes: short by rune count, so it must NOT be redacted.
	r := NewRedactor([]string{"абв"})
	if got := r.Redact("пароль абв"); got != "пароль абв" {
		t.Fatalf("Redact = %q, want the line unchanged", got)
	}

	r = NewRedactor([]string{"абвг"})
	if got := r.Redact("пароль абвг"); got != "пароль ***" {
		t.Fatalf("Redact = %q, want %q", got, "пароль ***")
	}
}

func TestRedactorHidesDecryptedSecret(t *testing.T) {
	id := testIdentity(t)
	const plain = "telegram-bot-token-42"
	marker, err := Encrypt(plain, id.Recipient())
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := Decrypt(marker, id)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	line := NewRedactor([]string{got}).Redact("curl -H 'Authorization: " + plain + "'")
	if strings.Contains(line, plain) {
		t.Fatalf("redacted line still carries the plaintext: %q", line)
	}
	if !strings.Contains(line, RedactPlaceholder) {
		t.Fatalf("redacted line has no placeholder: %q", line)
	}
}
