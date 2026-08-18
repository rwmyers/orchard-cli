package cli

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rwmyers/orchard-cli/vcs"
)

// plainDriver stands for a system with none of orchard's optional model — no
// branches tied to worktrees, no remote, no base to start from. jj is close to
// this. It records what orchard asked of it so the tests can check orchard
// asked for nothing more.
type plainDriver struct {
	worktrees []vcs.Worktree
	added     []vcs.AddRequest
	removed   []vcs.RemoveRequest
}

func (*plainDriver) Name() string { return "plain" }

func (*plainDriver) Detect(dir string) (string, error) { return dir, nil }

func (d *plainDriver) ListWorktrees(string) ([]vcs.Worktree, error) { return d.worktrees, nil }

func (d *plainDriver) AddWorktree(req vcs.AddRequest) error {
	d.added = append(d.added, req)
	d.worktrees = append(d.worktrees, vcs.Worktree{Name: req.Name, Path: req.Path})
	return nil
}

func (d *plainDriver) RemoveWorktree(req vcs.RemoveRequest) error {
	d.removed = append(d.removed, req)
	return nil
}

// branchingDriver adds git's branch-per-worktree model on top.
type branchingDriver struct {
	plainDriver
	branches map[string]bool
	deleted  []string
}

func newBranchingDriver() *branchingDriver {
	return &branchingDriver{branches: map[string]bool{}}
}

func (*branchingDriver) Name() string { return "branching" }

func (d *branchingDriver) BranchExists(_, name string) (bool, error) { return d.branches[name], nil }

func (d *branchingDriver) DeleteBranch(_, name string) error {
	d.deleted = append(d.deleted, name)
	delete(d.branches, name)
	return nil
}

func (d *branchingDriver) AddWorktree(req vcs.AddRequest) error {
	d.branches[req.Name] = true
	return d.plainDriver.AddWorktree(req)
}

// testOrchard wires a driver up to a configuration pointing at paths that are
// never touched, since none of these drivers goes near the filesystem.
func testOrchard(driver vcs.Driver) *orchard {
	return &orchard{
		Config: &Config{RootTree: "/repo", PlantDir: "/plants", VCS: driver.Name()},
		driver: driver,
		caps:   vcs.CapabilitiesOf(driver),
	}
}

func TestRunAddRejectsBaseADriverCannotHonour(t *testing.T) {
	// Silently ignoring --base would plant the batch on the wrong commit
	// without saying so, which is worse than refusing.
	driver := &plainDriver{}
	err := runAdd(testOrchard(driver), []string{"wt1"}, "main")
	if err == nil || !strings.Contains(err.Error(), "--base") {
		t.Fatalf("runAdd() error = %v, want it to refuse --base", err)
	}
	if len(driver.added) != 0 {
		t.Errorf("runAdd() created %d worktree(s) after refusing the base", len(driver.added))
	}
}

func TestRunAddSkipsBranchChecksWithoutABrancher(t *testing.T) {
	driver := &plainDriver{}
	if err := runAdd(testOrchard(driver), []string{"wt1", "wt2"}, ""); err != nil {
		t.Fatalf("runAdd() error = %v", err)
	}

	want := []vcs.AddRequest{
		{Root: "/repo", Name: "wt1", Path: filepath.Join("/plants", "wt1")},
		{Root: "/repo", Name: "wt2", Path: filepath.Join("/plants", "wt2")},
	}
	if !reflect.DeepEqual(driver.added, want) {
		t.Errorf("runAdd() asked for %+v, want %+v", driver.added, want)
	}
}

func TestRunAddRefusesATakenBranch(t *testing.T) {
	driver := newBranchingDriver()
	driver.branches["wt1"] = true

	err := runAdd(testOrchard(driver), []string{"wt1"}, "")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("runAdd() error = %v, want it to refuse the taken branch", err)
	}
	if len(driver.added) != 0 {
		t.Errorf("runAdd() created a worktree despite the taken branch")
	}
}

func TestRunRemoveWithoutABrancher(t *testing.T) {
	driver := &plainDriver{}
	if err := runAdd(testOrchard(driver), []string{"wt1"}, ""); err != nil {
		t.Fatalf("runAdd() error = %v", err)
	}

	// Removal must not insist on a branch that this system never had.
	if err := runRemove(testOrchard(driver), []string{"wt1"}, false); err != nil {
		t.Fatalf("runRemove() error = %v", err)
	}
	want := []vcs.RemoveRequest{{Root: "/repo", Name: "wt1", Path: filepath.Join("/plants", "wt1")}}
	if !reflect.DeepEqual(driver.removed, want) {
		t.Errorf("runRemove() asked for %+v, want %+v", driver.removed, want)
	}
}

