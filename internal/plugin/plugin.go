// Package plugin is the shared plumbing for orchard's external plugins,
// whatever role they serve.
//
// Orchard grows plugins along more than one axis: `vcs` plugins teach it a
// version control system, `harness` plugins teach it about a conversation tool.
// The mechanics are the same in both cases — find an executable, agree on a
// protocol version, run it with JSON on stdin, read JSON off stdout, do not let
// a wedged plugin wedge orchard — so they are the same code, with the role as a
// parameter. Two subtly different plugin protocols in one small CLI would be a
// permanent tax on everyone who writes one.
//
// # Discovery
//
// A plugin is an executable named orchard-<role>-<name> somewhere on $PATH, the
// convention git, kubectl and docker all use. Anything matching is loadable;
// there is no allowlist.
//
// # Protocol
//
// Orchard runs the executable once per call, with the verb as its only
// argument, and writes a JSON request object on stdin:
//
//	{"api_version": 1, "config": {"enabled": "true"}, "params": {...}}
//
// The plugin replies on stdout and exits:
//
//   - exit 0, with the result object on stdout
//   - non-zero exit, with {"error": "..."} on stdout
//
// Anything on stderr is logged by orchard and never parsed, so a plugin is free
// to write progress or debugging there. Orchard imposes the timeout.
//
// Every plugin must implement the describe verb, whatever its role, and it is
// the first thing called. It reports the protocol version the plugin speaks —
// orchard refuses to load one it does not implement — along with the plugin's
// own name and version and the list of capabilities that says which of the
// role's remaining verbs it answers.
package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// APIVersion is the protocol version this orchard speaks. A plugin reporting
// anything else from describe is refused rather than guessed at.
const APIVersion = 1

// Role is the axis a plugin extends orchard along. It appears in the executable
// name and in the configuration section header, so the two always agree.
type Role string

const (
	// RoleVCS is a version control driver: orchard-vcs-<name>, configured by
	// a [vcs.<name>] section.
	RoleVCS Role = "vcs"
	// RoleHarness is a conversation harness observer: orchard-harness-<name>,
	// configured by a [harness.<name>] section.
	RoleHarness Role = "harness"
)

// DescribeVerb is the one verb every plugin must answer, in every role.
const DescribeVerb = "describe"

// Timeouts. Describe is called on every discovered plugin, so it is held to a
// short one; the rest may be fetching over a network.
const (
	DescribeTimeout = 10 * time.Second
	DefaultTimeout  = 10 * time.Minute
	// orphanGrace is how long a killed plugin's descendants are given to
	// release its output pipe before orchard stops waiting on them.
	orphanGrace = 2 * time.Second
)

// ErrUnsupportedAPI is returned when a plugin speaks a protocol version this
// orchard does not.
var ErrUnsupportedAPI = errors.New("unsupported plugin api version")

// Found is a discovered executable that has not been run yet. Discovery reads
// directory entries only, so finding plugins costs nothing until one is used.
type Found struct {
	Role Role
	// Name is the part of the filename after the role, so orchard-vcs-jj is
	// "jj" and orchard-harness-claude-code is "claude-code".
	Name string
	Path string
}

