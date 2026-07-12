package main

import (
	"bufio"
	"io"
	"reflect"
	"strings"
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

	t.Run("remove accepts multiple args", func(t *testing.T) {
		// A nonexistent config makes RunE fail at config loading, proving
		// multiple worktree names get past arg validation.
		cmd := newRootCmd()
		cmd.SetArgs([]string{"remove", "wt1", "wt2", "--config", "/nonexistent/orchard.conf"})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "reading config") {
			t.Errorf("Execute() error = %v, want config loading error", err)
		}
	})

	t.Run("remove accepts no args", func(t *testing.T) {
		// No args passes validation (interactive mode) and reaches RunE,
		// where the nonexistent config fails.
		cmd := newRootCmd()
		cmd.SetArgs([]string{"remove", "--config", "/nonexistent/orchard.conf"})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "reading config") {
			t.Errorf("Execute() error = %v, want config loading error", err)
		}
	})
}

func TestPickWorktrees(t *testing.T) {
	planted := []plantedWorktree{
		{Name: "wt1", Path: "/path/to/plant/wt1"},
		{Name: "wt2", Path: "/path/to/plant/wt2"},
		{Name: "wt3", Path: "/path/to/plant/wt3"},
	}

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "toggle one and confirm",
			input: "2\n\n",
			want:  []string{"wt2"},
		},
		{
			name:  "toggle several on one line",
			input: "1 3\n\n",
			want:  []string{"wt1", "wt3"},
		},
		{
			name:  "toggle twice deselects",
			input: "2\n2\n\n",
			want:  nil,
		},
		{
			name:  "toggle all",
			input: "a\n\n",
			want:  []string{"wt1", "wt2", "wt3"},
		},
		{
			name:  "toggle all twice deselects all",
			input: "a\na\n\n",
			want:  nil,
		},
		{
			name:  "toggle all completes a partial selection",
			input: "1\na\n\n",
			want:  []string{"wt1", "wt2", "wt3"},
		},
		{
			name:  "invalid line leaves selection unchanged",
			input: "1 bogus\n2\n\n",
			want:  []string{"wt2"},
		},
		{
			name:  "out of range rejected",
			input: "4\n0\n3\n\n",
			want:  []string{"wt3"},
		},
		{
			name:  "quit aborts",
			input: "1\nq\n",
			want:  nil,
		},
		{
			name:  "EOF aborts",
			input: "1\n",
			want:  nil,
		},
		{
			name:  "confirm with nothing selected",
			input: "\n",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := bufio.NewScanner(strings.NewReader(tt.input))
			got, err := pickWorktrees(scanner, io.Discard, planted)
			if err != nil {
				t.Fatalf("pickWorktrees() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("pickWorktrees() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfirmRemoval(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "yes short", input: "y\n", want: true},
		{name: "yes long", input: "YES\n", want: true},
		{name: "no", input: "n\n", want: false},
		{name: "empty defaults to no", input: "\n", want: false},
		{name: "EOF declines", input: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := bufio.NewScanner(strings.NewReader(tt.input))
			got, err := confirmRemoval(scanner, io.Discard, []string{"wt1"})
			if err != nil {
				t.Fatalf("confirmRemoval() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("confirmRemoval() = %v, want %v", got, tt.want)
			}
		})
	}
}
