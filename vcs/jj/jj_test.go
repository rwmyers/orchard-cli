package jj

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rwmyers/orchard-cli/vcs"
)

func TestParseWorkspaces(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []vcs.Worktree
	}{
		{
			name:  "empty output",
			input: "",
			want:  nil,
		},
		{
			name:  "the default workspace alone",
			input: "default\t/home/me/src/project\n",
			want:  []vcs.Worktree{{Name: "default", Path: "/home/me/src/project"}},
		},
		{
			name:  "several workspaces",
			input: "default\t/home/me/src/project\nfeat-a\t/home/me/src/plants/feat-a\n",
			want: []vcs.Worktree{
				{Name: "default", Path: "/home/me/src/project"},
				{Name: "feat-a", Path: "/home/me/src/plants/feat-a"},
			},
		},
		{
			name: "a workspace with no resolvable root is skipped",
			// root is an optional in jj's template language, and renders
			// empty when it cannot be resolved. Such a workspace is not one
			// orchard could act on.
			input: "default\t/home/me/src/project\nstale\t\n",
			want:  []vcs.Worktree{{Name: "default", Path: "/home/me/src/project"}},
		},
		{
			name:  "paths are cleaned",
			input: "feat-a\t/home/me/src/plants/../plants/feat-a\n",
			want:  []vcs.Worktree{{Name: "feat-a", Path: "/home/me/src/plants/feat-a"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseWorkspaces([]byte(tt.input)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseWorkspaces() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDriverOptsOutOfBranches(t *testing.T) {
	// The whole reason jj shaped orchard's capability gating. If this ever
	// starts reporting true, orchard has begun demanding a branch per
	// workspace that jj does not have.
	caps := vcs.CapabilitiesOf(Driver{})
	if caps.Branches {
		t.Error("the jj driver reports Branches; jj workspaces are not tied to bookmarks")
	}
	if caps.Ignores {
		t.Error("the jj driver reports Ignores; jj has no cheap check-ignore equivalent")
	}
	if !caps.Update || !caps.BaseRef || !caps.Inspect {
		t.Errorf("CapabilitiesOf() = %+v, want update, base and inspect", caps)
	}
}

// jjRepo builds a jj repository with a git remote and one published commit,
// and returns the repository and plant directory paths. It skips the test when
// jj is not installed, the way the git-backed tests skip without git.
func jjRepo(t *testing.T) (repo, plants string) {
	t.Helper()
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj is not available")
	}

	dir := t.TempDir()
	// jj refuses to commit without an identity, and must not pick up the
	// developer's real configuration.
	config := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(config, []byte("[user]\nname = \"Test\"\nemail = \"test@example.com\"\n"), 0o644); err != nil {
		t.Fatalf("writing jj config: %v", err)
	}
	t.Setenv("JJ_CONFIG", config)

	run := func(dir, name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v: %s", name, args, err, out)
		}
	}

	origin := filepath.Join(dir, "origin.git")
	run(dir, "git", "init", "--quiet", "--bare", origin)
	run(dir, "jj", "git", "clone", "--quiet", origin, "repo")

	repo = filepath.Join(dir, "repo")
	plants = filepath.Join(dir, "plants")
	if err := os.MkdirAll(plants, 0o755); err != nil {
		t.Fatalf("creating plant dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}
	run(repo, "jj", "commit", "-m", "one")
	run(repo, "jj", "bookmark", "create", "main", "-r", "@-")
	run(repo, "jj", "git", "push", "-b", "main")

	// The driver's own output would otherwise land in the test log.
	vcs.SetOutput(io.Discard, io.Discard)
	t.Cleanup(func() { vcs.SetOutput(os.Stdout, os.Stderr) })

	return repo, plants
}

func TestAgainstRealJJ(t *testing.T) {
	repo, plants := jjRepo(t)
	driver := Driver{}

	t.Run("detects the repository from a subdirectory", func(t *testing.T) {
		sub := filepath.Join(repo, "sub")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("creating subdirectory: %v", err)
		}
		root, err := driver.Detect(sub)
		if err != nil {
			t.Fatalf("Detect() error = %v", err)
		}
		// The repository is what should be configured, not the subdirectory.
		if resolved, _ := filepath.EvalSymlinks(repo); root != resolved && root != repo {
			t.Errorf("Detect() = %q, want %q", root, repo)
		}
	})

	t.Run("declines a directory that is not a jj repository", func(t *testing.T) {
		if _, err := driver.Detect(t.TempDir()); err != vcs.ErrNotRepository {
			t.Errorf("Detect() error = %v, want ErrNotRepository", err)
		}
	})

	t.Run("a published base resolves and a nonsense one does not", func(t *testing.T) {
		ok, err := driver.BaseExists(repo, "main")
		if err != nil || !ok {
			t.Errorf("BaseExists(main) = %v, %v, want true", ok, err)
		}
		// An unknown revset makes jj exit non-zero rather than return
		// nothing, so this exercises the other branch of BaseExists.
		if ok, _ := driver.BaseExists(repo, "nosuchrevision"); ok {
			t.Error("BaseExists(nosuchrevision) = true, want false")
		}
	})

	path := filepath.Join(plants, "feat-a")

	t.Run("adds a workspace", func(t *testing.T) {
		err := driver.AddWorktree(vcs.AddRequest{
			Root: repo, Name: "feat-a", Path: path, Base: "main",
		})
		if err != nil {
			t.Fatalf("AddWorktree() error = %v", err)
		}

		worktrees, err := driver.ListWorktrees(repo)
		if err != nil {
			t.Fatalf("ListWorktrees() error = %v", err)
		}
		var found bool
		for _, wt := range worktrees {
			if wt.Name == "feat-a" {
				found = true
			}
		}
		if !found {
			t.Errorf("ListWorktrees() = %+v, want it to include feat-a", worktrees)
		}
	})

	t.Run("a fresh workspace on a published base holds nothing", func(t *testing.T) {
		state, err := driver.Inspect(repo, vcs.Worktree{Name: "feat-a", Path: path})
		if err != nil {
			t.Fatalf("Inspect() error = %v", err)
		}
		if !state.Safe() {
			t.Errorf("Inspect() = %+v, want it safe to remove", state)
		}
	})

	t.Run("a workspace with changes holds work", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(path, "wip.txt"), []byte("wip\n"), 0o644); err != nil {
			t.Fatalf("writing file: %v", err)
		}
		state, err := driver.Inspect(repo, vcs.Worktree{Name: "feat-a", Path: path})
		if err != nil {
			t.Fatalf("Inspect() error = %v", err)
		}
		if !state.Dirty {
			t.Errorf("Inspect() = %+v, want Dirty", state)
		}
		if state.Safe() {
			t.Error("Inspect() reported a workspace with changes as safe to remove")
		}
	})

	t.Run("published working-copy changes do not count as work", func(t *testing.T) {
		// jj's working copy is itself a commit, and stays non-empty after its
		// content is pushed. Asking only whether @ is non-empty would call
		// every workspace that had ever been touched dirty forever, which
		// would make `orchard remove --check` a standing refusal.
		run := func(name string, args ...string) {
			t.Helper()
			cmd := exec.Command(name, args...)
			cmd.Dir = path
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s %v: %v: %s", name, args, err, out)
			}
		}
		run("jj", "describe", "-m", "feat-a work")
		run("jj", "bookmark", "create", "feat-a", "-r", "@")
		run("jj", "git", "push", "-b", "feat-a")

		state, err := driver.Inspect(repo, vcs.Worktree{Name: "feat-a", Path: path})
		if err != nil {
			t.Fatalf("Inspect() error = %v", err)
		}
		if !state.Safe() {
			t.Errorf("Inspect() = %+v, want a pushed workspace to be safe to remove", state)
		}
	})

	t.Run("removing takes the directory with it", func(t *testing.T) {
		// jj workspace forget leaves the directory behind by design, so this
		// is the driver's own cleanup being checked, not jj's.
		err := driver.RemoveWorktree(vcs.RemoveRequest{Root: repo, Name: "feat-a", Path: path})
		if err != nil {
			t.Fatalf("RemoveWorktree() error = %v", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("the workspace directory is still at %s", path)
		}

		worktrees, _ := driver.ListWorktrees(repo)
		for _, wt := range worktrees {
			if wt.Name == "feat-a" {
				t.Errorf("ListWorktrees() still reports feat-a")
			}
		}
	})
}

func TestUpdateRootWithoutRemotes(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj is not available")
	}

	dir := t.TempDir()
	config := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(config, []byte("[user]\nname = \"Test\"\nemail = \"t@example.com\"\n"), 0o644); err != nil {
		t.Fatalf("writing jj config: %v", err)
	}
	t.Setenv("JJ_CONFIG", config)

	repo := filepath.Join(dir, "repo")
	cmd := exec.Command("jj", "git", "init", repo)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("jj git init: %v: %s", err, out)
	}

	vcs.SetOutput(io.Discard, io.Discard)
	t.Cleanup(func() { vcs.SetOutput(os.Stdout, os.Stderr) })

	// `jj git fetch` treats having no remote as an error. A repository made by
	// `jj git init` has none, and every `orchard add` in one would fail if the
	// driver passed that through.
	if err := (Driver{}).UpdateRoot(repo); err != nil {
		t.Errorf("UpdateRoot() error = %v, want a repository with no remotes to be left alone", err)
	}
}
