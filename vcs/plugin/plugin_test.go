package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	iplugin "github.com/rwmyers/orchard-cli/internal/plugin"
	"github.com/rwmyers/orchard-cli/vcs"
)

func fakePlugin(t *testing.T, name, script string) *Driver {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the plugin protocol is exercised with shell scripts")
	}
	path := filepath.Join(t.TempDir(), "orchard-vcs-"+name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("writing plugin: %v", err)
	}
	return New(iplugin.Found{Role: iplugin.RoleVCS, Name: name, Path: path})
}

// describing replies to describe with the given capability list and to
// everything else with what follows.
func describing(caps, rest string) string {
	return `if [ "$1" = "describe" ]; then
  echo '{"api_version":1,"name":"demo","version":"1.0","capabilities":[` + caps + `]}'
  exit 0
fi
` + rest
}

func TestCapabilitiesComeFromDescribe(t *testing.T) {
	t.Run("what the plugin declares is what orchard offers", func(t *testing.T) {
		d := fakePlugin(t, "partial", describing(`"update","inspect"`, `echo '{}'`))
		want := vcs.Capabilities{Update: true, Inspect: true}
		if got := vcs.CapabilitiesOf(d); got != want {
			t.Errorf("CapabilitiesOf() = %+v, want %+v", got, want)
		}
	})

	t.Run("a plugin declaring nothing gets nothing optional", func(t *testing.T) {
		d := fakePlugin(t, "minimal", describing(``, `echo '{}'`))
		if got := vcs.CapabilitiesOf(d); got != (vcs.Capabilities{}) {
			t.Errorf("CapabilitiesOf() = %+v, want nothing", got)
		}
	})

	t.Run("a plugin that cannot be described offers nothing", func(t *testing.T) {
		d := fakePlugin(t, "broken", `echo '{"error":"boom"}'; exit 1`)
		if got := vcs.CapabilitiesOf(d); got != (vcs.Capabilities{}) {
			t.Errorf("CapabilitiesOf() = %+v, want nothing", got)
		}
		// But it must not look like a working driver that simply does little:
		// the failure has to be reachable.
		source, err := d.Status()
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("Status() error = %v, want the plugin's complaint", err)
		}
		if !strings.Contains(source, "orchard-vcs-broken") {
			t.Errorf("Status() source = %q, want it to name the executable", source)
		}
	})
}

func TestDetect(t *testing.T) {
	t.Run("a claimed directory reports its root", func(t *testing.T) {
		d := fakePlugin(t, "claims", describing(``, `echo '{"root":"/repo"}'`))
		root, err := d.Detect("/repo/sub")
		if err != nil {
			t.Fatalf("Detect() error = %v", err)
		}
		if root != "/repo" {
			t.Errorf("Detect() = %q, want %q", root, "/repo")
		}
	})

	t.Run("declining is not a failure", func(t *testing.T) {
		// Every plugin but one declines any given directory, so this has to be
		// distinguishable from a plugin that is broken.
		d := fakePlugin(t, "declines", describing(``, `echo '{"not_repository":true}'`))
		if _, err := d.Detect("/elsewhere"); err != vcs.ErrNotRepository {
			t.Errorf("Detect() error = %v, want ErrNotRepository", err)
		}
	})

	t.Run("a broken plugin fails rather than declining", func(t *testing.T) {
		d := fakePlugin(t, "sad", describing(``, `echo '{"error":"disk on fire"}'; exit 1`))
		_, err := d.Detect("/repo")
		if err == nil || err == vcs.ErrNotRepository {
			t.Fatalf("Detect() error = %v, want the plugin's failure reported", err)
		}
		if !strings.Contains(err.Error(), "disk on fire") {
			t.Errorf("Detect() error = %q, want the plugin's message", err)
		}
	})
}

func TestVerbsAreRefusedUntilDescribeSucceeds(t *testing.T) {
	// A plugin speaking the wrong protocol must fail with that complaint,
	// not with whatever its verbs happen to do.
	d := fakePlugin(t, "future", `echo '{"api_version":99,"name":"future"}'`)
	_, err := d.ListWorktrees("/repo")
	if err == nil || !strings.Contains(err.Error(), "api version") {
		t.Fatalf("ListWorktrees() error = %v, want the version mismatch", err)
	}
}

func TestListWorktrees(t *testing.T) {
	d := fakePlugin(t, "lister", describing(``,
		`echo '{"worktrees":[{"name":"wt1","path":"/plants/wt1"},{"name":"wt2","path":"/plants/wt2"}]}'`))
	got, err := d.ListWorktrees("/repo")
	if err != nil {
		t.Fatalf("ListWorktrees() error = %v", err)
	}
	want := []vcs.Worktree{{Name: "wt1", Path: "/plants/wt1"}, {Name: "wt2", Path: "/plants/wt2"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ListWorktrees() = %+v, want %+v", got, want)
	}
}

func TestInspectDefaultsToUnsafe(t *testing.T) {
	// Nothing may conclude a worktree is disposable because a plugin failed to
	// answer.
	d := fakePlugin(t, "sad", describing(`"inspect"`, `echo '{"error":"cannot tell"}'; exit 1`))
	state, err := d.Inspect("/repo", vcs.Worktree{Name: "wt1", Path: "/plants/wt1"})
	if err == nil {
		t.Fatal("Inspect() error = nil, want the failure reported")
	}
	if state.Safe() {
		t.Errorf("Inspect() = %+v, want it treated as holding work", state)
	}
}

func TestRequestFieldNamesAreTheDocumentedOnes(t *testing.T) {
	// The wire names must be the lower-case ones in docs/PLUGINS.md, not Go's
	// exported field names. Dropping the json tags on vcs.AddRequest silently
	// sends {"Root":...,"Name":...} instead, which every plugin written to the
	// documentation would reject.
	check := `body=$(cat)
for field in '"root":"/repo"' '"name":"wt1"' '"path":"/plants/wt1"' '"base":"main"'; do
  case "$body" in
    *"$field"*) ;;
    *) echo "{\"error\":\"request is missing $field\"}"; exit 1 ;;
  esac
done
echo '{}'`

	d := fakePlugin(t, "strict", describing(`"base"`, check))
	err := d.AddWorktree(vcs.AddRequest{Root: "/repo", Name: "wt1", Path: "/plants/wt1", Base: "main"})
	if err != nil {
		t.Fatalf("AddWorktree() error = %v", err)
	}
}
