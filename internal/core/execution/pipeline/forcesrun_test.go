package pipeline

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

func TestStepForcesRun(t *testing.T) {
	tests := []struct {
		name string
		rs   ResolvedStep
		want bool
	}{
		{
			name: "check step forces run",
			rs: ResolvedStep{Step: config.DeployStep{
				Name:  "migrate",
				Type:  "shell",
				Cmd:   "make migrate",
				Check: &config.Action{Type: "builtin", Cmd: "file_exists", With: map[string]any{"path": "x"}},
			}},
			want: true,
		},
		{
			name: "predicate body forces run",
			rs: ResolvedStep{Step: config.DeployStep{
				Name: "assert-config",
				Type: "builtin",
				Cmd:  "file_exists",
				With: map[string]any{"path": "x"},
			}},
			want: true,
		},
		{
			name: "shell predicate builtin body forces run",
			rs: ResolvedStep{Step: config.DeployStep{
				Name: "assert-shell",
				Type: "builtin",
				Cmd:  "shell",
				With: map[string]any{"cmd": "true"},
			}},
			want: true,
		},
		{
			name: "action builtin body does not force run",
			rs: ResolvedStep{Step: config.DeployStep{
				Name: "wait",
				Type: "builtin",
				Cmd:  "docker_wait_healthy",
			}},
			want: false,
		},
		{
			name: "shell step whose command text is a builtin name does not force run",
			rs: ResolvedStep{Step: config.DeployStep{
				Name: "run-shell",
				Type: "shell",
				Cmd:  "file_exists",
			}},
			want: false,
		},
		{
			name: "unknown builtin name does not force run",
			rs: ResolvedStep{Step: config.DeployStep{
				Name: "bogus",
				Type: "builtin",
				Cmd:  "no_such_builtin",
			}},
			want: false,
		},
		{
			name: "parallel substep with predicate body forces run",
			rs: ResolvedStep{
				Step: config.DeployStep{Name: "group"},
				Parallel: &ResolvedParallel{Steps: []ResolvedStep{
					{Step: config.DeployStep{Name: "a", Type: "shell", Cmd: "true"}},
					{Step: config.DeployStep{Name: "b", Type: "builtin", Cmd: "tcp_reachable", With: map[string]any{"host": "h", "port": 1}}},
				}},
			},
			want: true,
		},
		{
			name: "parallel substep with check forces run",
			rs: ResolvedStep{
				Step: config.DeployStep{Name: "group"},
				Parallel: &ResolvedParallel{Steps: []ResolvedStep{
					{Step: config.DeployStep{
						Name:  "a",
						Type:  "shell",
						Cmd:   "true",
						Check: &config.Action{Type: "builtin", Cmd: "file_exists", With: map[string]any{"path": "x"}},
					}},
				}},
			},
			want: true,
		},
		{
			name: "parallel group with only action substeps does not force run",
			rs: ResolvedStep{
				Step: config.DeployStep{Name: "group"},
				Parallel: &ResolvedParallel{Steps: []ResolvedStep{
					{Step: config.DeployStep{Name: "a", Type: "shell", Cmd: "true"}},
					{Step: config.DeployStep{Name: "b", Type: "builtin", Cmd: "docker_wait_healthy"}},
				}},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StepForcesRun(tt.rs); got != tt.want {
				t.Errorf("StepForcesRun() = %v, want %v", got, tt.want)
			}
		})
	}
}
