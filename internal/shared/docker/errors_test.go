package docker

import "testing"

func TestIsNoSuchContainerErr(t *testing.T) {
	tests := []struct {
		stderr string
		want   bool
	}{
		{"Error response from daemon: No such container: mycontainer", true},
		{"No such container", true},
		{"permission denied", false},
		{"", false},
		{"Cannot connect to the Docker daemon", false},
	}
	for _, tc := range tests {
		got := IsNoSuchContainerErr(tc.stderr)
		if got != tc.want {
			t.Errorf("IsNoSuchContainerErr(%q) = %v, want %v", tc.stderr, got, tc.want)
		}
	}
}
