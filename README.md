# Orchard

Orchard is a small Go CLI tool designed to manage and spawn worktrees based on a designated root repository.

Its primary jobs are:
1. Updating the root repository to ensure it is up-to-date.
2. Creating a new worktree pointing to a target "plant" directory.
3. Cleaning up worktrees, and whatever else the version control system attaches to them, when they are done with.

Orchard drives **git** and **[jj](https://jj-vcs.dev)** out of the box, and is
not tied to either. Everything that touches a repository goes through a
*driver*, and support for another system is added by writing one, without
changing orchard. A driver can be either a Go package compiled in
([docs/EXTENDING.md](docs/EXTENDING.md)) or an `orchard-vcs-<name>` executable
on `$PATH` speaking JSON ([docs/PLUGINS.md](docs/PLUGINS.md)); orchard treats
the two alike.

Not every system shares git's model, and orchard does not pretend otherwise. jj
workspaces are not tied to bookmarks the way git worktrees are tied to branches,
so with the jj driver orchard creates no branch, asks for none before removing,
and deletes none afterwards — because the driver declines that capability, not
because orchard special-cases jj.

## Installation

To compile and install the `orchard` command globally to your `$GOPATH/bin` (usually `~/go/bin`), run the following command from the `orchard-cli` directory:

```bash
go install
```

Make sure your shell's `PATH` includes `~/go/bin` to run it globally.

## Configuration

Orchard resolves its configuration in the following order of precedence:

1. **Explicit Flag**: Specifying `--config <path>` (or `-c <path>`) on the CLI.
2. **Local Configuration**: An `orchard.conf` file present in the directory where the command is executed.
3. **Global Configuration**: A fallback configuration file located at `~/.config/orchard/orchard.conf`.

### Configuration Format

The configuration file is a simple `key = value` text file. Example:

```ini
# Path to the source/root worktree repository
root_tree = /absolute/path/to/root/repository

# Directory where new worktrees will be created
plant_dir = /absolute/path/to/plant/directory

# The version control driver that manages the root tree
vcs = git
```

`vcs` is optional. Without it, orchard asks each driver the binary was built
with to recognise the root tree and takes the first that does, in alphabetical
order by name. Set it to settle a repository more than one driver claims: a jj
repository colocated with git is claimed by both, and alphabetical order makes
that git unless `vcs = jj` says otherwise. [`orchard setup`](#setup) records whichever
driver it detected, so a configuration it wrote never has to detect again.

To have one written for you, run [`orchard setup`](#setup) in the repository.

## Usage

```bash
orchard [--config <config_path>] <subcommand> [args]
```

### Subcommands

#### setup

Writes a configuration file for a repository.

```bash
orchard setup [<root_tree>] [--plant-dir <path>] [--config <config_path>] [--force] [--prune]
```

The root tree defaults to the repository containing the current directory, so `orchard setup` can be run from anywhere inside a project; pass a directory to configure that repository instead. Either way the path is resolved to the repository root.

By default the two remaining questions are asked interactively: where worktrees should be planted, and where `orchard.conf` should be written. Each is a pick list of suggested paths with an `Other…` entry for anything else, followed by a summary and a final yes/no. Arrow keys (or `j`/`k`) move, `enter` selects, and `esc` or `ctrl+c` backs out without writing anything. As with `remove`, the prompts fall back to a plain numbered list when stdin is not a terminal.

The suggestions cover the layouts that need no further thought — for the plant directory, the one beside the root tree and a `plants` directory next to it; for the configuration, the root tree and the global path — plus the directory the command was run from. Anything that would be rejected, such as a plant directory inside the root tree, is left out.

Running setup again on a repository that is already configured starts by offering to reconfigure it, showing what the current file says; declining leaves it alone. Go ahead and the existing answers lead the pick lists, so keeping one of them is a single `enter`. The lookup covers the root tree, the working directory and the global path, so a configuration written anywhere setup could have put it is recognised.

Moving the plant directory strands anything already planted in the old one: `list` and `remove` stop seeing those worktrees, while their branches carry on reserving the names, so `orchard add` refuses to reuse them. Setup therefore offers to remove them — worktree and branch, the same as `remove` — before the move. Declining is fine and the move still happens; setup then prints what it left and the command that points orchard back at it. Nothing is removed if the final confirmation is cancelled.

Worktrees are only ever cleaned up when the previous configuration named the same `root_tree`, so a configuration belonging to another repository is never acted on. In a non-interactive run nothing is removed without `--prune`; setup prints the same warning instead.

Supplying `--plant-dir` (or `-p`) answers the only question without an obvious default and so skips the prompts entirely, which is the way to run setup from a script. For this subcommand `--config` says where the configuration is written rather than where it is read from; without it the file goes to `<root_tree>/orchard.conf`. An existing file is left alone unless `--force` (or `-f`) is given.

The plant directory is created if it does not exist. Planting inside the root tree is rejected, since it would leave the worktrees sitting untracked in the repository.

#### add

Creates one or more worktrees. All names are validated before anything is created, so a bad name later in the list does not leave a half-planted batch behind.

```bash
orchard [--config <config_path>] add [--base <branch_or_commit>] <worktree_name>...
```

With a driver that ties worktrees to branches — git does, jj does not — each
worktree gets a new branch named after it. Without `--base` (or `-b`) the branches start from the root tree's HEAD after it has been pulled; with `--base` they all start from the given branch or commit. A driver that does not support `--base` refuses the flag rather than ignoring it, and one whose worktrees are not tied to branches simply creates no branch.

#### remove

Removes one or more worktrees and, for a driver that ties worktrees to branches, their associated branches. All names are validated before anything is removed.

```bash
orchard [--config <config_path>] remove [--check] [<worktree_name>...]
```

`--check` refuses to remove any worktree that still holds work — uncommitted
changes, or commits that exist on no remote — and names every offender rather
than stopping at the first. It reports what the driver says, so a driver that
cannot answer the question refuses the flag rather than removing unchecked.

When no names are given, an interactive pick list of the planted worktrees is shown. Arrow keys (or `j`/`k`) move, `space` toggles an entry, `ctrl+a` selects all, `/` filters the list, `enter` confirms the selection, and `esc` or `ctrl+c` aborts. A final yes/no prompt guards the removal.

When stdin is not a terminal — piped input, or a screen reader session — the prompts fall back to a plain numbered list: each line toggles one entry by number and `0` confirms.

#### drivers

Lists the version control drivers this binary was built with, and which parts of
orchard's model each supports.

```bash
orchard drivers
```

```
DRIVER  BRANCHES  UPDATE  BASE  IGNORE  INSPECT  SOURCE
demo    yes       yes     yes   yes     yes      plugin /home/me/bin/orchard-vcs-demo (0.1.0)
git     yes       yes     yes   yes     yes      built-in
jj      no        yes     yes   no      yes      built-in
```

`BRANCHES` says whether the driver ties each worktree to a branch of the same
name, which is what makes `orchard remove` delete one. `BASE` says whether
`orchard add --base` is accepted; it is refused, rather than ignored, by a
driver that cannot honour it. `INSPECT` says whether `orchard remove --check`
is available. `SOURCE` distinguishes a driver compiled into this binary from one
discovered on `$PATH`, and is where a plugin that fails to load shows up.

#### root

Prints the path of the root tree (`root_tree`) resolved from the configuration, making it easy to return to the root of the orchard from anywhere.

```bash
orchard [--config <config_path>] root
```

#### list

Lists the worktrees planted in the plant directory (`plant_dir`), one per line as `name<TAB>path`. The root tree itself is not included.

```bash
orchard [--config <config_path>] list
```

### Examples

- **Set a repository up interactively**:
  Asks where worktrees should be planted and where the configuration should live, then writes it. Run again to change either answer.
  ```bash
  cd ~/code/my-project
  orchard setup
  ```

- **Set a repository up without any prompts**:
  Configures `~/code/my-project` to plant its worktrees in `~/code`, writing `~/code/my-project/orchard.conf`.
  ```bash
  orchard setup ~/code/my-project --plant-dir ~/code
  ```

- **Move the plant directory, clearing up what was planted in the old one**:
  Removes the worktrees left in the previous plant directory, and their branches, before writing the new configuration.
  ```bash
  orchard setup --plant-dir ~/code/plants --force --prune
  ```

- **Write the configuration to the global location**:
  Makes the configuration apply from any directory rather than only from the repository.
  ```bash
  orchard setup --plant-dir ~/code --config ~/.config/orchard/orchard.conf
  ```

- **Create a worktree with a new branch**:
  Creates a new worktree and branch named `my-new-feature` based on the root repository's current HEAD.
  ```bash
  orchard add my-new-feature
  ```

- **Create several worktrees at once**:
  Creates a worktree and branch for each name, all based on the root repository's current HEAD.
  ```bash
  orchard add feat-a feat-b feat-c
  ```

- **Create worktrees based on an existing branch or commit**:
  Creates branches `hotfix-a` and `hotfix-b` starting from `origin/release`, each in its own worktree.
  ```bash
  orchard add hotfix-a hotfix-b --base origin/release
  ```

- **Remove a worktree and its branch**:
  Removes the worktree named `my-feature` and deletes the corresponding branch `my-feature` from the root repository.
  ```bash
  orchard remove my-feature
  ```

- **Remove several worktrees at once**:
  Removes each named worktree and its branch in turn.
  ```bash
  orchard remove my-feature other-feature
  ```

- **Pick worktrees to remove interactively**:
  Shows a filterable pick list of planted worktrees and removes the confirmed selection.
  ```bash
  orchard remove
  ```

- **List planted worktrees**:
  Shows the name and path of each worktree in the plant directory.
  ```bash
  orchard list
  ```

- **Return to the root of the orchard**:
  Changes the current shell directory to the root repository.
  ```bash
  cd "$(orchard root)"
  ```

- **Use a custom configuration file**:
  The `--config` flag may appear anywhere on the command line, before or after the subcommand.
  ```bash
  orchard add my-feature --config /path/to/custom.conf
  ```

## Development

Before submitting changes, run the presubmit checks (formatting, vet, and unit tests):

```bash
make check
```

Linting uses [golangci-lint](https://golangci-lint.run/), which must be installed separately:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```

Then run it with:

```bash
make lint
```
