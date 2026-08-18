package vcs

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// bare implements no more than [Driver], standing in for a system with none of
// orchard's optional model — no branches tied to worktrees, no remote to update
// from, no base to start at.
type bare struct {
	name      string
	root      string
	detectErr error
}

func (b bare) Name() string { return b.name }

func (b bare) Detect(string) (string, error) {
	if b.detectErr != nil {
		return "", b.detectErr
	}
	return b.root, nil
}

func (bare) ListWorktrees(string) ([]Worktree, error) { return nil, nil }
func (bare) AddWorktree(AddRequest) error             { return nil }
func (bare) RemoveWorktree(RemoveRequest) error       { return nil }

// full opts into every optional interface, the way the built-in git driver
// does.
type full struct{ bare }

func (full) BranchExists(string, string) (bool, error) { return false, nil }
func (full) DeleteBranch(string, string) error         { return nil }
func (full) UpdateRoot(string) error                   { return nil }
func (full) BaseExists(string, string) (bool, error)   { return false, nil }
func (full) Ignores(string, string) (bool, error)      { return false, nil }

func TestCapabilitiesOf(t *testing.T) {
	t.Run("a driver opting into nothing", func(t *testing.T) {
		if got := CapabilitiesOf(bare{name: "bare"}); got != (Capabilities{}) {
			t.Errorf("CapabilitiesOf() = %+v, want everything false", got)
		}
	})

	t.Run("a driver opting into everything", func(t *testing.T) {
		want := Capabilities{Branches: true, Update: true, BaseRef: true, Ignores: true}
		if got := CapabilitiesOf(full{}); got != want {
			t.Errorf("CapabilitiesOf() = %+v, want %+v", got, want)
		}
	})
}

func TestRegister(t *testing.T) {
	// The registry is process-wide, so each driver registered here takes a
	// name nothing else uses.
	d := bare{name: "test-register"}
	Register(d)

	t.Run("a registered driver can be looked up", func(t *testing.T) {
		got, ok := Lookup("test-register")
		if !ok {
			t.Fatalf("Lookup() found nothing")
		}
		if got.Name() != d.Name() {
			t.Errorf("Lookup() = %q, want %q", got.Name(), d.Name())
		}
	})

	t.Run("an unregistered name is not found", func(t *testing.T) {
		if _, ok := Lookup("test-never-registered"); ok {
			t.Errorf("Lookup() found a driver that was never registered")
		}
	})

	t.Run("registering the same name twice panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Errorf("Register() did not panic on a duplicate name")
			}
		}()
		Register(bare{name: "test-register"})
	})

	t.Run("registering an unnamed driver panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Errorf("Register() did not panic on an empty name")
			}
		}()
		Register(bare{})
	})

	t.Run("registering a nil driver panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Errorf("Register() did not panic on a nil driver")
			}
		}()
		Register(nil)
	})
}

func TestNamesAreSorted(t *testing.T) {
	Register(bare{name: "test-sort-b"})
	Register(bare{name: "test-sort-a"})

	var seen []string
	for _, name := range Names() {
		if name == "test-sort-a" || name == "test-sort-b" {
			seen = append(seen, name)
		}
	}
	if want := []string{"test-sort-a", "test-sort-b"}; !reflect.DeepEqual(seen, want) {
		t.Errorf("Names() gave %v, want them in the order %v", seen, want)
	}
}