// Discover returns the plugins for a role that are on $PATH, ordered by name.
// Where two directories hold the same plugin the earlier one on $PATH wins,
// matching how a shell would resolve it.
func Discover(role Role) []Found {
	prefix := "orchard-" + string(role) + "-"

	seen := map[string]Found{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name, ok := strings.CutPrefix(entry.Name(), prefix)
			if !ok || name == "" {
				continue
			}
			if _, taken := seen[name]; taken {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if !executable(path) {
				continue
			}
			seen[name] = Found{Role: role, Name: name, Path: path}
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)

	found := make([]Found, 0, len(names))
	for _, name := range names {
		found = append(found, seen[name])
	}
	return found
}

// executable reports whether path is a regular file anyone may run. A directory
// or a stray data file matching the naming pattern is skipped rather than
// treated as a broken plugin.
func executable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// Description is the reply to describe, and is the same shape for every role.
// What Capabilities may contain is the role's business, not this package's.
type Description struct {
	APIVersion   int      `json:"api_version"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

// Client runs one plugin. A call is a fresh process, so a Client holds no
// connection and is cheap to keep around.
type Client struct {
	Role Role
	// Name is the name discovery derived from the filename. It is what
	// orchard calls the plugin even if describe reports something else,
	// since that is the name the configuration section uses.
	Name string
	Path string
	// Config is the plugin's own configuration section, passed through
	// verbatim on every call. Orchard does not look inside it — validating it
	// would mean knowing about every plugin that will ever exist.
	Config map[string]string
	// Timeout bounds a single call. Zero means DefaultTimeout.
	Timeout time.Duration
	// Stderr receives whatever the plugin writes there, prefixed with its
	// name. Nil discards it.
	Stderr io.Writer
}

// New builds a client for a discovered plugin.
func New(found Found) *Client {
	return &Client{Role: found.Role, Name: found.Name, Path: found.Path}
}

// String names the plugin the way error messages should.
func (c *Client) String() string {
	return "orchard-" + string(c.Role) + "-" + c.Name
}

// request is what goes in on stdin.
type request struct {
	APIVersion int               `json:"api_version"`
	Config     map[string]string `json:"config,omitempty"`
	Params     any               `json:"params,omitempty"`
}

// errorReply is what a failing plugin puts on stdout. Every reply is checked
// for it, so a plugin that reports an error but exits zero is still believed.
type errorReply struct {
	Error string `json:"error"`
}

// Describe runs the mandatory describe verb and checks the protocol version.
// Everything else about a plugin follows from what this returns, so a plugin
// that fails here is not used at all.
func (c *Client) Describe() (*Description, error) {
	var desc Description
	if err := c.call(DescribeTimeout, DescribeVerb, nil, &desc); err != nil {
		return nil, err
	}
	if desc.APIVersion != APIVersion {
		return nil, fmt.Errorf("%s: %w: plugin speaks %d, orchard speaks %d",
			c, ErrUnsupportedAPI, desc.APIVersion, APIVersion)
	}
	return &desc, nil
}

// Call runs a verb. params is marshalled into the request, and the result
// object is unmarshalled into result, which may be nil for a verb that returns
// nothing worth reading.
func (c *Client) Call(verb string, params, result any) error {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return c.call(timeout, verb, params, result)
}

func (c *Client) call(timeout time.Duration, verb string, params, result any) error {
	body, err := json.Marshal(request{APIVersion: APIVersion, Config: c.Config, Params: params})
	if err != nil {
		return fmt.Errorf("%s %s: encoding request: %w", c, verb, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, c.Path, verb)
	cmd.Stdin = bytes.NewReader(body)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Killing the plugin on timeout is not enough on its own. Anything it
	// spawned inherits the output pipe and can hold it open after its parent
	// is gone, and Wait does not return until that pipe closes — so without a
	// WaitDelay a plugin that leaves a child behind wedges orchard anyway,
	// which is the one thing the timeout exists to prevent. After the delay
	// the pipes are closed regardless and whatever has been read so far is
	// what we get.
	cmd.WaitDelay = orphanGrace
	runErr := cmd.Run()

	c.logStderr(verb, stderr.Bytes())

	// A plugin that says what went wrong is quoted rather than reduced to its
	// exit status, whether or not it also exited non-zero.
	if reply := decodeError(stdout.Bytes()); reply != "" {
		return fmt.Errorf("%s %s: %s", c, verb, reply)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%s %s: timed out after %s", c, verb, timeout)
	}
	if runErr != nil {
		return fmt.Errorf("%s %s: %w", c, verb, runErr)
	}

	if result == nil {
		return nil
	}
	// A verb with nothing to say may legitimately print nothing at all.
	if len(bytes.TrimSpace(stdout.Bytes())) == 0 {
		return nil
	}
	if err := json.Unmarshal(stdout.Bytes(), result); err != nil {
		return fmt.Errorf("%s %s: decoding reply: %w", c, verb, err)
	}
	return nil
}

// decodeError returns the message from an {"error": "..."} reply, or "" when
// the output is not one.
func decodeError(out []byte) string {
	if len(bytes.TrimSpace(out)) == 0 {
		return ""
	}
	var reply errorReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return ""
	}
	return reply.Error
}

// logStderr forwards a plugin's diagnostics, tagged so it is obvious they are
// not orchard's own.
func (c *Client) logStderr(verb string, out []byte) {
	if c.Stderr == nil || len(bytes.TrimSpace(out)) == 0 {
		return
	}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		_, _ = fmt.Fprintf(c.Stderr, "%s %s: %s\n", c, verb, line)
	}
}
