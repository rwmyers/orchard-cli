// Package vcs defines the interface orchard uses to talk to a version control
// system, and the registry drivers add themselves to.
//
// Orchard ships with a driver for git. Support for another system is added
// without changing orchard: implement [Driver] in a package of your own and
// register it from init,
//
//	package jj
//
//	import "github.com/rwmyers/orchard-cli/vcs"
//
//	func init() { vcs.Register(Driver{}) }
//
// then build a binary that imports that package alongside orchard's cli:
//
//	package main
//
//	import (
//		"github.com/rwmyers/orchard-cli/cli"
//		_ "github.com/rwmyers/orchard-cli/vcs/git"
//		_ "github.com/example/orchard-jj"
//	)
//
//	func main() { cli.Main() }
//
// [Driver] covers only what every system orchard can drive must do. The rest of
// orchard's model is optional, and a driver opts into each part by implementing
// the interface for it — [Brancher], [Updater], [BaseResolver], [Ignorer].
// Orchard skips the steps a driver has not opted into rather than assuming
// them, so a system that has no equivalent of git's branch-per-worktree does
// not have to pretend otherwise. [CapabilitiesOf] reports what a driver opted
// into.
package vcs

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrNotRepository is what Detect returns when a directory belongs to some
// other version control system. It is not a failure: orchard tries every
// registered driver, and only reports trouble when they all decline. A driver
// that recognises the directory but cannot work with it should return a
// descriptive error instead, which stops the search and is shown to the user.
var ErrNotRepository = errors.New("not a repository this driver handles")

// Worktree is one working copy attached to a repository. Git calls these
// worktrees and jj calls them workspaces; orchard calls them worktrees
// throughout.
type Worktree struct {
	// Name is what the driver knows the worktree by. Orchard identifies
	// worktrees by Path, so a driver with no separate notion of a name may
	// leave this as the base name of Path.
	Name string `json:"name"`
	// Path is the absolute, cleaned path of the working copy.
	Path string `json:"path"`
}

// AddRequest describes one worktree to create.
type AddRequest struct {
	// Root is the repository the worktree is created from.
	Root string `json:"root"`
	// Name is what the worktree is called. For a driver implementing
	// [Brancher] it is also the name of the branch to create.
	Name string `json:"name"`
	// Path is where the worktree goes. Its parent directory exists; Path
	// itself does not.
	Path string `json:"path"`
	// Base is the branch or commit the worktree starts from, or "" to start
	// from wherever the root tree currently is. It is only ever set for a
	// driver implementing [BaseResolver], so a driver that does not implement
	// that interface may ignore it.
	Base string `json:"base,omitempty"`
}

