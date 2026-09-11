package runio

import (
	"slices"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
)

func TestComposeContractEnv(t *testing.T) {
	withFiles := &config.DweConfig{
		Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
		Compose: config.ComposeConfig{Base: "compose.yaml"},
		Services: map[string]config.ServiceConfig{
			"catalog": {Enabled: true, Compose: []string{"/abs/catalog.yml"}},
		},
	}
	tests := []struct {
		name string
		rc   spec.RunContext
		want []string
	}{
		{
			name: "nil config omits both",
			rc:   spec.RunContext{ProjectRoot: "/project"},
			want: nil,
		},
		{
			name: "name without files",
			rc: spec.RunContext{
				Config:      &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
				ProjectRoot: "/project",
			},
			want: []string{"COMPOSE_PROJECT_NAME=dwe-laravel"},
		},
		{
			name: "relative files joined against the root, absolute kept",
			rc:   spec.RunContext{Config: withFiles, ProjectRoot: "/project"},
			want: []string{
				"COMPOSE_PROJECT_NAME=dwe-laravel",
				"COMPOSE_FILE=/project/compose.yaml:/abs/catalog.yml",
			},
		},
		{
			name: "no root leaves relative files as-is",
			rc:   spec.RunContext{Config: withFiles},
			want: []string{
				"COMPOSE_PROJECT_NAME=dwe-laravel",
				"COMPOSE_FILE=compose.yaml:/abs/catalog.yml",
			},
		},
		{
			name: "docker config project_name wins and is lowercased",
			rc: spec.RunContext{
				Config:       &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
				DockerConfig: &config.DockerConfig{ProjectName: "Custom"},
				ProjectRoot:  "/project",
			},
			want: []string{"COMPOSE_PROJECT_NAME=custom"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComposeContractEnv(tt.rc)
			if !slices.Equal(got, tt.want) {
				t.Errorf("ComposeContractEnv = %q, want %q", got, tt.want)
			}
		})
	}
}
