package config

import "testing"

func TestDockerVolumeConfigResolveName(t *testing.T) {
	tests := []struct {
		name        string
		vol         DockerVolumeConfig
		projectName string
		want        string
	}{
		{
			name:        "shared volume keeps name verbatim",
			vol:         DockerVolumeConfig{Name: "dwe_composer_cache", Shared: true},
			projectName: "dwe-laravel",
			want:        "dwe_composer_cache",
		},
		{
			name:        "non-shared volume is prefixed with project name",
			vol:         DockerVolumeConfig{Name: "build_cache"},
			projectName: "dwe-laravel",
			want:        "dwe-laravel_build_cache",
		},
		{
			name:        "shared flag wins even with empty project name",
			vol:         DockerVolumeConfig{Name: "shared_data", Shared: true},
			projectName: "",
			want:        "shared_data",
		},
		{
			name:        "empty project disables prefixing for non-shared volumes",
			vol:         DockerVolumeConfig{Name: "build_cache"},
			projectName: "",
			want:        "build_cache",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.vol.ResolveName(tt.projectName)
			if got != tt.want {
				t.Errorf("ResolveName(%q) = %q, want %q", tt.projectName, got, tt.want)
			}
		})
	}
}
