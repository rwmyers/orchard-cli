// Package git implements orchard's built-in driver for git. Importing it
// registers the driver, which is why orchard's own main imports it for effect
// only; a binary that leaves it out gets an orchard that does not know about
// git.
//
// It also stands as the worked example for a driver of your own: it implements
// every optional interface in the vcs package, so there is a demonstration here
// of each part of orchard's model a driver can opt into.
package git

import (
	"bufio"
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rwmyers/orchard-cli/vcs"
)

func init() { vcs.Register(Driver{}) }

// Driver manages git worktrees by running the git command line tool. It holds
// no state, so the zero value is ready to use.
type Driver struct{}

// Compile-time proof that git opts into every optional part of orchard's
// model. A driver of your own only needs the ones its system can honour.
var (
	_ vcs.Driver       = Driver{}
	_ vcs.Brancher     = Driver{}
	_ vcs.Updater      = Driver{}
	_ vcs.BaseResolver = Driver{}
	_ vcs.Ignorer      = Driver{}
	_ vcs.Inspector    = Driver{}
)

func (Driver) Name() string { return "git" }

// Detect returns the top level of the working tree containing dir, so that
// running orchard from a subdirectory configures the repository rather than the
// subdirectory.
func (d Driver) Detect(dir string) (string, error) {
	out, err := vcs.Output(dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		// A bare repository has no top level, and so fails here exactly as a
		// directory outside any repository does. Saying which it is saves the
		// user an error that looks plainly wrong to them.
		if d.isBare(dir) {
			return "", fmt.Errorf("%s is a bare repository; orchard needs a repository with a worktree", dir)
		}
		return "", vcs.ErrNotRepository
	}
	return filepath.Clean(strings.TrimSpace(string(out))), nil
}

// isBare reports whether dir is inside a repository that has no worktree.
func (Driver) isBare(dir string) bool {
	out, err := vcs.Output(dir, "git", "rev-parse", "--is-bare-repository")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func (Driver) ListWorktrees(root string) ([]vcs.Worktree, error) {
	out, err := vcs.Output(root, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktrees(out)
}

// parseWorktrees pulls the paths out of `git worktree list --porcelain`. The
// other fields of each record — HEAD, branch, prunable — are not part of what
// orchard needs to know, and are skipped.
func parseWorktrees(output []byte) ([]vcs.Worktree, error) {
	var worktrees []vcs.Worktree
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		path := filepath.Clean(strings.TrimPrefix(line, "worktree "))
		worktrees = append(worktrees, vcs.Worktree{Name: filepath.Base(path), Path: path})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return worktrees, nil
}

// AddWorktree creates the worktree on a new branch named after it, so that a
// shared base can be used for a whole batch and `orchard remove` finds a branch
// matching the worktree name.
func (Driver) AddWorktree(req vcs.AddRequest) error {
	args := []string{"worktree", "add", "-b", req.Name, req.Path}
	if req.Base != "" {
		args = append(args, req.Base)
	}
	if err := vcs.Run(req.Root, "git", args...); err != nil {
		return err
	}
	return vcs.Run(req.Path, "git", "submodule", "update", "--init", "--recursive")
}

// RemoveWorktree removes the worktree and its directory. --force is needed
// because a worktree being cleared up has usually been worked in.
func (Driver) RemoveWorktree(req vcs.RemoveRequest) error {
	return vcs.Run(req.Root, "git", "worktree", "remove", "--force", req.Path)
}

func (Driver) BranchExists(root, name string) (bool, error) {
	return vcs.Succeeds(root, "git", "show-ref", "--verify", "refs/heads/"+name)
}

func (Driver) DeleteBranch(root, name string) error {
	return vcs.Run(root, "git", "branch", "-D", name)
}

// UpdateRoot brings the root tree up to date, submodules included, so that
// worktrees created from it start from current work.
func (Driver) UpdateRoot(root string) error {
	if err := vcs.Run(root, "git", "pull"); err != nil {
		return err
	}
	return vcs.Run(root, "git", "submodule", "update", "--init", "--recursive")
}

// BaseExists reports whether base resolves to a commit. Asking for a commit
// specifically means a tag or a remote-tracking ref is accepted, while
// something that exists but cannot be started from is not.
func (Driver) BaseExists(root, base string) (bool, error) {
	return vcs.Succeeds(root, "git", "rev-parse", "--verify", "--quiet", base+"^{commit}")
}

func (Driver) Ignores(root, path string) (bool, error) {
	return vcs.Succeeds(root, "git", "check-ignore", "--quiet", "--", path)
}

// Inspect reports what removing the worktree would destroy. Both questions are
// answered from inside the worktree, so a detached HEAD or a branch renamed
// underneath orchard is still described accurately.
//
// Anything that cannot be determined is reported as unsafe. `orchard remove`
// without --check is unaffected either way; this only ever decides whether
// orchard removes something on its own initiative, and there the conservative
// answer is the right one.
func (Driver) Inspect(_ string, wt vcs.Worktree) (vcs.WorktreeState, error) {
	unsafe := vcs.WorktreeState{Dirty: true, Unpublished: true}

	// --porcelain covers staged, unstaged and untracked alike; any output at
	// all means there is something here that exists nowhere else.
	status, err := vcs.Output(wt.Path, "git", "status", "--porcelain")
	if err != nil {
		return unsafe, fmt.Errorf("checking for uncommitted changes in %s: %w", wt.Path, err)
	}

	// Commits reachable from HEAD but from no remote-tracking branch. This
	// deliberately asks about remotes rather than about the root tree's
	// branch: work that has been pushed survives the worktree going away,
	// and work that has only been merged locally does not.
	unpushed, err := vcs.Output(wt.Path, "git", "log", "--oneline", "HEAD", "--not", "--remotes")
	if err != nil {
		return unsafe, fmt.Errorf("checking for unpushed commits in %s: %w", wt.Path, err)
	}

	return vcs.WorktreeState{
		Dirty:       len(bytes.TrimSpace(status)) > 0,
		Unpublished: len(bytes.TrimSpace(unpushed)) > 0,
	}, nil
}
