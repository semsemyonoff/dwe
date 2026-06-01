package spec

import (
	"sort"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/filesgate"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
)

func TestResolveRequireIDs(t *testing.T) {
	tests := []struct {
		name       string
		require    filesgate.RequireSpec
		defFiles   map[string]model.FileSpec
		wantIDs    []string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:    "RequireRequired with required read file",
			require: filesgate.RequireRequired{},
			defFiles: map[string]model.FileSpec{
				"dump": {Access: model.FileAccessRead, Required: true},
			},
			wantIDs: []string{"dump"},
		},
		{
			name:    "RequireRequired with optional read file",
			require: filesgate.RequireRequired{},
			defFiles: map[string]model.FileSpec{
				"opt": {Access: model.FileAccessRead, Required: false},
			},
			wantErr:    true,
			wantErrMsg: "selected no files",
		},
		{
			name:    "RequireRequired with read_write file (always included)",
			require: filesgate.RequireRequired{},
			defFiles: map[string]model.FileSpec{
				"config": {Access: model.FileAccessReadWrite, Required: false},
			},
			wantIDs: []string{"config"},
		},
		{
			name:    "RequireRequired with mixed files",
			require: filesgate.RequireRequired{},
			defFiles: map[string]model.FileSpec{
				"required_read": {Access: model.FileAccessRead, Required: true},
				"optional_read": {Access: model.FileAccessRead, Required: false},
				"read_write":    {Access: model.FileAccessReadWrite, Required: false},
				"write_only":    {Access: model.FileAccessWrite, Required: false},
			},
			wantIDs: []string{"read_write", "required_read"},
		},
		{
			name:    "RequireAll selects all read and read_write",
			require: filesgate.RequireAll{},
			defFiles: map[string]model.FileSpec{
				"required_read": {Access: model.FileAccessRead, Required: true},
				"optional_read": {Access: model.FileAccessRead, Required: false},
				"read_write":    {Access: model.FileAccessReadWrite, Required: false},
				"write_only":    {Access: model.FileAccessWrite, Required: false},
			},
			wantIDs: []string{"optional_read", "read_write", "required_read"},
		},
		{
			name:    "RequireAll with no reads",
			require: filesgate.RequireAll{},
			defFiles: map[string]model.FileSpec{
				"write_only": {Access: model.FileAccessWrite},
			},
			wantErr:    true,
			wantErrMsg: "selected no files",
		},
		{
			name:    "RequireList with explicit file-ids",
			require: filesgate.RequireList{IDs: []string{"dump", "backup"}},
			defFiles: map[string]model.FileSpec{
				"dump":   {Access: model.FileAccessRead, Required: true},
				"backup": {Access: model.FileAccessRead, Required: true},
			},
			wantIDs: []string{"dump", "backup"},
		},
		{
			name:    "RequireList with unknown file-id",
			require: filesgate.RequireList{IDs: []string{"unknown"}},
			defFiles: map[string]model.FileSpec{
				"dump": {Access: model.FileAccessRead},
			},
			wantErr:    true,
			wantErrMsg: "does not exist",
		},
		{
			name:    "RequireList with write-only file rejected",
			require: filesgate.RequireList{IDs: []string{"output"}},
			defFiles: map[string]model.FileSpec{
				"output": {Access: model.FileAccessWrite},
			},
			wantErr:    true,
			wantErrMsg: "access=write",
		},
		{
			name:    "RequireList with read_write allowed",
			require: filesgate.RequireList{IDs: []string{"rw"}},
			defFiles: map[string]model.FileSpec{
				"rw": {Access: model.FileAccessReadWrite},
			},
			wantIDs: []string{"rw"},
		},
		{
			name:    "RequireList output is sorted",
			require: filesgate.RequireList{IDs: []string{"z", "a", "m"}},
			defFiles: map[string]model.FileSpec{
				"z": {Access: model.FileAccessRead},
				"a": {Access: model.FileAccessRead},
				"m": {Access: model.FileAccessRead},
			},
			wantIDs: []string{"a", "m", "z"},
		},
		{
			name:    "RequireRequired with no required reads",
			require: filesgate.RequireRequired{},
			defFiles: map[string]model.FileSpec{
				"opt1": {Access: model.FileAccessRead, Required: false},
				"opt2": {Access: model.FileAccessRead, Required: false},
			},
			wantErr:    true,
			wantErrMsg: "selected no files",
		},
		{
			name:     "nil require defaults to RequireRequired",
			require:  nil,
			defFiles: map[string]model.FileSpec{"dump": {Access: model.FileAccessRead, Required: true}},
			wantIDs:  []string{"dump"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveRequireIDs(tt.require, tt.defFiles)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveRequireIDs error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.wantErrMsg != "" && err != nil {
				if !contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("error message %q does not contain %q", err.Error(), tt.wantErrMsg)
				}
			}
			if !tt.wantErr {
				// Sort both for comparison.
				gotSorted := make([]string, len(got))
				copy(gotSorted, got)
				sort.Strings(gotSorted)
				wantSorted := make([]string, len(tt.wantIDs))
				copy(wantSorted, tt.wantIDs)
				sort.Strings(wantSorted)

				if !stringsEqual(gotSorted, wantSorted) {
					t.Errorf("got %v, want %v", gotSorted, wantSorted)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}
