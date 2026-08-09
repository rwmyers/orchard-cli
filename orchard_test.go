package main

import (
	"fmt"
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

func TestDuplicateName(t *testing.T) {
	tests := []struct {
		name  string
		names []string
		want  string
	}{
		{name: "no names", names: nil, want: ""},
		{name: "unique names", names: []string{"wt1", "wt2", "wt3"}, want: ""},
		{name: "adjacent duplicate", names: []string{"wt1", "wt1"}, want: "wt1"},
		{name: "separated duplicate", names: []string{"wt1", "wt2", "wt1"}, want: "wt1"},
		{name: "first duplicate reported", names: []string{"wt2", "wt2", "wt3", "wt3"}, want: "wt2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := duplicateName(tt.names); got != tt.want {
				t.Errorf("duplicateName(%v) = %q, want %q", tt.names, got, tt.want)
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
		{name: "add with unknown flag", args: []string{"add", "wt1", "--bogus"}, wantErr: true},
		{name: "add base flag needs a value", args: []string{"add", "wt1", "--base"}, wantErr: true},
		{name: "root with args", args: []string{"root", "extra"}, wantErr: true},
		{name: "list with args", args: []string{"list", "extra"}, wantErr: true},
		{name: "setup with too many args", args: []string{"setup", "one", "two"}, wantErr: true},
		{name: "setup with an unknown flag", args: []string{"setup", "--bogus"}, wantErr: true},
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

	t.Run("add accepts multiple names", func(t *testing.T) {
		// A nonexistent config makes RunE fail at config loading, proving
		// multiple worktree names get past arg validation.
		cmd := newRootCmd()
		cmd.SetArgs([]string{"add", "wt1", "wt2", "wt3", "--config", "/nonexistent/orchard.conf"})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "reading config") {
			t.Errorf("Execute() error = %v, want config loading error", err)
		}
	})

	t.Run("add accepts a base flag", func(t *testing.T) {
		cmd := newRootCmd()
		cmd.SetArgs([]string{"add", "wt1", "wt2", "--base", "main", "--config", "/nonexistent/orchard.conf"})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "reading config") {
			t.Errorf("Execute() error = %v, want config loading error", err)
		}
	})

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

	t.Run("setup takes a root tree and flags", func(t *testing.T) {
		// A nonexistent root tree fails before any prompt, proving the
		// positional argument and the flags reach runSetup.
		cmd := newRootCmd()
		cmd.SetArgs([]string{"setup", "/nonexistent/repo", "--plant-dir", "/nonexistent/plant", "--force"})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "/nonexistent/repo") {
			t.Errorf("Execute() error = %v, want an error naming the root tree", err)
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

// accessiblePrompter returns a prompter wired to canned input and running in
// huh's accessible (non-terminal) mode, which is the code path taken whenever
// stdin is not a TTY.
func accessiblePrompter(input string) huhPrompter {
	return huhPrompter{in: strings.NewReader(input), out: io.Discard, terminal: false}
}

func TestHuhPrompterPickWorktrees(t *testing.T) {
	planted := []plantedWorktree{
		{Name: "wt1", Path: "/path/to/plant/wt1"},
		{Name: "wt2", Path: "/path/to/plant/wt2"},
		{Name: "wt3", Path: "/path/to/plant/wt3"},
	}

	// In accessible mode each line toggles a single entry by number and 0
	// confirms the selection.
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "toggle one and confirm",
			input: "2\n0\n",
			want:  []string{"wt2"},
		},
		{
			name:  "toggle several",
			input: "1\n3\n0\n",
			want:  []string{"wt1", "wt3"},
		},
		{
			name:  "toggle twice deselects",
			input: "2\n2\n0\n",
			want:  nil,
		},
		{
			name:  "selection is returned in list order",
			input: "3\n1\n0\n",
			want:  []string{"wt1", "wt3"},
		},
		{
			name:  "invalid input reprompts",
			input: "bogus\n2\n0\n",
			want:  []string{"wt2"},
		},
		{
			name:  "out of range reprompts",
			input: "4\n3\n0\n",
			want:  []string{"wt3"},
		},
		{
			name:  "confirm with nothing selected",
			input: "0\n",
			want:  nil,
		},
		{
			name:  "EOF confirms what is selected",
			input: "1\n",
			want:  []string{"wt1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := accessiblePrompter(tt.input).PickWorktrees(planted)
			if err != nil {
				t.Fatalf("PickWorktrees() error = %v", err)
			}
			// An empty selection may come back as nil or an empty slice;
			// callers only look at the length, so treat the two alike.
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("PickWorktrees() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfirmDescription(t *testing.T) {
	t.Run("lists every name when short", func(t *testing.T) {
		got := confirmDescription([]string{"wt1", "wt2"})
		if got != "wt1\nwt2" {
			t.Errorf("confirmDescription() = %q, want %q", got, "wt1\nwt2")
		}
	})

	t.Run("summarises the tail when long", func(t *testing.T) {
		names := make([]string, maxConfirmNames+3)
		for i := range names {
			names[i] = fmt.Sprintf("wt%d", i)
		}
		got := confirmDescription(names)
		lines := strings.Split(got, "\n")
		if len(lines) != maxConfirmNames+1 {
			t.Fatalf("confirmDescription() produced %d lines, want %d", len(lines), maxConfirmNames+1)
		}
		if want := "... and 3 more"; lines[len(lines)-1] != want {
			t.Errorf("last line = %q, want %q", lines[len(lines)-1], want)
		}
	})

	t.Run("does not modify the caller's slice", func(t *testing.T) {
		names := make([]string, maxConfirmNames+1)
		for i := range names {
			names[i] = fmt.Sprintf("wt%d", i)
		}
		confirmDescription(names)
		if want := fmt.Sprintf("wt%d", maxConfirmNames); names[maxConfirmNames] != want {
			t.Errorf("names[%d] = %q, want %q", maxConfirmNames, names[maxConfirmNames], want)
		}
	})
}

func TestHuhPrompterConfirmRemoval(t *testing.T) {
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
			got, err := accessiblePrompter(tt.input).ConfirmRemoval([]string{"wt1"})
			if err != nil {
				t.Fatalf("ConfirmRemoval() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ConfirmRemoval() = %v, want %v", got, tt.want)
			}
		})
	}
}
