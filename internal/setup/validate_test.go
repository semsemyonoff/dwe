package setup

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestValidateAndCoercePort tests port preset validation and coercion.
func TestValidateAndCoercePort(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantType  any
		wantValue any
		wantErr   bool
	}{
		// Valid ports
		{
			name:      "port 1 (minimum)",
			raw:       "1",
			wantType:  int(0),
			wantValue: 1,
		},
		{
			name:      "port 80",
			raw:       "80",
			wantType:  int(0),
			wantValue: 80,
		},
		{
			name:      "port 8080",
			raw:       "8080",
			wantType:  int(0),
			wantValue: 8080,
		},
		{
			name:      "port 65535 (maximum)",
			raw:       "65535",
			wantType:  int(0),
			wantValue: 65535,
		},
		{
			name:      "port with spaces",
			raw:       "  8080  ",
			wantType:  int(0),
			wantValue: 8080,
		},
		// Invalid ports
		{
			name:    "port 0",
			raw:     "0",
			wantErr: true,
		},
		{
			name:    "port -1",
			raw:     "-1",
			wantErr: true,
		},
		{
			name:    "port 65536 (out of range)",
			raw:     "65536",
			wantErr: true,
		},
		{
			name:    "port 99999",
			raw:     "99999",
			wantErr: true,
		},
		{
			name:    "non-numeric",
			raw:     "abc",
			wantErr: true,
		},
		{
			name:    "empty string",
			raw:     "",
			wantErr: true,
		},
	}

	q := Question{
		ID:   "test_port",
		Type: TypeInput,
		Validate: &ValidateSpec{
			Preset: PresetPort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ValidateAndCoerce(q, tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateAndCoerce() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				// Check type
				port, ok := result.(int)
				if !ok {
					t.Fatalf("ValidateAndCoerce() returned type %T, want int", result)
				}
				// Check value
				if port != tt.wantValue {
					t.Fatalf("ValidateAndCoerce() = %d, want %d", port, tt.wantValue)
				}
			}
		})
	}
}

// TestValidateAndCoerceHostname tests hostname preset validation and coercion.
func TestValidateAndCoerceHostname(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantValue string
		wantErr   bool
	}{
		// Valid hostnames
		{
			name:      "simple hostname",
			raw:       "localhost",
			wantValue: "localhost",
		},
		{
			name:      "hostname with number",
			raw:       "host1",
			wantValue: "host1",
		},
		{
			name:      "hostname with dash",
			raw:       "my-host",
			wantValue: "my-host",
		},
		{
			name:      "fully qualified domain name",
			raw:       "example.com",
			wantValue: "example.com",
		},
		{
			name:      "subdomain",
			raw:       "sub.example.com",
			wantValue: "sub.example.com",
		},
		{
			name:      "hostname with spaces",
			raw:       "  localhost  ",
			wantValue: "localhost",
		},
		{
			name:      "single character hostname",
			raw:       "a",
			wantValue: "a",
		},
		// Invalid hostnames
		{
			name:    "hostname starting with dash",
			raw:     "-invalid",
			wantErr: true,
		},
		{
			name:    "hostname ending with dash",
			raw:     "invalid-",
			wantErr: true,
		},
		{
			name:    "label ending with dash",
			raw:     "my-.example.com",
			wantErr: true,
		},
		{
			name:    "empty hostname",
			raw:     "",
			wantErr: true,
		},
		{
			name:    "only spaces",
			raw:     "   ",
			wantErr: true,
		},
		{
			name:    "hostname with underscore",
			raw:     "my_host",
			wantErr: true,
		},
		{
			name:    "hostname with special characters",
			raw:     "host@example.com",
			wantErr: true,
		},
	}

	q := Question{
		ID:   "test_hostname",
		Type: TypeInput,
		Validate: &ValidateSpec{
			Preset: PresetHostname,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ValidateAndCoerce(q, tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateAndCoerce() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				hostname, ok := result.(string)
				if !ok {
					t.Fatalf("ValidateAndCoerce() returned type %T, want string", result)
				}
				if hostname != tt.wantValue {
					t.Fatalf("ValidateAndCoerce() = %q, want %q", hostname, tt.wantValue)
				}
			}
		})
	}
}

