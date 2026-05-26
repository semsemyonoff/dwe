package i18n

import (
	"testing"
)

func TestStoreT(t *testing.T) {
	tests := []struct {
		name     string
		store    *Store
		locale   string
		key      string
		fallback string
		want     string
	}{
		{
			name: "hit in requested locale",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {UI: map[string]string{"ui.test": "English"}},
					"ru": {UI: map[string]string{"ui.test": "Русский"}},
				},
			},
			locale:   "ru",
			key:      "ui.test",
			fallback: "fallback",
			want:     "Русский",
		},
		{
			name: "miss in locale, hit in en",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {UI: map[string]string{"ui.test": "English"}},
					"ru": {UI: map[string]string{}},
				},
			},
			locale:   "ru",
			key:      "ui.test",
			fallback: "fallback",
			want:     "English",
		},
		{
			name: "miss everywhere, use fallback",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {UI: map[string]string{}},
					"ru": {UI: map[string]string{}},
				},
			},
			locale:   "ru",
			key:      "ui.unknown",
			fallback: "fallback",
			want:     "fallback",
		},
		{
			name: "empty fallback returns empty string",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {UI: map[string]string{}},
				},
			},
			locale:   "ru",
			key:      "ui.unknown",
			fallback: "",
			want:     "",
		},
		{
			name:     "nil store returns fallback",
			store:    nil,
			locale:   "ru",
			key:      "ui.test",
			fallback: "fallback",
			want:     "fallback",
		},
		{
			name: "empty value skipped, falls back to en",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {UI: map[string]string{"ui.test": "English"}},
					"ru": {UI: map[string]string{"ui.test": ""}},
				},
			},
			locale:   "ru",
			key:      "ui.test",
			fallback: "fallback",
			want:     "English",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.store.T(tt.locale, tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("T(%q, %q, %q) = %q, want %q", tt.locale, tt.key, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestStoreCommandDescription(t *testing.T) {
	tests := []struct {
		name      string
		store     *Store
		locale    string
		commandID string
		fallback  string
		want      string
	}{
		{
			name: "hit in requested locale",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {Commands: map[string]CommandStrings{
						"build.docker": {Description: "Build Docker image"},
					}},
					"ru": {Commands: map[string]CommandStrings{
						"build.docker": {Description: "Собрать Docker образ"},
					}},
				},
			},
			locale:    "ru",
			commandID: "build.docker",
			fallback:  "fallback",
			want:      "Собрать Docker образ",
		},
		{
			name: "complex dotted ID",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {Commands: map[string]CommandStrings{
						"services.main.db.migrate": {Description: "Run migration"},
					}},
				},
			},
			locale:    "en",
			commandID: "services.main.db.migrate",
			fallback:  "default",
			want:      "Run migration",
		},
		{
			name: "miss in locale, hit in en",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {Commands: map[string]CommandStrings{
						"build.docker": {Description: "Build Docker image"},
					}},
					"ru": {Commands: map[string]CommandStrings{}},
				},
			},
			locale:    "ru",
			commandID: "build.docker",
			fallback:  "fallback",
			want:      "Build Docker image",
		},
		{
			name: "miss everywhere, use fallback",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {Commands: map[string]CommandStrings{}},
				},
			},
			locale:    "en",
			commandID: "unknown.cmd",
			fallback:  "fallback",
			want:      "fallback",
		},
		{
			name:      "nil store returns fallback",
			store:     nil,
			locale:    "ru",
			commandID: "build.docker",
			fallback:  "fallback",
			want:      "fallback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.store.CommandDescription(tt.locale, tt.commandID, tt.fallback)
			if got != tt.want {
				t.Errorf("CommandDescription(%q, %q, %q) = %q, want %q", tt.locale, tt.commandID, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestStoreCommandConfirmationText(t *testing.T) {
	tests := []struct {
		name      string
		store     *Store
		locale    string
		commandID string
		fallback  string
		want      string
	}{
		{
			name: "hit in requested locale",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {Commands: map[string]CommandStrings{
						"stop.service": {ConfirmationText: "Are you sure?"},
					}},
					"ru": {Commands: map[string]CommandStrings{
						"stop.service": {ConfirmationText: "Вы уверены?"},
					}},
				},
			},
			locale:    "ru",
			commandID: "stop.service",
			fallback:  "default",
			want:      "Вы уверены?",
		},
		{
			name: "miss in locale, hit in en",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {Commands: map[string]CommandStrings{
						"stop.service": {ConfirmationText: "Are you sure?"},
					}},
					"ru": {Commands: map[string]CommandStrings{}},
				},
			},
			locale:    "ru",
			commandID: "stop.service",
			fallback:  "default",
			want:      "Are you sure?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.store.CommandConfirmationText(tt.locale, tt.commandID, tt.fallback)
			if got != tt.want {
				t.Errorf("CommandConfirmationText(%q, %q, %q) = %q, want %q", tt.locale, tt.commandID, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestStoreParamDescription(t *testing.T) {
	tests := []struct {
		name      string
		store     *Store
		locale    string
		commandID string
		paramName string
		fallback  string
		want      string
	}{
		{
			name: "hit in requested locale",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {Commands: map[string]CommandStrings{
						"build.docker": {Params: map[string]ParamStrings{
							"tag": {Description: "Image tag"},
						}},
					}},
					"ru": {Commands: map[string]CommandStrings{
						"build.docker": {Params: map[string]ParamStrings{
							"tag": {Description: "Тег образа"},
						}},
					}},
				},
			},
			locale:    "ru",
			commandID: "build.docker",
			paramName: "tag",
			fallback:  "default",
			want:      "Тег образа",
		},
		{
			name: "param name with underscore",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {Commands: map[string]CommandStrings{
						"build.docker": {Params: map[string]ParamStrings{
							"my_param": {Description: "My param"},
						}},
					}},
				},
			},
			locale:    "en",
			commandID: "build.docker",
			paramName: "my_param",
			fallback:  "default",
			want:      "My param",
		},
		{
			name: "miss in locale, hit in en",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {Commands: map[string]CommandStrings{
						"build.docker": {Params: map[string]ParamStrings{
							"tag": {Description: "Image tag"},
						}},
					}},
					"ru": {Commands: map[string]CommandStrings{}},
				},
			},
			locale:    "ru",
			commandID: "build.docker",
			paramName: "tag",
			fallback:  "default",
			want:      "Image tag",
		},
		{
			name: "miss everywhere, use fallback",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {Commands: map[string]CommandStrings{}},
				},
			},
			locale:    "en",
			commandID: "unknown.cmd",
			paramName: "param",
			fallback:  "default",
			want:      "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.store.ParamDescription(tt.locale, tt.commandID, tt.paramName, tt.fallback)
			if got != tt.want {
				t.Errorf("ParamDescription(%q, %q, %q, %q) = %q, want %q", tt.locale, tt.commandID, tt.paramName, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestStoreGroupTitle(t *testing.T) {
	tests := []struct {
		name     string
		store    *Store
		locale   string
		groupID  string
		fallback string
		want     string
	}{
		{
			name: "hit in requested locale",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {Groups: map[string]GroupStrings{
						"services.main": {Title: "Main Services"},
					}},
					"ru": {Groups: map[string]GroupStrings{
						"services.main": {Title: "Основные услуги"},
					}},
				},
			},
			locale:   "ru",
			groupID:  "services.main",
			fallback: "default",
			want:     "Основные услуги",
		},
		{
			name: "miss in locale, hit in en",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {Groups: map[string]GroupStrings{
						"services.main": {Title: "Main Services"},
					}},
					"ru": {Groups: map[string]GroupStrings{}},
				},
			},
			locale:   "ru",
			groupID:  "services.main",
			fallback: "default",
			want:     "Main Services",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.store.GroupTitle(tt.locale, tt.groupID, tt.fallback)
			if got != tt.want {
				t.Errorf("GroupTitle(%q, %q, %q) = %q, want %q", tt.locale, tt.groupID, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestStoreGroupDescription(t *testing.T) {
	tests := []struct {
		name     string
		store    *Store
		locale   string
		groupID  string
		fallback string
		want     string
	}{
		{
			name: "hit in requested locale",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {Groups: map[string]GroupStrings{
						"services.main": {Description: "Main services"},
					}},
					"ru": {Groups: map[string]GroupStrings{
						"services.main": {Description: "Основные услуги"},
					}},
				},
			},
			locale:   "ru",
			groupID:  "services.main",
			fallback: "default",
			want:     "Основные услуги",
		},
		{
			name: "miss in locale, hit in en",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {Groups: map[string]GroupStrings{
						"services.main": {Description: "Main services"},
					}},
					"ru": {Groups: map[string]GroupStrings{}},
				},
			},
			locale:   "ru",
			groupID:  "services.main",
			fallback: "default",
			want:     "Main services",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.store.GroupDescription(tt.locale, tt.groupID, tt.fallback)
			if got != tt.want {
				t.Errorf("GroupDescription(%q, %q, %q) = %q, want %q", tt.locale, tt.groupID, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestStoreAvailableLocales(t *testing.T) {
	tests := []struct {
		name  string
		store *Store
		want  []string
	}{
		{
			name:  "nil store returns [en]",
			store: nil,
			want:  []string{"en"},
		},
		{
			name: "empty locales returns [en]",
			store: &Store{
				locales: make(map[string]*Bundle),
			},
			want: []string{"en"},
		},
		{
			name: "single locale en",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {},
				},
			},
			want: []string{"en"},
		},
		{
			name: "multiple locales, en first",
			store: &Store{
				locales: map[string]*Bundle{
					"en": {},
					"ru": {},
					"de": {},
				},
			},
			want: []string{"en", "ru", "de"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.store.AvailableLocales()
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("AvailableLocales() = %v, want %v", got, tt.want)
				return
			}
			// Check first is always "en"
			if got[0] != "en" {
				t.Errorf("AvailableLocales() first element = %q, want en", got[0])
			}
		})
	}
}
