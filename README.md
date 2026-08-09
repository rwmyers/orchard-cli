# Orchard

Orchard is a small Go CLI tool designed to manage and spawn Git worktrees based on a designated root repository. 

Its primary jobs are:
1. Performing a git update (pull and recursive submodule update) on the root repository to ensure it is up-to-date.
2. Creating a new git worktree pointing to a target "plant" directory.
3. Automatically updating submodules within the newly created worktree.

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
```

## Usage

```bash
orchard [--config <config_path>] <subcommand> [args]
```

### Subcommands

#### add

Creates one or more worktrees. All names are validated before anything is created, so a bad name later in the list does not leave a half-planted batch behind.

```bash
orchard [--config <config_path>] add [--base <branch_or_commit>] <worktree_name>...
```

Each worktree gets a new branch named after it. Without `--base` (or `-b`) the branches start from the root tree's HEAD after it has been pulled; with `--base` they all start from the given branch or commit.

#### remove

Removes one or more worktrees and their associated branches. All names are validated before anything is removed.

```bash
orchard [--config <config_path>] remove [<worktree_name>...]
```

When no names are given, an interactive pick list of the planted worktrees is shown. Arrow keys (or `j`/`k`) move, `space` toggles an entry, `ctrl+a` selects all, `/` filters the list, `enter` confirms the selection, and `esc` or `ctrl+c` aborts. A final yes/no prompt guards the removal.

When stdin is not a terminal — piped input, or a screen reader session — the prompts fall back to a plain numbered list: each line toggles one entry by number and `0` confirms.

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