// RemoveRequest describes one worktree to remove. The driver is responsible for
// leaving nothing behind at Path, including the directory itself.
type RemoveRequest struct {
	Root string `json:"root"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// Driver is the minimum orchard needs to manage worktrees in a version control
// system. Implementations must be safe to register from init and are used from
// a single goroutine.
type Driver interface {
	// Name identifies the driver in the `vcs` configuration key, in
	// `orchard drivers`, and in error messages. It must be unique among
	// registered drivers, and should be the name of the command users know
	// the system by — "git", "jj", "hg".
	Name() string

	// Detect reports the root of the repository containing dir, which is an
	// existing directory. It returns [ErrNotRepository] when dir belongs to
	// another system, and a descriptive error when dir is a repository this
	// driver recognises but cannot manage worktrees in.
	Detect(dir string) (root string, err error)

	// ListWorktrees returns every working copy attached to root, root itself
	// included. Orchard picks out the ones it manages by path, so returning
	// extras is harmless and omitting any is not.
	ListWorktrees(root string) ([]Worktree, error)

	// AddWorktree creates the worktree described by req. Orchard has already
	// checked that nothing is planted at req.Path and, for a [Brancher], that
	// the branch name is free.
	AddWorktree(req AddRequest) error

	// RemoveWorktree removes the worktree described by req and deletes its
	// directory. Any branch is dealt with separately through [Brancher], so a
	// driver need not touch one here.
	RemoveWorktree(req RemoveRequest) error
}

// Brancher is implemented by drivers that tie each worktree to a branch of the
// same name, as git does. Orchard refuses to plant a worktree whose branch name
// is taken, insists the branch is present before removing one, and deletes it
// afterwards.
//
// A driver whose worktrees are independent of any named ref — jj, whose
// workspaces are not tied to bookmarks — should leave this unimplemented, and
// orchard skips all three steps.
type Brancher interface {
	// BranchExists reports whether name is already a branch in root.
	BranchExists(root, name string) (bool, error)
	// DeleteBranch removes the branch called name from root, discarding
	// anything on it that was never merged. It is called after the
	// corresponding worktree has gone.
	DeleteBranch(root, name string) error
}

// Updater is implemented by drivers that can refresh the root tree from a
// remote. Orchard does so once before creating a batch of worktrees, so that
// they start from current work. `orchard add` proceeds without it for a driver
// that does not implement it.
type Updater interface {
	UpdateRoot(root string) error
}

// BaseResolver is implemented by drivers that can start a worktree from a
// nominated branch or commit, which is what `orchard add --base` asks for.
// Orchard rejects --base outright for a driver that does not implement it,
// rather than quietly ignoring the flag.
type BaseResolver interface {
	// BaseExists reports whether base names something worktrees can be
	// created from. Orchard checks this once, after any update and before
	// creating anything, so that an unusable base is reported before a batch
	// is half planted.
	BaseExists(root, base string) (bool, error)
}

// Ignorer is implemented by drivers that can say whether the repository's
// ignore rules already cover a path. `orchard setup` uses it to decide whether
// to suggest ignoring the configuration file it just wrote inside a repository.
type Ignorer interface {
	Ignores(root, path string) (bool, error)
}

// WorktreeState is what removing a worktree right now would destroy. Both
// fields are conservative: a driver that cannot tell should report true, since
// the cost of a needless refusal is an argument and the cost of a wrong
// go-ahead is lost work.
type WorktreeState struct {
	// Dirty reports changes in the working copy that are not committed
	// anywhere.
	Dirty bool `json:"dirty"`
	// Unpublished reports commits that exist only in this worktree's line of
	// work — not merged into the root tree's current branch and not present
	// on any remote. Removing it would be the only copy gone.
	Unpublished bool `json:"unpublished"`
}

// Safe reports whether removing the worktree would lose nothing.
func (s WorktreeState) Safe() bool { return !s.Dirty && !s.Unpublished }

// Inspector is implemented by drivers that can report whether a worktree still
// holds work. It backs `orchard remove --check`, and it is how anything else in
// orchard — the harness reclaimer, which decides whether a worktree whose
// conversation has finished may be taken back — asks that question. Reclaiming
// must never shell out to a version control system directly; a repository that
// orchard drives through a plugin has to be answerable through the same
// interface.
//
// A driver that leaves this out is one whose worktrees orchard will not remove
// on its own initiative, only when a person asks.
type Inspector interface {
	Inspect(root string, wt Worktree) (WorktreeState, error)
}

// Capabilities reports which of the optional interfaces a driver implements.
// It is derived from the driver rather than declared by it, so the two can
// never disagree.
type Capabilities struct {
	// Branches is set when the driver implements [Brancher].
	Branches bool
	// Update is set when the driver implements [Updater].
	Update bool
	// BaseRef is set when the driver implements [BaseResolver].
	BaseRef bool
	// Ignores is set when the driver implements [Ignorer].
	Ignores bool
	// Inspect is set when the driver implements [Inspector].
	Inspect bool
}

// Capability names, as they appear in the capabilities list an external plugin
// returns from describe. A driver written in Go never deals in these: it opts
// in by implementing an interface, and the compiler checks the spelling.
const (
	CapBranches = "branches"
	CapUpdate   = "update"
	CapBaseRef  = "base"
	CapIgnores  = "ignore"
	CapInspect  = "inspect"
)

// CapabilitiesFromNames reads a plugin's declared capability list. Names it
// does not recognise are ignored, so a plugin written against a later orchard
// still loads with the parts this one understands.
func CapabilitiesFromNames(names []string) Capabilities {
	var caps Capabilities
	for _, name := range names {
		switch name {
		case CapBranches:
			caps.Branches = true
		case CapUpdate:
			caps.Update = true
		case CapBaseRef:
			caps.BaseRef = true
		case CapIgnores:
			caps.Ignores = true
		case CapInspect:
			caps.Inspect = true
		}
	}
	return caps
}

// Names lists the capabilities in the form a plugin declares them.
func (c Capabilities) Names() []string {
	var names []string
	for _, pair := range []struct {
		name string
		on   bool
	}{
		{CapBranches, c.Branches},
		{CapUpdate, c.Update},
		{CapBaseRef, c.BaseRef},
		{CapIgnores, c.Ignores},
		{CapInspect, c.Inspect},
	} {
		if pair.on {
			names = append(names, pair.name)
		}
	}
	return names
}

// Capable is implemented by drivers whose capabilities are not fixed at compile
// time — in practice the adapter for an external plugin, which learns them from
// the plugin's describe reply.
//
// A driver written in Go should not implement this. Opting in by implementing
// an interface leaves nothing to keep in step; declaring capabilities separately
// creates two things that can disagree. [CapabilitiesOf] guards against the
// disagreement anyway by taking only what is both declared and implemented, so
// a Capable driver can narrow what it offers but never claim more than it can
// do.
type Capable interface {
	Capabilities() Capabilities
}

// Diagnostic is implemented by drivers that might fail to load — in practice
// the adapter for an external plugin, which has to run the thing before it
// knows anything about it. `orchard drivers` uses it to say where a driver came
// from, and why one is offering nothing.
//
// A driver compiled into the binary cannot fail to load and has no reason to
// implement this; [StatusOf] reports it as built in.
type Diagnostic interface {
	// Status returns a short description of where the driver came from, and
	// the error that stopped it loading, if any.
	Status() (source string, err error)
}

// StatusOf reports where a driver came from and whether it is usable.
func StatusOf(d Driver) (source string, err error) {
	if diag, ok := d.(Diagnostic); ok {
		return diag.Status()
	}
	return "built-in", nil
}

// CapabilitiesOf reports what d opted into: the interfaces it implements,
// narrowed by its own declaration if it makes one.
func CapabilitiesOf(d Driver) Capabilities {
	var caps Capabilities
	_, caps.Branches = d.(Brancher)
	_, caps.Update = d.(Updater)
	_, caps.BaseRef = d.(BaseResolver)
	_, caps.Ignores = d.(Ignorer)
	_, caps.Inspect = d.(Inspector)

	declarer, ok := d.(Capable)
	if !ok {
		return caps
	}
	declared := declarer.Capabilities()
	return Capabilities{
		Branches: caps.Branches && declared.Branches,
		Update:   caps.Update && declared.Update,
		BaseRef:  caps.BaseRef && declared.BaseRef,
		Ignores:  caps.Ignores && declared.Ignores,
		Inspect:  caps.Inspect && declared.Inspect,
	}
}

var (
	mu      sync.RWMutex
	drivers = map[string]Driver{}
)

// Register adds d to the set of drivers orchard can use, and is meant to be
// called from a driver package's init. It panics on a nil driver, an empty
// name, or a name already registered, since each is a mistake in the program
// rather than something that can happen at runtime.
func Register(d Driver) {
	if d == nil {
		panic("vcs: Register called with a nil driver")
	}
	name := d.Name()
	if name == "" {
		panic("vcs: Register called with an unnamed driver")
	}

	mu.Lock()
	defer mu.Unlock()
	if _, dup := drivers[name]; dup {
		panic(fmt.Sprintf("vcs: driver %q is registered twice", name))
	}
	drivers[name] = d
}

// Lookup returns the driver registered under name.
func Lookup(name string) (Driver, bool) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := drivers[name]
	return d, ok
}

// Names lists the registered drivers in alphabetical order.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(drivers))
	for name := range drivers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Registered lists the drivers themselves, ordered by name.
func Registered() []Driver {
	names := Names()
	list := make([]Driver, 0, len(names))
	for _, name := range names {
		if d, ok := Lookup(name); ok {
			list = append(list, d)
		}
	}
	return list
}

// ErrNoDriver is returned by [Detect] when no registered driver recognises a
// directory.
var ErrNoDriver = errors.New("no registered driver recognises this directory")

// Detect finds the driver for the repository containing dir and returns it
// along with the repository root. Drivers are tried in alphabetical order, so
// the result does not depend on the order they were imported in; a directory
// that somehow belongs to two systems is disambiguated with the `vcs`
// configuration key.
//
// A driver that recognises dir but reports a problem with it stops the search,
// so that the specific complaint — a bare repository, say — reaches the user
// instead of a blanket "not a repository".
func Detect(dir string) (Driver, string, error) {
	return detect(Registered(), dir)
}

// detect is Detect over an explicit set of drivers, so that the search can be
// exercised without touching the process-wide registry.
func detect(drivers []Driver, dir string) (Driver, string, error) {
	names := make([]string, 0, len(drivers))
	for _, d := range drivers {
		names = append(names, d.Name())
		root, err := d.Detect(dir)
		if errors.Is(err, ErrNotRepository) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return d, root, nil
	}
	return nil, "", fmt.Errorf("%s: %w (tried %v)", dir, ErrNoDriver, names)
}
