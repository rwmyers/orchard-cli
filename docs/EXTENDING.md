# Writing a version control driver

Orchard does not know about git. It knows about *drivers*: Go implementations of
[`vcs.Driver`](../vcs/vcs.go) that it resolves for a root tree and then asks to
do things. It ships with two — [`vcs/git`](../vcs/git) and
[`vcs/jj`](../vcs/jj) — and neither has any privilege the one you write will not
have.

Adding support for another system means writing a package of your own. Nothing
in orchard changes.

## Two ways to write one

`vcs.Driver` is the contract. There are two ways to satisfy it, and they meet at
the same registry — nothing downstream can tell which a given driver came from.

| | **Go driver** (this document) | **Plugin** ([docs/PLUGINS.md](PLUGINS.md)) |
| --- | --- | --- |
| What you write | a Go package | an executable named `orchard-vcs-<name>` on `$PATH` |
| Installed by | rebuilding orchard with your driver imported | dropping a file on `$PATH` |
| Contract checked by | the compiler | you, against the protocol |
| Language | Go | any — the worked example is Python |
| Cost per operation | a function call | a process |

**Write a Go driver unless you have a reason not to.** It cannot get the
protocol wrong, it needs no process per call, and a missing method is a compile
error rather than a capability that quietly disappears. The reasons not to are
real, though — another language, no rebuild, no binary to distribute — and when
they apply, the plugin protocol is there and is a first-class citizen rather
than a lesser path. The plugin adapter is itself an ordinary `vcs.Driver`.

## The shape of it

A driver registers itself from `init`:

```go
package jj

import "github.com/rwmyers/orchard-cli/vcs"

func init() { vcs.Register(Driver{}) }

type Driver struct{}

func (Driver) Name() string { return "jj" }
// ...
```

and a binary that wants it imports it for effect alongside orchard's command
line:

```go
package main

import (
	"github.com/rwmyers/orchard-cli/cli"

	_ "github.com/rwmyers/orchard-cli/vcs/git"
	_ "github.com/example/orchard-jj"
)

func main() { cli.Main() }
```

`go install .` and you have an orchard that drives both. This is the same
arrangement `database/sql` uses for its drivers, and it costs you one file: the
driver is compiled in, so it is type-checked against the interface, works on
every platform orchard does, and carries no version-matching constraints of its
own.

`orchard drivers` prints what a binary ended up with:

```
DRIVER  BRANCHES  UPDATE  BASE  IGNORE  INSPECT  SOURCE
git     yes       yes     yes   yes     yes      built-in
jj      no        yes     yes   no      no       built-in
```

[`examples/custom-binary`](../examples/custom-binary) is that arrangement as a
working module: a separate `go.mod`, importing nothing but orchard's public
packages. It is what proves a driver really can be written without touching
orchard — if the public interface were ever insufficient, that module would stop
compiling.

## What you must implement

`vcs.Driver` is the whole of it — five methods, and only what every system
orchard can drive must be able to do:

| Method | What orchard uses it for |
| --- | --- |
| `Name()` | The `vcs` configuration key, `orchard drivers`, error messages. |
| `Detect(dir)` | Finding the root tree, and deciding which driver owns it. Return `vcs.ErrNotRepository` when the directory is not yours. |
| `ListWorktrees(root)` | `orchard list`, and checking a name is free before `add` and taken before `remove`. Return everything, root tree included; orchard picks out what it manages by path. |
| `AddWorktree(req)` | `orchard add`. |
| `RemoveWorktree(req)` | `orchard remove`. Leave nothing at `req.Path`, the directory included. |

The [`vcs`](../vcs) package has helpers for the usual way of doing this —
`vcs.Run` for commands that change something, announcing them as they go;
`vcs.Output` for commands you parse; `vcs.Succeeds` for the exit-status
questions.

## What you may implement

The rest of orchard's model is optional, and you opt into each part by
implementing its interface. Orchard **skips** what you leave out rather than
assuming it — which is the point of the whole exercise, because orchard's model
came from git and not every system shares it.

| Interface | Methods | What implementing it turns on |
| --- | --- | --- |
| `vcs.Brancher` | `BranchExists`, `DeleteBranch` | Worktrees are tied to a branch of the same name. Orchard refuses to plant one whose branch name is taken, insists the branch is there before removing, and deletes it afterwards. Leave it out and all three steps are skipped, and the confirmation prompt stops promising to delete branches. |
| `vcs.Updater` | `UpdateRoot` | Orchard refreshes the root tree once before planting a batch. Leave it out and `orchard add` goes straight to planting. |
| `vcs.BaseResolver` | `BaseExists` | `orchard add --base` works, and the base is checked before anything is created. Leave it out and `--base` is **refused with an error** rather than quietly ignored. |
| `vcs.Ignorer` | `Ignores` | `orchard setup` can suggest ignoring the configuration file it wrote inside a repository. Leave it out and the hint is skipped. |
| `vcs.Inspector` | `Inspect` | `orchard remove --check` works. This is also how anything that reclaims worktrees on its own initiative — the harness reclaimer, deciding whether a worktree whose conversation has finished may be taken back — asks whether one still holds work. Leave it out and orchard will only remove worktrees when a person asks. |

