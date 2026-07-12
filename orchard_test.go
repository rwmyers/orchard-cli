package main

import (
	"io"
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

func TestFilterPlantedWorktrees(t *testing.T) {
	rootTree := "/path/to/plant/root"
	plantDir := "/path/to/plant"

	tests := []struct {
		name      string
		worktrees []gitWorktree
		want      []plantedWorktree
	}{
		{
			name:      "no worktrees",
			worktrees: nil,
			want:      nil,
		},
		{
			name: "root tree excluded",
			worktrees: []gitWorktree{
				{Path: "/path/to/plant/root"},
			},
			want: nil,
		},
		{
			name: "worktree outside plant dir excluded",
			worktrees: []gitWorktree{
				{Path: "/somewhere/else/wt1"},
			},
			want: nil,
		},
		{
			name: "planted worktrees listed with names",
			worktrees: []gitWorktree{
				{Path: "/path/to/plant/root"},
				{Path: "/path/to/plant/wt1"},
				{Path: "/path/to/plant/wt2"},
				{Path: "/somewhere/else/wt3"},
			},
			want: []plantedWorktree{
				{Name: "wt1", Path: "/path/to/plant/wt1"},
				{Name: "wt2", Path: "/path/to/plant/wt2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterPlantedWorktrees(tt.worktrees, rootTree, plantDir)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("filterPlantedWorktrees() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestArgValidation(t *testing.T) {
	// Arg validation runs before RunE, so no config file is needed for the
	// error cases below.
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "add with no args", args: []string{"add"}, wantErr: true},
		{name: "add with too many args", args: []string{"add", "wt1", "main", "extra"}, wantErr: true},
		{name: "remove with no args", args: []string{"remove"}, wantErr: true},
		{name: "remove with too many args", args: []string{"remove", "wt1", "wt2"}, wantErr: true},
		{name: "root with args", args: []string{"root", "extra"}, wantErr: true},
		{name: "list with args", args: []string{"list", "extra"}, wantErr: true},
		{name: "unknown subcommand", args: []string{"bogus"}, wantErr: true},
		{name: "help", args: []string{"--help"}, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newRootCmd()
			cmd.SetArgs(tt.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			err := cmd.Execute()
			if (err != nil) != tt.wantErr {
				t.Errorf("Execute(%v) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
		})
	}
}
