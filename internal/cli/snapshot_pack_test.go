package cli

import (
	"reflect"
	"testing"

	"devbox-cli/internal/core/project/config"
)

func TestMergeExcludes(t *testing.T) {
	cases := []struct {
		name   string
		cfg    *config.SnapshotConfig
		cli    []string
		expect []string
	}{
		{"nil config", nil, []string{"a"}, []string{"a"}},
		{
			"config only",
			&config.SnapshotConfig{Pack: config.SnapshotPackConfig{Exclude: []string{"**/*.tmp"}}},
			nil,
			[]string{"**/*.tmp"},
		},
		{
			"append cli onto config",
			&config.SnapshotConfig{Pack: config.SnapshotPackConfig{Exclude: []string{"**/*.tmp"}}},
			[]string{".cache/**"},
			[]string{"**/*.tmp", ".cache/**"},
		},
		{
			"empty both",
			&config.SnapshotConfig{},
			nil,
			[]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeExcludes(tc.cfg, tc.cli)
			if !reflect.DeepEqual(got, tc.expect) {
				t.Errorf("got %v want %v", got, tc.expect)
			}
		})
	}
}

func TestDeriveNameFromTarPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/x/y/foo.tar.gz", "foo"},
		{"foo.TAR.GZ", "foo"},
		{"foo.tgz", "foo"},
		{"foo", "foo"},
	}
	for _, tc := range cases {
		if got := deriveNameFromTarPath(tc.in); got != tc.want {
			t.Errorf("derive(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}
