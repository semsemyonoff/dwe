package config

import "testing"

func TestUICommandsDefaultDepth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  *DevboxConfig
		want int
	}{
		{"nil cfg", nil, 3},
		{"missing block", &DevboxConfig{}, 3},
		{"zero treated as default", &DevboxConfig{UI: UIConfig{Commands: UICommandsConfig{DefaultExpandedDepth: 0}}}, 3},
		{"explicit positive", &DevboxConfig{UI: UIConfig{Commands: UICommandsConfig{DefaultExpandedDepth: 5}}}, 5},
		{"negative clamps to 0", &DevboxConfig{UI: UIConfig{Commands: UICommandsConfig{DefaultExpandedDepth: -2}}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := UICommandsDefaultDepth(tc.cfg); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestUICommandsAutoCollapseEmpty(t *testing.T) {
	t.Parallel()
	trueVal, falseVal := true, false
	cases := []struct {
		name string
		cfg  *DevboxConfig
		want bool
	}{
		{"nil cfg", nil, true},
		{"missing block", &DevboxConfig{}, true},
		{"nil field defaults to true", &DevboxConfig{UI: UIConfig{Commands: UICommandsConfig{AutoCollapseEmpty: nil}}}, true},
		{"explicit true", &DevboxConfig{UI: UIConfig{Commands: UICommandsConfig{AutoCollapseEmpty: &trueVal}}}, true},
		{"explicit false honoured", &DevboxConfig{UI: UIConfig{Commands: UICommandsConfig{AutoCollapseEmpty: &falseVal}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := UICommandsAutoCollapseEmpty(tc.cfg); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUICommandsShowTypeBadges(t *testing.T) {
	t.Parallel()
	trueVal, falseVal := true, false
	cases := []struct {
		name string
		cfg  *DevboxConfig
		want bool
	}{
		{"nil cfg", nil, true},
		{"missing block", &DevboxConfig{}, true},
		{"nil field defaults to true", &DevboxConfig{UI: UIConfig{Commands: UICommandsConfig{ShowTypeBadges: nil}}}, true},
		{"explicit true", &DevboxConfig{UI: UIConfig{Commands: UICommandsConfig{ShowTypeBadges: &trueVal}}}, true},
		{"explicit false honoured", &DevboxConfig{UI: UIConfig{Commands: UICommandsConfig{ShowTypeBadges: &falseVal}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := UICommandsShowTypeBadges(tc.cfg); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
