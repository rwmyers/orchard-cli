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

Creates a new worktree.

```bash
orchard [--config <config_path>] add <worktree_name> [<base_branch_or_commit>]
```

#### remove

Removes a worktree and its associated branch.

```bash
orchard [--config <config_path>] remove <worktree_name>
```

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

- **Create a worktree based on an existing branch**:
  Creates a new worktree checking out the existing `main` branch.
  ```bash
  orchard add my-main-worktree main
  ```

- **Remove a worktree and its branch**:
  Removes the worktree named `my-feature` and deletes the corresponding branch `my-feature` from the root repository.
  ```bash
  orchard remove my-feature
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
