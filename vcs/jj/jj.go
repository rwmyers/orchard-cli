// Package jj implements orchard's built-in driver for Jujutsu. Importing it
// registers the driver.
//
// jj is the reason orchard's model is capability-gated rather than
// git-shaped. Its workspaces differ from git worktrees in ways that would each
// otherwise have needed a special case somewhere in orchard:
//
//   - A workspace is not tied to a bookmark the way a git worktree is tied to a
//     branch. `jj workspace add` creates no named ref at all. So this driver
//     does not implement [vcs.Brancher], and orchard stops checking a name is
//     free before planting, stops insisting one is present before removing, and
//     stops deleting anything afterwards.
//   - `jj workspace forget` unlinks a workspace and deliberately leaves its
//     directory on disk, so RemoveWorktree deletes it. Orchard's contract is
//     that nothing is left at the path; how a driver gets there is its own
//     business.
//   - jj has no cheap equivalent of `git check-ignore`, so [vcs.Ignorer] is
//     left out and `orchard setup` simply skips the hint it would have printed.
//
// Everything here was verified against jj 0.44.0. jj's command line is still
// moving, and the two things most likely to need revisiting for another release
// are listTemplate and the revsets in Inspect.
package jj

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rwmyers/orchard-cli/vcs"
)

func init() { vcs.Register(Driver{}) }

// Driver manages jj workspaces by running the jj command line tool. It holds no
// state, so the zero value is ready to use.
type Driver struct{}

// jj workspaces are independent of bookmarks and jj has no cheap ignore check,
// so [vcs.Brancher] and [vcs.Ignorer] are absent on purpose.
var (
	_ vcs.Driver       = Driver{}
	_ vcs.Updater      = Driver{}
	_ vcs.BaseResolver = Driver{}
	_ vcs.Inspector    = Driver{}
)

func (Driver) Name() string { return "jj" }

// readArgs prefix every command whose output is parsed, so that a user's colour
// and pager settings cannot corrupt it.
var readArgs = []string{"--no-pager", "--color=never"}

func read(dir string, args ...string) ([]byte, error) {
	return vcs.Output(dir, "jj", append(append([]string{}, readArgs...), args...)...)
}

// Detect returns the root of the workspace containing dir.
//
// A repository colocated with git is claimed by this driver and the git one
// alike. Orchard settles that by taking the first in alphabetical order, which
// is git; put `vcs = jj` in orchard.conf to drive it with jj instead.
// `orchard setup` records whichever driver it detected, so the choice is made
// once.
func (Driver) Detect(dir string) (string, error) {
	out, err := read(dir, "workspace", "root")
	if err != nil {
		return "", vcs.ErrNotRepository
	}
	return filepath.Clean(strings.TrimSpace(string(out))), nil
}

// listTemplate renders one workspace per line as name, tab, root path. The
// keywords are bare rather than called — `name`, not `name()` — which is what
// the WorkspaceRef template type accepts.
const listTemplate = `name ++ "\t" ++ root ++ "\n"`

func (Driver) ListWorktrees(root string) ([]vcs.Worktree, error) {
	out, err := read(root, "workspace", "list", "-T", listTemplate)
	if err != nil {
		return nil, err
	}
	return parseWorkspaces(out), nil
}

// parseWorkspaces reads the lines listTemplate produces. A workspace whose root
// jj cannot resolve renders an empty path and is skipped, since it is not one
// orchard could act on.
func parseWorkspaces(out []byte) []vcs.Worktree {
	var worktrees []vcs.Worktree
	for _, line := range strings.Split(string(out), "\n") {
		name, path, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		name, path = strings.TrimSpace(name), strings.TrimSpace(path)
		if name == "" || path == "" {
			continue
		}
		worktrees = append(worktrees, vcs.Worktree{Name: name, Path: filepath.Clean(path)})
	}
	return worktrees
}

