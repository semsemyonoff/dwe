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

func TestIsDaemonUnavailableErr(t *testing.T) {
	tests := []struct {
		stderr string
		want   bool
	}{
		{"Cannot connect to the Docker daemon at unix:///var/run/docker.sock", true},
		{"Cannot connect to the Docker daemon", true},
		{"No such container: mycontainer", false},
		{"permission denied", false},
		{"", false},
	}
	for _, tc := range tests {
		got := IsDaemonUnavailableErr(tc.stderr)
		if got != tc.want {
			t.Errorf("IsDaemonUnavailableErr(%q) = %v, want %v", tc.stderr, got, tc.want)
		}
	}
}