`vcs.CapabilitiesOf` reports what a driver opted into. It is derived from the
driver by type assertion rather than declared by it, so the two cannot drift
apart.

### `Inspector` and reclaiming worktrees

`Inspect` deserves particular care, because it is the one method whose answer
lets orchard destroy something without being asked. Both fields of
`vcs.WorktreeState` should be **conservative**: report `true` for anything you
cannot determine. The cost of a needless refusal is an argument; the cost of a
wrong go-ahead is lost work. Orchard already treats a failed `Inspect` as
"holds work".

Nothing in orchard may answer these questions by running `git` directly. A
repository driven by a plugin has to be answerable through the same interface,
or the abstraction is a fiction the moment anything wants to reclaim a worktree.

### Dynamic capabilities

A driver whose capabilities are not known until runtime — the plugin adapter,
which learns them from `describe` — implements `vcs.Capable` to declare them.
`CapabilitiesOf` then takes what is **both declared and implemented**, so such a
driver can narrow what it offers but never claim more than it can do.

A driver written in Go should not implement `vcs.Capable`. Opting in by
implementing an interface leaves nothing to keep in step.

### Why this is not a fat interface with feature flags

The obvious alternative — one big interface, with a `Capabilities` struct the
driver fills in — makes a driver declare `Branches: false` and then still write
`BranchExists` returning `(false, nil)` for a system that has no such concept.
Two things to keep in step, and a pile of methods that must not be called.
Opting in by implementing an interface is Go's own idiom for this (`io.ReaderFrom`,
`http.Flusher`), and there is nothing to keep in step.

## The jj driver, and why it is not git with different words

[`vcs/jj`](../vcs/jj) drives [Jujutsu](https://jj-vcs.dev) and ships built in. It
is the best thing to read after this document, because jj differs from git in
exactly the places the optional interfaces exist to absorb:

- **jj workspaces are not tied to bookmarks.** Git's `worktree add -b <name>`
  creates a branch; jj's `workspace add` creates nothing of the sort. So the jj
  driver does not implement `vcs.Brancher`, and orchard stops asking about
  branches entirely — no special case anywhere in orchard.
- **`jj workspace forget` leaves the directory behind.** Git's `worktree remove`
  deletes it. The jj driver's `RemoveWorktree` calls `forget` and then
  `os.RemoveAll`. Where the line falls between the tool and the driver is the
  driver's business; orchard only requires that nothing is left at the path.
- **jj has no cheap `check-ignore` equivalent**, so `vcs.Ignorer` is left out and
  `orchard setup` skips a hint.

It does implement `vcs.Updater`, `vcs.BaseResolver` and `vcs.Inspector`. Its
`UpdateRoot` is worth a look: `jj git fetch` treats having no remote as an
error, and a jj repository without one is entirely normal, so the driver checks
for remotes first rather than failing every `orchard add` in such a repository.
Absorbing that kind of difference is the driver's job, not orchard's.

> **Version sensitivity:** verified against jj 0.44.0. jj's command line is
> still moving; `listTemplate` (which depends on the `WorkspaceRef` template
> type, whose keywords are bare — `name`, not `name()`) and the revsets in
> `Inspect` are the parts most likely to need revisiting for another release.
> Both are named constants for that reason, and `vcs/jj` has an integration test
> that runs against a real jj when one is installed and skips when it is not.

## Detection, and repositories two drivers claim

Without a `vcs` key in `orchard.conf`, orchard asks each registered driver to
recognise the root tree, in alphabetical order by name, and takes the first that
does. A driver that recognises the directory but cannot work with it should
return a descriptive error instead of `ErrNotRepository`; that stops the search,
so its complaint reaches the user rather than a blanket "no driver recognises
this". The git driver does this for bare repositories.

A jj repository colocated with git is claimed by both. Alphabetical order makes
that resolve to git, deterministically rather than by import order — and

```ini
vcs = jj
```

in `orchard.conf` settles it the other way. `orchard setup` records whichever
driver it detected, so a configuration written by setup never has to detect
again.

## Testing a driver

Drivers are ordinary Go packages, so test them as such. Two things worth doing:

- Assert the optional interfaces you meant to implement, and only those:

  ```go
  var (
      _ vcs.Driver  = Driver{}
      _ vcs.Updater = Driver{}
  )
  ```

  A missing method makes this a compile error rather than a capability that
  quietly disappears.

- Use `vcs.SetOutput` to capture what `vcs.Run` prints instead of letting it
  reach the terminal.