// AddWorktree creates a workspace at req.Path. --name is passed explicitly even
// though jj defaults it to the basename of the destination, so that the name
// orchard knows the worktree by stays the name jj knows it by whatever jj's
// default becomes.
func (Driver) AddWorktree(req vcs.AddRequest) error {
	args := []string{"workspace", "add", "--name", req.Name}
	if req.Base != "" {
		args = append(args, "--revision", req.Base)
	}
	return vcs.Run(req.Root, "jj", append(args, req.Path)...)
}

// RemoveWorktree unlinks the workspace and then deletes its directory, which
// `jj workspace forget` leaves behind by design.
func (Driver) RemoveWorktree(req vcs.RemoveRequest) error {
	if err := vcs.Run(req.Root, "jj", "workspace", "forget", req.Name); err != nil {
		return err
	}
	return os.RemoveAll(req.Path)
}

// UpdateRoot fetches from the git remotes, if there are any. A jj repository
// without one is perfectly normal — `jj git init` makes them — and `jj git
// fetch` treats having nothing to fetch from as an error, which would fail
// every `orchard add` in such a repository. Having nothing to fetch is not a
// failure to bring the root tree up to date.
func (Driver) UpdateRoot(root string) error {
	remotes, err := read(root, "git", "remote", "list")
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(remotes)) == 0 {
		fmt.Println("No git remotes; nothing to fetch.")
		return nil
	}
	return vcs.Run(root, "jj", "git", "fetch")
}

// BaseExists reports whether base is a revset resolving to at least one
// revision. jj tells the two failure modes apart by exit status: an
// unparseable or unknown revset exits non-zero, while one that is merely empty
// exits zero having printed nothing, so the output is what decides.
func (Driver) BaseExists(root, base string) (bool, error) {
	out, err := read(root, "log", "--no-graph", "-n", "1", "-r", base, "-T", `"ok"`)
	if err != nil {
		return false, nil
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

// Revsets for Inspect. Both are evaluated from inside the workspace, where @ is
// that workspace's working-copy commit.
const (
	// dirtyRevset matches the working-copy commit when it holds changes that
	// exist nowhere else.
	//
	// jj has no uncommitted state as git understands it — the working copy is
	// itself a commit, and that commit can be published while still being
	// worked in. Asking only whether @ is non-empty would therefore call a
	// workspace dirty for as long as it has ever been touched, even once its
	// content is on a remote, which would make `orchard remove --check` a
	// standing refusal rather than a useful check. So the question is whether
	// @ is non-empty *and* unreachable from any remote bookmark.
	dirtyRevset = `@ ~ empty() ~ ::remote_bookmarks()`
	// unpublishedRevset matches commits leading to the working copy that no
	// remote bookmark reaches. In a repository with no remotes nothing is
	// reachable, so everything counts as unpublished — which is correct, since
	// there is genuinely nowhere else for the work to be.
	unpublishedRevset = `::@ ~ ::remote_bookmarks() ~ empty()`
)

// Inspect reports what removing the workspace would destroy. Anything that
// cannot be determined is reported as unsafe, since this is the answer that
// decides whether orchard removes a workspace on its own initiative.
func (Driver) Inspect(_ string, wt vcs.Worktree) (vcs.WorktreeState, error) {
	unsafe := vcs.WorktreeState{Dirty: true, Unpublished: true}

	dirty, err := matches(wt.Path, dirtyRevset)
	if err != nil {
		return unsafe, fmt.Errorf("checking for changes in %s: %w", wt.Path, err)
	}
	unpublished, err := matches(wt.Path, unpublishedRevset)
	if err != nil {
		return unsafe, fmt.Errorf("checking for unpublished commits in %s: %w", wt.Path, err)
	}
	return vcs.WorktreeState{Dirty: dirty, Unpublished: unpublished}, nil
}

// matches reports whether a revset selects anything, evaluated in dir.
func matches(dir, revset string) (bool, error) {
	out, err := read(dir, "log", "--no-graph", "-r", revset, "-T", `"X"`)
	if err != nil {
		return false, err
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}
