# The VCS plugin protocol

A version control driver can be an **executable** rather than a Go package
compiled into orchard. Orchard runs it once per operation, with the verb as its
only argument and a JSON request on stdin, and reads a JSON result from stdout.

This is the same mechanism `harness` plugins use — one `internal/plugin`
package, parameterised by role — so everything here about discovery, versioning,
timeouts and failure applies to both. Only the verb set below is specific to
`vcs`.

> **Which to write.** A driver in [Go](EXTENDING.md) is checked against the
> interface by the compiler and cannot get the protocol wrong; write one of
> those unless you have a reason not to. Write a plugin when the driver should
> be in another language, installed by dropping a file on `$PATH`, or
> distributed without shipping a binary.

## Discovery

An executable named `orchard-vcs-<name>` anywhere on `$PATH`. So
`orchard-vcs-jj` is the driver named `jj`, selected by `vcs = jj` in
`orchard.conf`. Anything matching the pattern is loadable; there is no
allowlist.

Where two directories hold the same plugin the earlier one on `$PATH` wins, as
a shell would resolve it. A driver compiled into the binary beats a plugin of
the same name, so installing an experimental `orchard-vcs-git` cannot silently
displace the built-in one.

Discovery reads directory entries only — nothing is executed until a plugin is
actually used, so `orchard --help` runs no plugins.

## Calling convention

```
orchard-vcs-<name> <verb>   < request.json   > result.json
```

**Request** on stdin:

```json
{
  "api_version": 1,
  "config": {"enabled": "true"},
  "params": {"root": "/home/me/src/project"}
}
```

`config` is the plugin's own `[vcs.<name>]` section from `orchard.conf`, passed
through verbatim. Orchard does not look inside it — validating it would mean
knowing about every plugin that will ever exist.

**Success**: exit 0, with the result object on stdout.

**Failure**: non-zero exit, with `{"error": "..."}` on stdout. Orchard quotes
the message, so it is worth writing for the person who will read it. An error
reply is believed even on a zero exit.

**stderr** is logged by orchard, tagged with the plugin's name, and never
parsed. Put progress and diagnostics there.

**Timeouts** are orchard's. `describe` gets 10s and everything else 10 minutes.
A plugin that is killed gets a short grace period for anything it spawned to
release the output pipe, then orchard stops waiting — a wedged plugin must never
wedge orchard.

## describe

Mandatory, and the first verb called. Nothing else runs until it succeeds, so a
plugin speaking the wrong protocol fails with that complaint rather than with
whatever its other verbs happen to do.

```json
{"api_version": 1, "name": "jj", "version": "0.3.1", "capabilities": ["update", "base"]}
```

Orchard refuses to load a plugin whose `api_version` it does not implement. The
current version is **1**.

`capabilities` is how a plugin opts into the optional parts of orchard's model —
the equivalent of a Go driver implementing an interface. Orchard calls only the
verbs that follow from it. A capability orchard does not recognise is ignored,
so a plugin written against a later orchard still loads with the parts this one
understands.

| Capability | Verbs it enables | What orchard does with it |
| --- | --- | --- |
| *(none)* | `detect`, `list-worktrees`, `add-worktree`, `remove-worktree` | Always required. |
| `branches` | `branch-exists`, `delete-branch` | Worktrees are tied to a branch of the same name: the name must be free to plant, present to remove, and is deleted afterwards. Omit it for a system like jj whose workspaces are not tied to a named ref, and orchard skips all three. |
| `update` | `update-root` | The root tree is refreshed before a batch is planted. |
| `base` | `base-exists` | `orchard add --base` is accepted. Omit it and the flag is **refused with an error** rather than ignored. |
| `ignore` | `ignores` | `orchard setup` can suggest ignoring the config file it wrote. |
| `inspect` | `inspect` | `orchard remove --check` works, and anything that reclaims worktrees on its own initiative can ask whether one still holds work. |

## Verbs

Every verb mirrors a method on orchard's `vcs.Driver` interface, so the protocol
has no shape of its own to learn.

### `detect` — always required

```json
→ {"dir": "/home/me/src/project/sub"}
← {"root": "/home/me/src/project"}
← {"not_repository": true}
```

Declining is the normal outcome for every plugin but one, so it is a **result**,
not an error. Reserve the error reply for a directory you recognise but cannot
work with — orchard stops searching and shows your message, which is how "this
is a bare repository" reaches the user instead of a blanket "no driver
recognises this".

### `list-worktrees` — always required

```json
→ {"root": "/home/me/src/project"}
← {"worktrees": [{"name": "feat-a", "path": "/home/me/src/plants/feat-a"}]}
```

Return **everything**, the root tree included; orchard picks out what it manages
by path. Extras are harmless, omissions are not.

### `add-worktree` — always required

```json
→ {"root": "...", "name": "feat-a", "path": "/home/me/src/plants/feat-a", "base": "main"}
← {}
```

`base` is present only if you declared `base`. The parent of `path` exists;
`path` itself does not. Orchard has already checked nothing is planted there
and, if you declared `branches`, that the branch name is free.

### `remove-worktree` — always required

```json
→ {"root": "...", "name": "feat-a", "path": "/home/me/src/plants/feat-a"}
← {}
```

Leave **nothing** at `path`, the directory included. Where the line falls
between your tool and your plugin is your business: `jj workspace forget`
deliberately leaves the directory behind, so a jj plugin removes it itself.
Branches are dealt with separately through `delete-branch`.

### `branch-exists`, `delete-branch` — capability `branches`

```json
→ {"root": "...", "name": "feat-a"}
← {"exists": true}          ← {}
```

### `update-root` — capability `update`

```json
→ {"root": "..."}   ← {}
```

### `base-exists` — capability `base`

```json
→ {"root": "...", "base": "origin/release"}
← {"exists": true}
```

Checked once, after any update and before anything is created, so an unusable
base is reported before a batch is half planted.

### `ignores` — capability `ignore`

```json
→ {"root": "...", "path": "/home/me/src/project/orchard.conf"}
← {"ignored": false}
```

### `inspect` — capability `inspect`

```json
→ {"root": "...", "worktree": {"name": "feat-a", "path": "/home/me/src/plants/feat-a"}}
← {"dirty": false, "unpublished": true}
```

What removing this worktree right now would destroy. `dirty` is uncommitted
changes; `unpublished` is commits that exist only here — not on any remote, so
removing the worktree would be the only copy gone.

**Be conservative.** Anything you cannot determine should be reported as `true`.
This answer decides whether orchard removes a worktree on its own initiative;
the cost of a needless refusal is an argument, the cost of a wrong go-ahead is
lost work. Orchard already treats a failed `inspect` as "holds work".

## A worked example

[`examples/orchard-vcs-demo`](../examples/orchard-vcs-demo) is a complete plugin
in Python, backed by git, implementing every verb. It can be run against a real
repository and compared with the built-in git driver:

```bash
cp examples/orchard-vcs-demo/orchard-vcs-demo ~/bin/
orchard drivers
```

```
DRIVER  BRANCHES  UPDATE  BASE  IGNORE  INSPECT  SOURCE
demo    yes       yes     yes   yes     yes      plugin /home/me/bin/orchard-vcs-demo (0.1.0)
git     yes       yes     yes   yes     yes      built-in
```

`orchard drivers` is where a misbehaving plugin shows up: a plugin that fails
`describe` is listed with no capabilities, `[failed]` against its source, and
the reason printed underneath.
