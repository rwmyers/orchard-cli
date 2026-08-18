package git

import (
	"reflect"
	"testing"

	"github.com/rwmyers/orchard-cli/vcs"
)

func TestParseWorktrees(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []vcs.Worktree
		wantErr bool
	}{
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name: "single worktree",
			input: `worktree /path/to/plant/wt1
HEAD c876b767ca3a361d934aec79846c000966825c80
branch refs/heads/wt1
`,
			want: []vcs.Worktree{
				{Name: "wt1", Path: "/path/to/plant/wt1"},
			},
		},
		{
			name: "multiple worktrees",
			input: `worktree /path/to/plant/wt1
HEAD c876b767ca3a361d934aec79846c000966825c80
branch refs/heads/wt1

worktree /path/to/plant/wt2
HEAD 123456
branch refs/heads/wt2
`,
			want: []vcs.Worktree{
				{Name: "wt1", Path: "/path/to/plant/wt1"},
				{Name: "wt2", Path: "/path/to/plant/wt2"},
			},
		},
		{
			name: "prunable worktree",
			input: `worktree /path/to/plant/wt2
HEAD c876b767ca3a361d934aec79846c000966825c80
branch refs/heads/wt2
prunable gitdir file points to non-existent location
`,
			want: []vcs.Worktree{
				{Name: "wt2", Path: "/path/to/plant/wt2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWorktrees([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("parseWorktrees() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseWorktrees() = %v, want %v", got, tt.want)
			}
		})
	}
}
