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

1. **Explicit Flag**: Specifying `-config <path>` on the CLI.
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
orchard [-config <config_path>] <worktree_name> [<base_branch_or_commit>]
```

### Examples

- **Create a worktree with a new branch**:
  Creates a new worktree and branch named `my-new-feature` based on the root repository's current HEAD.
  ```bash
  orchard my-new-feature
  ```

- **Create a worktree based on an existing branch**:
  Creates a new worktree checking out the existing `main` branch.
  ```bash
  orchard my-main-worktree main
  ```

- **Use a custom configuration file**:
  ```bash
  orchard -config /path/to/custom.conf my-feature
  ```