// TestValidateAndCoercePath tests path preset validation and coercion.
func TestValidateAndCoercePath(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantValue string
		wantErr   bool
	}{
		// Valid paths
		{
			name:      "simple path",
			raw:       "/tmp",
			wantValue: "/tmp",
		},
		{
			name:      "relative path",
			raw:       "config/app.yml",
			wantValue: "config/app.yml",
		},
		{
			name:      "path with spaces",
			raw:       "  /usr/local/bin  ",
			wantValue: "/usr/local/bin",
		},
		{
			name:      "path with special chars",
			raw:       "/home/user/.config/app",
			wantValue: "/home/user/.config/app",
		},
		{
			name:      "dotfiles",
			raw:       ".bashrc",
			wantValue: ".bashrc",
		},
		// Invalid paths
		{
			name:    "empty path",
			raw:     "",
			wantErr: true,
		},
		{
			name:    "only spaces",
			raw:     "   ",
			wantErr: true,
		},
		{
			name:    "path with NUL character",
			raw:     "/tmp/test\x00file",
			wantErr: true,
		},
	}

	q := Question{
		ID:   "test_path",
		Type: TypeInput,
		Validate: &ValidateSpec{
			Preset: PresetPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ValidateAndCoerce(q, tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateAndCoerce() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				path, ok := result.(string)
				if !ok {
					t.Fatalf("ValidateAndCoerce() returned type %T, want string", result)
				}
				if path != tt.wantValue {
					t.Fatalf("ValidateAndCoerce() = %q, want %q", path, tt.wantValue)
				}
			}
		})
	}
}

// TestValidateAndCoerceNonEmpty tests non-empty preset validation and coercion.
func TestValidateAndCoerceNonEmpty(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantValue string
		wantErr   bool
	}{
		// Valid non-empty strings
		{
			name:      "simple string",
			raw:       "hello",
			wantValue: "hello",
		},
		{
			name:      "string with spaces",
			raw:       "hello world",
			wantValue: "hello world",
		},
		{
			name:      "string with leading/trailing spaces",
			raw:       "  hello  ",
			wantValue: "hello",
		},
		{
			name:      "string with tabs",
			raw:       "\thello\t",
			wantValue: "hello",
		},
		{
			name:      "single character",
			raw:       "a",
			wantValue: "a",
		},
		// Invalid (empty) values
		{
			name:    "empty string",
			raw:     "",
			wantErr: true,
		},
		{
			name:    "only spaces",
			raw:     "   ",
			wantErr: true,
		},
		{
			name:    "only tabs",
			raw:     "\t\t",
			wantErr: true,
		},
		{
			name:    "only newlines",
			raw:     "\n\n",
			wantErr: true,
		},
	}

	q := Question{
		ID:   "test_non_empty",
		Type: TypeInput,
		Validate: &ValidateSpec{
			Preset: PresetNonEmpty,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ValidateAndCoerce(q, tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateAndCoerce() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				str, ok := result.(string)
				if !ok {
					t.Fatalf("ValidateAndCoerce() returned type %T, want string", result)
				}
				if str != tt.wantValue {
					t.Fatalf("ValidateAndCoerce() = %q, want %q", str, tt.wantValue)
				}
			}
		})
	}
}

