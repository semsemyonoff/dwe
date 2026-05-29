package spec

import "testing"

func TestEnvLineKey_Empty(t *testing.T) {
	if k := EnvLineKey(""); k != "" {
		t.Errorf("empty line should return empty key, got %q", k)
	}
}

func TestEnvLineKey_Comment(t *testing.T) {
	if k := EnvLineKey("# APP_KEY=foo"); k != "" {
		t.Errorf("comment line should return empty key, got %q", k)
	}
}

func TestEnvLineKey_Basic(t *testing.T) {
	if k := EnvLineKey("APP_KEY=secret"); k != "APP_KEY" {
		t.Errorf("expected APP_KEY, got %q", k)
	}
}

func TestEnvLineKey_WithSpaces(t *testing.T) {
	if k := EnvLineKey("  DB_HOST = localhost  "); k != "DB_HOST" {
		t.Errorf("expected DB_HOST, got %q", k)
	}
}

func TestEnvLineKey_NoEquals(t *testing.T) {
	if k := EnvLineKey("NOEQUALS"); k != "NOEQUALS" {
		t.Errorf("line without equals should return full trimmed text as key, got %q", k)
	}
}

func TestParseEnvKeys_Basic(t *testing.T) {
	data := []byte("APP_KEY=secret\n# comment\n\nDB_HOST=localhost\n")
	keys := ParseEnvKeys(data)
	if !keys["APP_KEY"] {
		t.Error("expected APP_KEY in result")
	}
	if !keys["DB_HOST"] {
		t.Error("expected DB_HOST in result")
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d: %v", len(keys), keys)
	}
}

func TestParseEnvKeys_Empty(t *testing.T) {
	keys := ParseEnvKeys([]byte(""))
	if len(keys) != 0 {
		t.Errorf("expected empty keys map, got %v", keys)
	}
}

func TestParseEnvEntries(t *testing.T) {
	t.Parallel()
	data := []byte(`
# comment
KEY_EMPTY=
KEY_VAL=value
KEY_DQ_EMPTY=""
KEY_SQ_EMPTY=''
KEY_DQ=" v "
KEY_SQ='hi'
KEY_MISMATCH="value'

KEY_PLAIN=plain
`)
	got := ParseEnvEntries(data)
	want := map[string]string{
		"KEY_EMPTY":    "",
		"KEY_VAL":      "value",
		"KEY_DQ_EMPTY": "",
		"KEY_SQ_EMPTY": "",
		"KEY_DQ":       " v ",
		"KEY_SQ":       "hi",
		"KEY_MISMATCH": `"value'`,
		"KEY_PLAIN":    "plain",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: got %q want %q", k, got[k], v)
		}
	}
}
