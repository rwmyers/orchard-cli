// Package hg is a skeleton driver, for copying rather than for using.
//
// It sketches Mercurial through `hg share`, and exists to show the shape of a
// driver that lives outside orchard's repository. It has NOT been run against
// real Mercurial: treat every command in it as a guess to be checked, and the
// structure — the interfaces implemented and, more importantly, the ones left
// out — as the part worth copying.
package hg

import (
	"path/filepath"
	"strings"

	"github.com/rwmyers/orchard-cli/vcs"
)

func init() { vcs.Register(Driver{}) }

type Driver struct{}

// Mercurial has named branches, but `hg share` working copies are not tied to
// one the way git worktrees are, so vcs.Brancher is left out — which is the
// decision every driver author has to make first. vcs.Ignorer and
// vcs.Inspector are left out here only because this is a skeleton.
var (
	_ vcs.Driver       = Driver{}
	_ vcs.Updater      = Driver{}
	_ vcs.BaseResolver = Driver{}
)

func (Driver) Name() string { return "hg" }

func (Driver) Detect(dir string) (string, error) {
	out, err := vcs.Output(dir, "hg", "root")
	if err != nil {
		return "", vcs.ErrNotRepository
	}
	return filepath.Clean(strings.TrimSpace(string(out))), nil
}

// ListWorktrees is the method with no natural Mercurial equivalent: `hg share`
// does not record its shares anywhere, so a real driver would have to keep its
// own record. That is the sort of gap to find before writing the rest.
func (Driver) ListWorktrees(root string) ([]vcs.Worktree, error) {
	return nil, nil
}

func (Driver) AddWorktree(req vcs.AddRequest) error {
	args := []string{"share", req.Root, req.Path}
	if req.Base != "" {
		args = append(args, "--updaterev", req.Base)
	}
	return vcs.Run(req.Root, "hg", args...)
}

func (Driver) RemoveWorktree(req vcs.RemoveRequest) error {
	return vcs.Run(req.Root, "rm", "-rf", req.Path)
}

func (Driver) UpdateRoot(root string) error {
	return vcs.Run(root, "hg", "pull", "--update")
}

func (Driver) BaseExists(root, base string) (bool, error) {
	return vcs.Succeeds(root, "hg", "log", "-r", base, "--template", "x")
}
