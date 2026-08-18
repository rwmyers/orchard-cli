// Package plugin lets a version control driver be an external executable
// rather than a Go package compiled into orchard.
//
// Importing it registers every orchard-vcs-<name> on $PATH as an ordinary
// [vcs.Driver]. Nothing downstream can tell the difference: the same registry,
// the same interface, the same capability gating. This package is an adapter,
// not a second extension mechanism — [vcs.Driver] remains the contract, and
// this is one way of satisfying it.
//
// # Which one to write
//
// A driver in Go is checked against the interface by the compiler, needs no
// process per call, and cannot get the protocol wrong. Write one of those
// unless you have a reason not to.
//
// The reasons not to are real, though: a plugin can be written in any language,
// including a shell script; it can be installed by dropping a file on $PATH
// rather than rebuilding orchard; and it can be distributed by someone who
// would rather not ship a binary at all. When those matter, write a plugin.
//
// # Capabilities
//
// A Go driver opts into the optional parts of orchard's model by implementing
// their interfaces, which is settled at compile time. A plugin cannot do that,
// so it declares its capabilities in the describe reply and this adapter
// reports them through [vcs.Capable]. The adapter implements every optional
// interface, and [vcs.CapabilitiesOf] intersects what it implements with what
// the plugin declared — so a plugin gets exactly what it asked for and orchard
// never calls a verb the plugin did not claim.
//
// See docs/PLUGINS.md for the wire protocol.
package plugin

import (
	"fmt"
	"os"
	"sync"

	"github.com/rwmyers/orchard-cli/internal/plugin"
	"github.com/rwmyers/orchard-cli/vcs"
)

func init() { Register() }

// Register adds every discovered orchard-vcs-* to the driver registry.
// Importing this package does it; it is exported for a binary that would rather
// call it explicitly, and it is safe to call more than once.
func Register() {
	for _, found := range plugin.Discover(plugin.RoleVCS) {
		if _, taken := vcs.Lookup(found.Name); taken {
			// A driver compiled in beats a plugin of the same name. That way
			// installing an experimental orchard-vcs-git cannot silently
			// displace the built-in one.
			continue
		}
		vcs.Register(New(found))
	}
}

// Verbs. These mirror the methods of [vcs.Driver] and its optional interfaces
// one for one, so the protocol has no shape of its own to learn.
const (
	verbDetect         = "detect"
	verbListWorktrees  = "list-worktrees"
	verbAddWorktree    = "add-worktree"
	verbRemoveWorktree = "remove-worktree"
	verbBranchExists   = "branch-exists"
	verbDeleteBranch   = "delete-branch"
	verbUpdateRoot     = "update-root"
	verbBaseExists     = "base-exists"
	verbIgnores        = "ignores"
	verbInspect        = "inspect"
)

// Driver adapts one plugin executable to [vcs.Driver].
//
// It implements every optional interface unconditionally, because Go interfaces
// are settled at compile time and the plugin's capabilities are not. What the
// plugin actually offers is reported through [vcs.Capabilities], which orchard
// intersects with the interfaces implemented here; the methods below are
// therefore never reached for a capability the plugin did not declare.
type Driver struct {
	client *plugin.Client

	once sync.Once
	desc *plugin.Description
	err  error
}

var (
	_ vcs.Driver       = (*Driver)(nil)
	_ vcs.Capable      = (*Driver)(nil)
	_ vcs.Brancher     = (*Driver)(nil)
	_ vcs.Updater      = (*Driver)(nil)
	_ vcs.BaseResolver = (*Driver)(nil)
	_ vcs.Ignorer      = (*Driver)(nil)
	_ vcs.Inspector    = (*Driver)(nil)
)

// New builds a driver for a discovered plugin. The plugin is not run until
// something is asked of it, so discovering plugins costs no processes and
// `orchard --help` does not execute anything.
func New(found plugin.Found) *Driver {
	client := plugin.New(found)
	client.Stderr = os.Stderr
	return &Driver{client: client}
}

// Configure hands the plugin its own [vcs.<name>] configuration section, to be
// passed through verbatim on every call. Orchard does not look inside it.
//
// This is the seam for the sectioned configuration file: once orchard.conf is
// read through internal/conf, the command line calls this with the entries of
// the matching section. Until then a plugin simply receives no configuration.
func (d *Driver) Configure(config map[string]string) { d.client.Config = config }

// Name is taken from the filename rather than from describe, so that it is
// known without running anything and always matches the configuration section
// that names the plugin.
func (d *Driver) Name() string { return d.client.Name }

// describe runs describe once and remembers the outcome, including a failure.
// A plugin that cannot be described is not retried on every call.
func (d *Driver) describe() (*plugin.Description, error) {
	d.once.Do(func() { d.desc, d.err = d.client.Describe() })
	return d.desc, d.err
}

