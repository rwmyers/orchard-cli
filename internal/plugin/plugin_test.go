package plugin

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writePlugin drops an executable shell script into dir under the orchard
// naming convention and returns its path.
func writePlugin(t *testing.T, dir, role, name, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the plugin protocol is exercised with shell scripts")
	}
	path := filepath.Join(dir, "orchard-"+role+"-"+name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("writing plugin: %v", err)
	}
	return path
}

// echoing replies with a fixed describe and echoes its stdin back for any other
// verb, so a test can check what orchard sent.
const echoing = `
if [ "$1" = "describe" ]; then
  echo '{"api_version":1,"name":"demo","version":"9.9","capabilities":["branches","inspect"]}'
else
  cat
fi
`

func TestDiscover(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "vcs", "demo", echoing)
	writePlugin(t, dir, "harness", "claude-code", echoing)

	// A file matching the pattern but not executable is not a plugin.
	if err := os.WriteFile(filepath.Join(dir, "orchard-vcs-notexec"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}
	// Nor is something that merely looks similar.
	writePlugin(t, dir, "vcs", "", echoing)

	t.Setenv("PATH", dir)

	t.Run("finds the plugins for a role", func(t *testing.T) {
		found := Discover(RoleVCS)
		if len(found) != 1 || found[0].Name != "demo" {
			t.Fatalf("Discover(vcs) = %+v, want just demo", found)
		}
		if found[0].Role != RoleVCS {
			t.Errorf("Discover(vcs) role = %q", found[0].Role)
		}
	})

	t.Run("roles do not see each other's plugins", func(t *testing.T) {
		found := Discover(RoleHarness)
		if len(found) != 1 || found[0].Name != "claude-code" {
			t.Fatalf("Discover(harness) = %+v, want just claude-code", found)
		}
	})

	t.Run("an unused role finds nothing", func(t *testing.T) {
		if found := Discover(Role("nosuchrole")); len(found) != 0 {
			t.Errorf("Discover() = %+v, want nothing", found)
		}
	})
}

func TestDiscoverPrefersTheEarlierPathEntry(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	wanted := writePlugin(t, first, "vcs", "demo", echoing)
	writePlugin(t, second, "vcs", "demo", echoing)

	t.Setenv("PATH", first+string(os.PathListSeparator)+second)

	found := Discover(RoleVCS)
	if len(found) != 1 {
		t.Fatalf("Discover() = %+v, want one entry", found)
	}
	// A shell would run the first one on PATH; so does orchard.
	if found[0].Path != wanted {
		t.Errorf("Discover() chose %q, want %q", found[0].Path, wanted)
	}
}

func TestDescribe(t *testing.T) {
	dir := t.TempDir()

	t.Run("reports what the plugin says", func(t *testing.T) {
		path := writePlugin(t, dir, "vcs", "ok", echoing)
		desc, err := New(Found{Role: RoleVCS, Name: "ok", Path: path}).Describe()
		if err != nil {
			t.Fatalf("Describe() error = %v", err)
		}
		if desc.Name != "demo" || desc.Version != "9.9" {
			t.Errorf("Describe() = %+v", desc)
		}
		if want := []string{"branches", "inspect"}; strings.Join(desc.Capabilities, ",") != strings.Join(want, ",") {
			t.Errorf("Describe() capabilities = %v, want %v", desc.Capabilities, want)
		}
	})

	t.Run("a protocol version orchard does not speak is refused", func(t *testing.T) {
		path := writePlugin(t, dir, "vcs", "future",
			`echo '{"api_version":99,"name":"future"}'`)
		_, err := New(Found{Role: RoleVCS, Name: "future", Path: path}).Describe()
		if !errors.Is(err, ErrUnsupportedAPI) {
			t.Fatalf("Describe() error = %v, want ErrUnsupportedAPI", err)
		}
		// The numbers matter: this is the message that tells someone which
		// side to upgrade.
		if got := err.Error(); !strings.Contains(got, "99") || !strings.Contains(got, "1") {
			t.Errorf("Describe() error = %q, want both versions named", got)
		}
	})
}

func TestCallSendsTheRequestEnvelope(t *testing.T) {
	dir := t.TempDir()
	path := writePlugin(t, dir, "vcs", "echo", echoing)

	client := New(Found{Role: RoleVCS, Name: "echo", Path: path})
	client.Config = map[string]string{"enabled": "true"}

	var got struct {
		APIVersion int               `json:"api_version"`
		Config     map[string]string `json:"config"`
		Params     map[string]string `json:"params"`
	}
	if err := client.Call("anything", map[string]string{"root": "/repo"}, &got); err != nil {
		t.Fatalf("Call() error = %v", err)
	}

	if got.APIVersion != APIVersion {
		t.Errorf("api_version = %d, want %d", got.APIVersion, APIVersion)
	}
	// The configuration section reaches the plugin verbatim; orchard never
	// looks inside it.
	if got.Config["enabled"] != "true" {
		t.Errorf("config = %v, want it passed through", got.Config)
	}
	if got.Params["root"] != "/repo" {
		t.Errorf("params = %v, want the call's parameters", got.Params)
	}
}

func TestCallErrors(t *testing.T) {
	dir := t.TempDir()

	t.Run("an error reply is quoted", func(t *testing.T) {
		path := writePlugin(t, dir, "vcs", "sad",
			`if [ "$1" = "describe" ]; then echo '{"api_version":1,"name":"sad"}'; else echo '{"error":"no such branch"}'; exit 1; fi`)
		err := New(Found{Role: RoleVCS, Name: "sad", Path: path}).Call("detect", nil, nil)
		if err == nil || !strings.Contains(err.Error(), "no such branch") {
			t.Fatalf("Call() error = %v, want the plugin's own message", err)
		}
	})

	t.Run("a crash without a reply is still an error", func(t *testing.T) {
		path := writePlugin(t, dir, "vcs", "crash", `exit 3`)
		if err := New(Found{Role: RoleVCS, Name: "crash", Path: path}).Call("detect", nil, nil); err == nil {
			t.Fatal("Call() error = nil, want the exit status reported")
		}
	})

	t.Run("a wedged plugin does not wedge orchard", func(t *testing.T) {
		path := writePlugin(t, dir, "vcs", "slow", `sleep 30`)
		client := New(Found{Role: RoleVCS, Name: "slow", Path: path})
		client.Timeout = 100 * time.Millisecond

		done := make(chan error, 1)
		go func() { done <- client.Call("detect", nil, nil) }()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "timed out") {
				t.Errorf("Call() error = %v, want a timeout", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Call() did not return; the timeout is not being enforced")
		}
	})
}

func TestStderrIsLoggedNotParsed(t *testing.T) {
	dir := t.TempDir()
	path := writePlugin(t, dir, "vcs", "chatty",
		`echo "running git" >&2; if [ "$1" = "describe" ]; then echo '{"api_version":1,"name":"chatty"}'; else echo '{}'; fi`)

	var log bytes.Buffer
	client := New(Found{Role: RoleVCS, Name: "chatty", Path: path})
	client.Stderr = &log

	if err := client.Call("detect", nil, nil); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	got := log.String()
	if !strings.Contains(got, "running git") {
		t.Errorf("stderr log = %q, want the plugin's diagnostics", got)
	}
	// Tagged, so it is never mistaken for orchard's own output.
	if !strings.Contains(got, "orchard-vcs-chatty") {
		t.Errorf("stderr log = %q, want it attributed to the plugin", got)
	}
}