func TestDetect(t *testing.T) {
	declines := bare{name: "declines", detectErr: ErrNotRepository}
	broken := bare{name: "broken", detectErr: errors.New("bare repository")}
	claims := bare{name: "claims", root: "/repo"}

	t.Run("a driver that declines is passed over", func(t *testing.T) {
		driver, root, err := detect([]Driver{declines, claims}, "/somewhere")
		if err != nil {
			t.Fatalf("detect() error = %v", err)
		}
		if driver.Name() != "claims" || root != "/repo" {
			t.Errorf("detect() = %q, %q, want %q, %q", driver.Name(), root, "claims", "/repo")
		}
	})

	t.Run("the first driver to claim the directory wins", func(t *testing.T) {
		second := bare{name: "second", root: "/other"}
		driver, _, err := detect([]Driver{claims, second}, "/somewhere")
		if err != nil {
			t.Fatalf("detect() error = %v", err)
		}
		if driver.Name() != "claims" {
			t.Errorf("detect() chose %q, want the first claimant %q", driver.Name(), "claims")
		}
	})

	t.Run("a driver's own complaint stops the search", func(t *testing.T) {
		// Reporting "bare repository" beats falling through to a blanket "no
		// driver recognises this", which would be the wrong diagnosis.
		_, _, err := detect([]Driver{broken, claims}, "/somewhere")
		if err == nil || err.Error() != "bare repository" {
			t.Errorf("detect() error = %v, want the driver's own complaint", err)
		}
	})

	t.Run("no driver recognising the directory is reported", func(t *testing.T) {
		_, _, err := detect([]Driver{declines}, "/somewhere")
		if !errors.Is(err, ErrNoDriver) {
			t.Errorf("detect() error = %v, want ErrNoDriver", err)
		}
		if got := err.Error(); !strings.Contains(got, "declines") || !strings.Contains(got, "/somewhere") {
			t.Errorf("detect() error = %q, want it to name the directory and what was tried", got)
		}
	})

	t.Run("an empty registry is reported the same way", func(t *testing.T) {
		if _, _, err := detect(nil, "/somewhere"); !errors.Is(err, ErrNoDriver) {
			t.Errorf("detect() error = %v, want ErrNoDriver", err)
		}
	})
}

// declaring implements every optional interface but narrows what it offers at
// runtime, the way the adapter for an external plugin does.
type declaring struct {
	full
	declared Capabilities
}

func (d declaring) Capabilities() Capabilities { return d.declared }

func (declaring) Inspect(string, Worktree) (WorktreeState, error) { return WorktreeState{}, nil }

func TestCapabilitiesOfADeclaringDriver(t *testing.T) {
	t.Run("a declaration narrows what is offered", func(t *testing.T) {
		d := declaring{declared: Capabilities{Update: true, Inspect: true}}
		want := Capabilities{Update: true, Inspect: true}
		if got := CapabilitiesOf(d); got != want {
			t.Errorf("CapabilitiesOf() = %+v, want %+v", got, want)
		}
	})

	t.Run("a declaration cannot claim more than is implemented", func(t *testing.T) {
		// A wrong describe reply must not make orchard call a method that is
		// not there. declaringBare implements none of the optional
		// interfaces, so declaring them buys it nothing.
		d := declaringBare{declared: Capabilities{Branches: true, Update: true}}
		if got := CapabilitiesOf(d); got != (Capabilities{}) {
			t.Errorf("CapabilitiesOf() = %+v, want nothing claimable", got)
		}
	})
}

// declaringBare declares capabilities it does not implement.
type declaringBare struct {
	bare
	declared Capabilities
}

func (d declaringBare) Capabilities() Capabilities { return d.declared }

func TestCapabilityNames(t *testing.T) {
	names := []string{CapBranches, CapUpdate, CapBaseRef, CapIgnores, CapInspect}
	caps := CapabilitiesFromNames(names)
	want := Capabilities{Branches: true, Update: true, BaseRef: true, Ignores: true, Inspect: true}
	if caps != want {
		t.Errorf("CapabilitiesFromNames(%v) = %+v, want %+v", names, caps, want)
	}
	if got := caps.Names(); !reflect.DeepEqual(got, names) {
		t.Errorf("Names() = %v, want %v", got, names)
	}

	t.Run("an unknown capability is ignored", func(t *testing.T) {
		// A plugin written against a later orchard still loads with the parts
		// this one understands.
		caps := CapabilitiesFromNames([]string{CapUpdate, "teleport"})
		if want := (Capabilities{Update: true}); caps != want {
			t.Errorf("CapabilitiesFromNames() = %+v, want %+v", caps, want)
		}
	})
}
