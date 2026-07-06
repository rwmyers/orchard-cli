package main

import (
	"reflect"
	"testing"
)

func TestWorktreeExists(t *testing.T) {
	plantDir := "/path/to/plant"

	t.Run("existing worktree", func(t *testing.T) {
		worktrees := []gitWorktree{
			{Path: "/path/to/plant/wt1"},
		}

		exists := worktreeExists(worktrees, plantDir, "wt1")
		if !exists {
			t.Errorf("expected wt1 to exist")
		}
	})

	t.Run("non-existing worktree", func(t *testing.T) {
		worktrees := []gitWorktree{
			{Path: "/path/to/plant/wt1"},
		}

		exists := worktreeExists(worktrees, plantDir, "wt2")
		if exists {
			t.Errorf("expected wt2 to not exist")
		}
	})

	t.Run("multiple worktrees, match second", func(t *testing.T) {
		worktrees := []gitWorktree{
			{Path: "/path/to/plant/wt1"},
			{Path: "/path/to/plant/wt2"},
		}

		exists := worktreeExists(worktrees, plantDir, "wt2")
		if !exists {
			t.Errorf("expected wt2 to exist")
		}
	})
}

func TestParseWorktrees(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []gitWorktree
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
			want: []gitWorktree{
				{Path: "/path/to/plant/wt1"},
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
			want: []gitWorktree{
				{Path: "/path/to/plant/wt1"},
				{Path: "/path/to/plant/wt2"},
			},
		},
		{
			name: "prunable worktree",
			input: `worktree /path/to/plant/wt2
HEAD c876b767ca3a361d934aec79846c000966825c80
branch refs/heads/wt2
prunable gitdir file points to non-existent location
`,
			want: []gitWorktree{
				{Path: "/path/to/plant/wt2"},
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