func TestRunRemoveDeletesTheBranchWithABrancher(t *testing.T) {
	driver := newBranchingDriver()
	if err := runAdd(testOrchard(driver), []string{"wt1"}, ""); err != nil {
		t.Fatalf("runAdd() error = %v", err)
	}
	if err := runRemove(testOrchard(driver), []string{"wt1"}, false); err != nil {
		t.Fatalf("runRemove() error = %v", err)
	}
	if want := []string{"wt1"}; !reflect.DeepEqual(driver.deleted, want) {
		t.Errorf("runRemove() deleted %v, want %v", driver.deleted, want)
	}
}

func TestConfirmRemovalWordingFollowsTheDriver(t *testing.T) {
	// The confirmation must not promise to delete branches a system does not
	// have.
	if got := removalSubject(true); got != "worktree(s) and their branches" {
		t.Errorf("removalSubject(true) = %q", got)
	}
	if got := removalSubject(false); got != "worktree(s)" {
		t.Errorf("removalSubject(false) = %q", got)
	}
}

func TestResolveDriver(t *testing.T) {
	t.Run("an unknown name is reported", func(t *testing.T) {
		_, err := resolveDriver(&Config{RootTree: "/repo", PlantDir: "/plants", VCS: "nosuchvcs"})
		if err == nil || !strings.Contains(err.Error(), "nosuchvcs") {
			t.Errorf("resolveDriver() error = %v, want it to name the missing driver", err)
		}
	})

	t.Run("a configured name is used without detection", func(t *testing.T) {
		// git is registered by this package's tests; naming it must not
		// require the root tree to exist.
		driver, err := resolveDriver(&Config{RootTree: "/nonexistent", PlantDir: "/plants", VCS: "git"})
		if err != nil {
			t.Fatalf("resolveDriver() error = %v", err)
		}
		if driver.Name() != "git" {
			t.Errorf("resolveDriver() = %q, want %q", driver.Name(), "git")
		}
	})
}

// inspectingDriver knows whether its worktrees still hold work, the way a
// driver backing `orchard remove --check` must.
type inspectingDriver struct {
	branchingDriver
	state map[string]vcs.WorktreeState
}

func newInspectingDriver() *inspectingDriver {
	return &inspectingDriver{
		branchingDriver: *newBranchingDriver(),
		state:           map[string]vcs.WorktreeState{},
	}
}

func (*inspectingDriver) Name() string { return "inspecting" }

func (d *inspectingDriver) Inspect(_ string, wt vcs.Worktree) (vcs.WorktreeState, error) {
	return d.state[filepath.Base(wt.Path)], nil
}

func TestRemoveCheckNeedsAnInspector(t *testing.T) {
	// Silently removing without checking would be the dangerous reading of a
	// flag whose whole purpose is caution.
	driver := &plainDriver{}
	if err := runAdd(testOrchard(driver), []string{"wt1"}, ""); err != nil {
		t.Fatalf("runAdd() error = %v", err)
	}

	err := runRemove(testOrchard(driver), []string{"wt1"}, true)
	if err == nil || !strings.Contains(err.Error(), "--check") {
		t.Fatalf("runRemove() error = %v, want --check refused", err)
	}
	if len(driver.removed) != 0 {
		t.Errorf("runRemove() removed a worktree it could not check")
	}
}

func TestRemoveCheckRefusesWorktreesHoldingWork(t *testing.T) {
	driver := newInspectingDriver()
	if err := runAdd(testOrchard(driver), []string{"clean", "dirty", "unpushed"}, ""); err != nil {
		t.Fatalf("runAdd() error = %v", err)
	}
	driver.state["dirty"] = vcs.WorktreeState{Dirty: true}
	driver.state["unpushed"] = vcs.WorktreeState{Unpublished: true}

	err := runRemove(testOrchard(driver), []string{"clean", "dirty", "unpushed"}, true)
	if err == nil {
		t.Fatal("runRemove() error = nil, want a refusal")
	}
	// Every offender is named, so a batch is fixed in one pass.
	for _, want := range []string{"dirty (local changes)", "unpushed (unpushed commits)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("runRemove() error = %q, want it to mention %q", err.Error(), want)
		}
	}
	// Nothing is removed, not even the worktree that was safe: a batch is
	// refused whole rather than part-way through.
	if len(driver.removed) != 0 {
		t.Errorf("runRemove() removed %d worktree(s), want none", len(driver.removed))
	}
}

func TestRemoveCheckAllowsCleanWorktrees(t *testing.T) {
	driver := newInspectingDriver()
	if err := runAdd(testOrchard(driver), []string{"clean"}, ""); err != nil {
		t.Fatalf("runAdd() error = %v", err)
	}
	if err := runRemove(testOrchard(driver), []string{"clean"}, true); err != nil {
		t.Fatalf("runRemove() error = %v", err)
	}
	if len(driver.removed) != 1 {
		t.Errorf("runRemove() removed %d worktree(s), want 1", len(driver.removed))
	}
}
