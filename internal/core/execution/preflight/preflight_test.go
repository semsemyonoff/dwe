package preflight

import (
	"reflect"
	"testing"
)

func TestStagesForPreflight(t *testing.T) {
	cases := []struct {
		stage string
		want  []string
	}{
		// deploy is the only stage with a second (post-setup) moment: the final
		// preflight runs both, so post-setup checks skipped at the early
		// pre-wizard gate execute here.
		{"deploy", []string{"deploy", "post-setup"}},
		{"run", []string{"run"}},
		{"stop", []string{"stop"}},
		{"command", []string{"command"}},
		// Empty stage → nil, which AllForStages treats as "match every check".
		{"", nil},
	}
	for _, tc := range cases {
		t.Run(tc.stage, func(t *testing.T) {
			if got := stagesForPreflight(tc.stage); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("stagesForPreflight(%q) = %v, want %v", tc.stage, got, tc.want)
			}
		})
	}
}