// Capabilities reports what the plugin declared. A plugin that cannot be
// described declares nothing, which leaves it with only the mandatory methods —
// and those report the failure when called, so it is never silently skipped.
func (d *Driver) Capabilities() vcs.Capabilities {
	desc, err := d.describe()
	if err != nil {
		return vcs.Capabilities{}
	}
	return vcs.CapabilitiesFromNames(desc.Capabilities)
}

// Describe exposes the plugin's own account of itself.
func (d *Driver) Describe() (*plugin.Description, error) { return d.describe() }

// Status names the executable this driver runs, and reports whatever stopped it
// loading. Running describe is the only way to find out, so this is what makes
// `orchard drivers` the place a misbehaving plugin shows up.
func (d *Driver) Status() (string, error) {
	source := "plugin " + d.client.Path
	desc, err := d.describe()
	if err != nil {
		return source, err
	}
	if desc.Version != "" {
		source += " (" + desc.Version + ")"
	}
	return source, nil
}

// call refuses to run any verb until describe has succeeded, so a plugin
// speaking the wrong protocol version fails with that complaint rather than
// with whatever its verbs happen to do.
func (d *Driver) call(verb string, params, result any) error {
	if _, err := d.describe(); err != nil {
		return err
	}
	return d.client.Call(verb, params, result)
}

type detectParams struct {
	Dir string `json:"dir"`
}

type detectResult struct {
	Root string `json:"root"`
	// NotRepository is how a plugin declines a directory. It is a field
	// rather than an error reply because declining is the normal outcome for
	// every plugin but one, and orchard must be able to tell it apart from a
	// plugin that is broken.
	NotRepository bool `json:"not_repository"`
}

func (d *Driver) Detect(dir string) (string, error) {
	var result detectResult
	if err := d.call(verbDetect, detectParams{Dir: dir}, &result); err != nil {
		return "", err
	}
	if result.NotRepository || result.Root == "" {
		return "", vcs.ErrNotRepository
	}
	return result.Root, nil
}

type rootParams struct {
	Root string `json:"root"`
}

type listResult struct {
	Worktrees []vcs.Worktree `json:"worktrees"`
}

func (d *Driver) ListWorktrees(root string) ([]vcs.Worktree, error) {
	var result listResult
	if err := d.call(verbListWorktrees, rootParams{Root: root}, &result); err != nil {
		return nil, err
	}
	return result.Worktrees, nil
}

func (d *Driver) AddWorktree(req vcs.AddRequest) error {
	return d.call(verbAddWorktree, req, nil)
}

func (d *Driver) RemoveWorktree(req vcs.RemoveRequest) error {
	return d.call(verbRemoveWorktree, req, nil)
}

type branchParams struct {
	Root string `json:"root"`
	Name string `json:"name"`
}

type existsResult struct {
	Exists bool `json:"exists"`
}

func (d *Driver) BranchExists(root, name string) (bool, error) {
	var result existsResult
	err := d.call(verbBranchExists, branchParams{Root: root, Name: name}, &result)
	return result.Exists, err
}

func (d *Driver) DeleteBranch(root, name string) error {
	return d.call(verbDeleteBranch, branchParams{Root: root, Name: name}, nil)
}

func (d *Driver) UpdateRoot(root string) error {
	return d.call(verbUpdateRoot, rootParams{Root: root}, nil)
}

type baseParams struct {
	Root string `json:"root"`
	Base string `json:"base"`
}

func (d *Driver) BaseExists(root, base string) (bool, error) {
	var result existsResult
	err := d.call(verbBaseExists, baseParams{Root: root, Base: base}, &result)
	return result.Exists, err
}

type ignoresParams struct {
	Root string `json:"root"`
	Path string `json:"path"`
}

type ignoresResult struct {
	Ignored bool `json:"ignored"`
}

func (d *Driver) Ignores(root, path string) (bool, error) {
	var result ignoresResult
	err := d.call(verbIgnores, ignoresParams{Root: root, Path: path}, &result)
	return result.Ignored, err
}

type inspectParams struct {
	Root string       `json:"root"`
	Tree vcs.Worktree `json:"worktree"`
}

type inspectResult struct {
	Dirty       bool `json:"dirty"`
	Unpublished bool `json:"unpublished"`
}

// Inspect reports what a plugin says about a worktree, defaulting to unsafe if
// the plugin cannot be reached. Nothing may conclude a worktree is disposable
// because a plugin failed to answer.
func (d *Driver) Inspect(root string, wt vcs.Worktree) (vcs.WorktreeState, error) {
	var result inspectResult
	if err := d.call(verbInspect, inspectParams{Root: root, Tree: wt}, &result); err != nil {
		return vcs.WorktreeState{Dirty: true, Unpublished: true},
			fmt.Errorf("inspecting %s: %w", wt.Path, err)
	}
	return vcs.WorktreeState{Dirty: result.Dirty, Unpublished: result.Unpublished}, nil
}