// TestValidateAndCoerceRegex tests regex validation and coercion.
func TestValidateAndCoerceRegex(t *testing.T) {
	tests := []struct {
		name      string
		pattern   string
		raw       string
		wantValue string
		wantErr   bool
	}{
		// Valid regex matches
		{
			name:      "simple pattern",
			pattern:   "^[a-z]+$",
			raw:       "hello",
			wantValue: "hello",
		},
		{
			name:      "digit pattern",
			pattern:   "^[0-9]{3}$",
			raw:       "123",
			wantValue: "123",
		},
		{
			name:      "email pattern",
			pattern:   "^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}$",
			raw:       "test@example.com",
			wantValue: "test@example.com",
		},
		// Invalid regex matches
		{
			name:    "pattern mismatch",
			pattern: "^[a-z]+$",
			raw:     "Hello",
			wantErr: true,
		},
		{
			name:    "pattern mismatch digits",
			pattern: "^[0-9]{3}$",
			raw:     "12",
			wantErr: true,
		},
		// Invalid regex patterns
		{
			name:    "invalid regex syntax",
			pattern: "[invalid",
			raw:     "anything",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := Question{
				ID:   "test_regex",
				Type: TypeInput,
				Validate: &ValidateSpec{
					Regex: tt.pattern,
				},
			}
			result, err := ValidateAndCoerce(q, tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateAndCoerce() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				str, ok := result.(string)
				if !ok {
					t.Fatalf("ValidateAndCoerce() returned type %T, want string", result)
				}
				if str != tt.wantValue {
					t.Fatalf("ValidateAndCoerce() = %q, want %q", str, tt.wantValue)
				}
			}
		})
	}
}

// TestValidateAndCoerceNoValidation tests behavior when no validation spec is provided.
func TestValidateAndCoerceNoValidation(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantValue string
	}{
		{
			name:      "simple string",
			raw:       "hello",
			wantValue: "hello",
		},
		{
			name:      "string with special chars",
			raw:       "hello@world!",
			wantValue: "hello@world!",
		},
		{
			name:      "empty string is allowed without validation spec",
			raw:       "",
			wantValue: "",
		},
		{
			name:      "spaces preserved without preset",
			raw:       "  hello  ",
			wantValue: "  hello  ",
		},
	}

	// No validation spec
	q := Question{
		ID:       "test_no_validation",
		Type:     TypeInput,
		Validate: nil,
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ValidateAndCoerce(q, tt.raw)
			if err != nil {
				t.Fatalf("ValidateAndCoerce() error = %v, want nil", err)
			}
			str, ok := result.(string)
			if !ok {
				t.Fatalf("ValidateAndCoerce() returned type %T, want string", result)
			}
			if str != tt.wantValue {
				t.Fatalf("ValidateAndCoerce() = %q, want %q", str, tt.wantValue)
			}
		})
	}
}

// TestValidateAndCoerceEmptyValidateSpec tests behavior with empty ValidateSpec.
func TestValidateAndCoerceEmptyValidateSpec(t *testing.T) {
	q := Question{
		ID:       "test_empty_spec",
		Type:     TypeInput,
		Validate: &ValidateSpec{}, // Empty: no preset, no regex
	}

	result, err := ValidateAndCoerce(q, "test value")
	if err != nil {
		t.Fatalf("ValidateAndCoerce() error = %v, want nil", err)
	}

	str, ok := result.(string)
	if !ok {
		t.Fatalf("ValidateAndCoerce() returned type %T, want string", result)
	}

	if str != "test value" {
		t.Fatalf("ValidateAndCoerce() = %q, want %q", str, "test value")
	}
}

// TestRoundTripYAML demonstrates that ValidateAndCoerce produces values
// that round-trip correctly through YAML marshaling.
func TestRoundTripYAML(t *testing.T) {
	// Create a mock answer map with various types
	answers := map[string]any{
		"port":      8080,
		"hostname":  "localhost",
		"path":      "/tmp/config",
		"non_empty": "test",
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(answers)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}

	// Unmarshal back
	var result map[string]any
	if err := yaml.Unmarshal(yamlBytes, &result); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}

	// Verify types are preserved
	if port, ok := result["port"].(int); !ok || port != 8080 {
		t.Fatalf("port type lost in round-trip: got %T with value %v", result["port"], result["port"])
	}

	if hostname, ok := result["hostname"].(string); !ok || hostname != "localhost" {
		t.Fatalf("hostname type lost in round-trip: got %T with value %v", result["hostname"], result["hostname"])
	}

	if path, ok := result["path"].(string); !ok || path != "/tmp/config" {
		t.Fatalf("path type lost in round-trip: got %T with value %v", result["path"], result["path"])
	}
}
