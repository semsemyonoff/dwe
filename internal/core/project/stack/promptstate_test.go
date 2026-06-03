package stack

import "testing"

func TestHealthState_AllMappings(t *testing.T) {
	tests := []struct {
		name string
		in   Health
		want string
	}{
		{"running", HealthRunning, "running"},
		{"partial", HealthPartial, "partial"},
		{"stopped", HealthStopped, "stopped"},
		{"unknown_falls_back_to_stopped", Health(99), "stopped"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := HealthState(tc.in)
			if got != tc.want {
				t.Fatalf("HealthState(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
