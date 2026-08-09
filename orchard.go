package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

type Config struct {
	RootTree string
	PlantDir string
}

// configFileName is the file orchard looks for in the working directory, and
// the name `orchard setup` writes.
const configFileName = "orchard.conf"

// globalConfigPath is the fallback configuration location, used when the
// working directory has no orchard.conf. It returns "" when the home directory
// cannot be determined.
func globalConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "orchard", configFileName)
}

// parseConfig reads the key/value pairs from filename. It does not check that
// the directories named still exist, so that `orchard setup` can report what a
// configuration says even once it has gone stale.
func parseConfig(filename string) (*Config, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	cfg := &Config{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid config line: %s", line)
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "root_tree":
			cfg.RootTree = val
		case "plant_dir":
			cfg.PlantDir = val
		default:
			return nil, fmt.Errorf("unknown configuration key: %s", key)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if cfg.RootTree == "" {
		return nil, fmt.Errorf("root_tree is not set in config")
	}
	if cfg.PlantDir == "" {
		return nil, fmt.Errorf("plant_dir is not set in config")
	}

	// Clean paths
	cfg.RootTree = filepath.Clean(cfg.RootTree)
	cfg.PlantDir = filepath.Clean(cfg.PlantDir)

	return cfg, nil
}

func readConfig(filename string) (*Config, error) {
	cfg, err := parseConfig(filename)
	if err != nil {
		return nil, err
	}

	// Verify directories exist
	if _, err := os.Stat(cfg.RootTree); os.IsNotExist(err) {
		return nil, fmt.Errorf("root_tree directory does not exist: %s", cfg.RootTree)
	}
	if _, err := os.Stat(cfg.PlantDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("plant_dir directory does not exist: %s", cfg.PlantDir)
	}

	return cfg, nil
}

type gitWorktree struct {
	Path string
}

type cliGitClient struct{}

func (g cliGitClient) listWorktrees(rootTree string) ([]gitWorktree, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = rootTree
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseWorktrees(output)
}

func (g cliGitClient) BranchExists(rootTree string, branchName string) (bool, error) {
	cmd := exec.Command("git", "show-ref", "--verify", "refs/heads/"+branchName)
	cmd.Dir = rootTree
	err := cmd.Run()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CommitExists reports whether ref resolves to a commit in the root tree. It
// is checked up front so an unusable base is rejected before any worktree is
// created rather than midway through a batch.
func (g cliGitClient) CommitExists(rootTree string, ref string) (bool, error) {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	cmd.Dir = rootTree
	err := cmd.Run()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func parseWorktrees(output []byte) ([]gitWorktree, error) {
	var worktrees []gitWorktree
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "worktree ") {
			wtPath := filepath.Clean(strings.TrimPrefix(line, "worktree "))
			worktrees = append(worktrees, gitWorktree{Path: wtPath})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return worktrees, nil
}

type plantedWorktree struct {
	Name string
	Path string
}

// filterPlantedWorktrees returns the worktrees that orchard manages: direct
// children of plantDir, excluding the root tree itself.
func filterPlantedWorktrees(worktrees []gitWorktree, rootTree, plantDir string) []plantedWorktree {
	var planted []plantedWorktree
	for _, wt := range worktrees {
		if wt.Path == rootTree || filepath.Dir(wt.Path) != plantDir {
			continue
		}
		planted = append(planted, plantedWorktree{Name: filepath.Base(wt.Path), Path: wt.Path})
	}
	return planted
}

func worktreeExists(worktrees []gitWorktree, plantDir, name string) bool {
	targetPath := filepath.Clean(filepath.Join(plantDir, name))
	for _, wt := range worktrees {
		if wt.Path == targetPath {
			return true
		}
	}
	return false
}

func runCmd(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("Running: %s %s (in %s)\n", name, strings.Join(args, " "), dir)
	return cmd.Run()
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configPath string

	rootCmd := &cobra.Command{
		Use:   "orchard",
		Short: "Manage git worktrees based on a designated root repository",
	}
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "path to configuration file")

	// Resolves the config once arg validation has passed. Silences usage so
	// that runtime failures don't dump help text; arg-validation errors,
	// which happen before RunE, still show it.
	loadConfig := func(cmd *cobra.Command) (*Config, error) {
		cmd.SilenceUsage = true
		cfg, err := resolveConfig(configPath)
		if err != nil {
			return nil, fmt.Errorf("reading config: %w", err)
		}
		return cfg, nil
	}

	var baseRef string
	addCmd := &cobra.Command{
		Use:   "add <worktree_name>...",
		Short: "Create one or more worktrees",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			return runAdd(cfg, args, baseRef)
		},
	}
	addCmd.Flags().StringVarP(&baseRef, "base", "b", "", "branch or commit the new worktrees start from (defaults to the root tree's HEAD)")

	removeCmd := &cobra.Command{
		Use:   "remove [<worktree_name>...]",
		Short: "Remove worktrees and their branches (pick list when no names are given)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return runRemoveInteractive(cfg, newHuhPrompter())
			}
			return runRemove(cfg, args)
		},
	}

	rootPathCmd := &cobra.Command{
		Use:   "root",
		Short: "Print the root tree path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			// Print only the path so the output can be used in command
			// substitution, e.g. cd "$(orchard root)".
			fmt.Println(cfg.RootTree)
			return nil
		},
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List worktrees in the plant directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			return runList(cfg)
		},
	}

	var setupOpts setupOptions
	setupCmd := &cobra.Command{
		Use:   "setup [<root_tree>]",
		Short: "Write a configuration file for a repository",
		Long: `Write an ` + configFileName + ` for a repository.

The root tree defaults to the repository containing the current directory; pass
a directory to configure that repository instead. The plant directory and the
location of the configuration file are asked for interactively, and the plant
directory is created if it does not exist.

Supplying --plant-dir answers the only question without an obvious default and
so skips the prompts entirely. For this subcommand --config says where the
configuration is written rather than where it is read from.

Moving the plant directory strands anything already planted in the old one,
since orchard stops listing it while its branches stay put. Setup offers to
remove those worktrees; --prune answers that offer up front.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Nothing here is an arg-validation failure, so runtime errors
			// should not dump the help text.
			cmd.SilenceUsage = true
			if len(args) == 1 {
				setupOpts.RootTree = args[0]
			}
			setupOpts.ConfigPath = configPath
			return runSetup(setupOpts, newHuhPrompter())
		},
	}
	setupCmd.Flags().StringVarP(&setupOpts.PlantDir, "plant-dir", "p", "", "directory worktrees are planted in (skips the prompts)")
	setupCmd.Flags().BoolVarP(&setupOpts.Force, "force", "f", false, "overwrite an existing configuration file")
	setupCmd.Flags().BoolVar(&setupOpts.Prune, "prune", false, "remove worktrees left behind in the previous plant directory")

	rootCmd.AddCommand(setupCmd, addCmd, removeCmd, rootPathCmd, listCmd)
	return rootCmd
}

func resolveConfig(configPath string) (*Config, error) {
	actualConfigPath := configPath
	if actualConfigPath == "" {
		if _, err := os.Stat(configFileName); err == nil {
			actualConfigPath = configFileName
		} else if global := globalConfigPath(); global != "" {
			if _, err := os.Stat(global); err == nil {
				actualConfigPath = global
			}
		}
	}
	if actualConfigPath == "" {
		actualConfigPath = configFileName // Fallback to trigger file missing error in readConfig
	}

	cfg, err := readConfig(actualConfigPath)
	if err != nil {
		return nil, fmt.Errorf("path %s: %w", actualConfigPath, err)
	}
	return cfg, nil
}

// duplicateName returns the first name that appears more than once, or "" when
// every name is unique.
func duplicateName(names []string) string {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			return name
		}
		seen[name] = true
	}
	return ""
}

func runAdd(cfg *Config, worktreeNames []string, baseRef string) error {
	fmt.Printf("Loaded config: root_tree=%s, plant_dir=%s\n", cfg.RootTree, cfg.PlantDir)

	// Validate every name before creating anything, so a bad name later in the
	// list doesn't leave a half-planted batch behind.
	if dup := duplicateName(worktreeNames); dup != "" {
		return fmt.Errorf("worktree %q is named more than once", dup)
	}

	client := cliGitClient{}
	worktrees, err := client.listWorktrees(cfg.RootTree)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}
	for _, name := range worktreeNames {
		if worktreeExists(worktrees, cfg.PlantDir, name) {
			return fmt.Errorf("worktree %q already exists", name)
		}

		branchExists, err := client.BranchExists(cfg.RootTree, name)
		if err != nil {
			return fmt.Errorf("checking if branch exists: %w", err)
		}
		if branchExists {
			return fmt.Errorf("branch %q already exists", name)
		}
	}

	// 1. Git update root_tree
	fmt.Println("Updating root repository...")
	if err := runCmd(cfg.RootTree, "git", "pull"); err != nil {
		return fmt.Errorf("failed to git pull in root_tree: %w", err)
	}

	if err := runCmd(cfg.RootTree, "git", "submodule", "update", "--init", "--recursive"); err != nil {
		return fmt.Errorf("failed to update submodules in root_tree: %w", err)
	}

	// The base is resolved after the pull, since it may be a ref that only
	// arrives with it.
	if baseRef != "" {
		baseExists, err := client.CommitExists(cfg.RootTree, baseRef)
		if err != nil {
			return fmt.Errorf("checking base %q: %w", baseRef, err)
		}
		if !baseExists {
			return fmt.Errorf("base %q is not a branch or commit in the root tree", baseRef)
		}
	}

	// 2. Create a worktree for each name, each on its own new branch so that a
	// shared base can be used for the whole batch and `orchard remove` finds a
	// branch matching the worktree name.
	for _, name := range worktreeNames {
		newWorktreePath := filepath.Join(cfg.PlantDir, name)
		fmt.Printf("Creating new worktree at %s...\n", newWorktreePath)

		gitArgs := []string{"worktree", "add", "-b", name, newWorktreePath}
		if baseRef != "" {
			gitArgs = append(gitArgs, baseRef)
		}

		if err := runCmd(cfg.RootTree, "git", gitArgs...); err != nil {
			return fmt.Errorf("failed to create worktree %q: %w", name, err)
		}

		// 3. Initialize submodules in the new worktree
		fmt.Println("Initializing submodules in the new worktree...")
		if err := runCmd(newWorktreePath, "git", "submodule", "update", "--init", "--recursive"); err != nil {
			return fmt.Errorf("failed to initialize submodules in worktree %q: %w", name, err)
		}
	}

	fmt.Println("Success!")
	return nil
}

func runList(cfg *Config) error {
	client := cliGitClient{}
	worktrees, err := client.listWorktrees(cfg.RootTree)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}

	// Print name and path only, tab-separated, so the output is easy to
	// consume from scripts (e.g. cut, awk).
	for _, wt := range filterPlantedWorktrees(worktrees, cfg.RootTree, cfg.PlantDir) {
		fmt.Printf("%s\t%s\n", wt.Name, wt.Path)
	}
	return nil
}

// prompter collects the interactive input for `orchard remove` when no names
// are given. It is an interface so the removal flow can be tested without
// standing up a terminal.
type prompter interface {
	PickWorktrees(planted []plantedWorktree) ([]string, error)
	ConfirmRemoval(names []string) (bool, error)
}

// maxPickHeight caps how many rows the pick list occupies so that a long list
// scrolls inside the viewport instead of running off the top of the terminal.
const maxPickHeight = 12

// maxConfirmNames caps how many names the confirmation lists. The list does
// not scroll, so without a cap a large selection pushes the question itself
// off the top of the screen.
const maxConfirmNames = 10

func confirmDescription(names []string) string {
	if len(names) <= maxConfirmNames {
		return strings.Join(names, "\n")
	}
	shown := append([]string{}, names[:maxConfirmNames]...)
	shown = append(shown, fmt.Sprintf("... and %d more", len(names)-maxConfirmNames))
	return strings.Join(shown, "\n")
}

// promptField is the part of a huh field this package uses. Both
// *huh.MultiSelect and *huh.Confirm satisfy it.
type promptField interface {
	huh.Field
	RunAccessible(w io.Writer, r io.Reader) error
}

// huhPrompter drives the prompts with huh. On a terminal the fields render as
// a full TUI (arrow keys move, space toggles, ctrl+a selects all, / filters,
// enter confirms). When stdin is not a terminal it falls back to huh's
// accessible line-based mode so piped input still works.
type huhPrompter struct {
	in       io.Reader
	out      io.Writer
	terminal bool
}

func newHuhPrompter() huhPrompter {
	return huhPrompter{in: os.Stdin, out: os.Stdout, terminal: term.IsTerminal(os.Stdin.Fd())}
}

// byteReader hands out at most one byte per Read. Accessible mode builds a
// fresh bufio.Scanner for every prompt it issues, and a buffered reader would
// read past the newline and swallow the input meant for the next prompt.
// Reading a byte at a time keeps each scanner from consuming more than its own
// line; the volume of interactive input makes the extra reads irrelevant.
//
// Once the underlying reader is exhausted it yields one final newline before
// reporting EOF. huh's accessible prompts re-read after rejecting a line but
// hold on to the rejected text, so input ending straight after an invalid
// answer would be answered with something the field cannot parse — in a select
// that means indexing its options with -1. The trailing newline makes the end
// of input mean "take the default" instead.
type byteReader struct {
	r    io.Reader
	done bool
}

func (b *byteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n, err := b.r.Read(p[:1])
	if n == 0 && errors.Is(err, io.EOF) && !b.done {
		b.done = true
		p[0] = '\n'
		return 1, nil
	}
	return n, err
}

func (p huhPrompter) run(field promptField) error {
	if !p.terminal {
		return field.RunAccessible(p.out, &byteReader{r: p.in})
	}
	// The field's own Run() builds a form with the help footer switched off,
	// which would leave the keybindings undiscoverable, so build it here.
	return huh.NewForm(huh.NewGroup(field)).WithShowHelp(true).Run()
}

// PickWorktrees shows the planted worktrees as a multi-select and returns the
// names left selected. Aborting (ctrl+c or esc) returns no names rather than
// an error, since backing out is a normal outcome and not a failure.
func (p huhPrompter) PickWorktrees(planted []plantedWorktree) ([]string, error) {
	options := make([]huh.Option[string], len(planted))
	for i, wt := range planted {
		options[i] = huh.NewOption(wt.Name, wt.Name)
	}

	var names []string
	field := huh.NewMultiSelect[string]().
		Title("Select worktrees to remove").
		Options(options...).
		Filterable(true).
		Height(min(len(planted)+2, maxPickHeight)).
		Value(&names)

	if err := p.run(field); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil, nil
		}
		return nil, err
	}
	return names, nil
}

// confirm asks a yes/no question. Anything other than an explicit yes —
// including aborting — declines, so backing out never triggers the action being
// confirmed.
func (p huhPrompter) confirm(title, description, affirmative, negative string) (bool, error) {
	var confirmed bool
	field := huh.NewConfirm().
		Title(title).
		Description(description).
		Affirmative(affirmative).
		Negative(negative).
		Value(&confirmed)

	if err := p.run(field); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, nil
		}
		return false, err
	}
	return confirmed, nil
}

// ConfirmRemoval asks for a final yes/no before the destructive removal.
func (p huhPrompter) ConfirmRemoval(names []string) (bool, error) {
	return p.confirm(fmt.Sprintf("Remove %d worktree(s) and their branches?", len(names)),
		confirmDescription(names), "Remove", "Cancel")
}

func runRemoveInteractive(cfg *Config, p prompter) error {
	client := cliGitClient{}
	worktrees, err := client.listWorktrees(cfg.RootTree)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}
	planted := filterPlantedWorktrees(worktrees, cfg.RootTree, cfg.PlantDir)
	if len(planted) == 0 {
		fmt.Println("No worktrees to remove.")
		return nil
	}

	names, err := p.PickWorktrees(planted)
	if err != nil {
		return fmt.Errorf("reading selection: %w", err)
	}
	if len(names) == 0 {
		fmt.Println("No worktrees selected.")
		return nil
	}

	confirmed, err := p.ConfirmRemoval(names)
	if err != nil {
		return fmt.Errorf("reading confirmation: %w", err)
	}
	if !confirmed {
		fmt.Println("Aborted.")
		return nil
	}
	return runRemove(cfg, names)
}

func runRemove(cfg *Config, worktreeNames []string) error {
	fmt.Printf("Loaded config: root_tree=%s, plant_dir=%s\n", cfg.RootTree, cfg.PlantDir)

	// Validate every name before removing anything, so a bad name later in
	// the list doesn't leave the removal half-done.
	client := cliGitClient{}
	worktrees, err := client.listWorktrees(cfg.RootTree)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}
	for _, name := range worktreeNames {
		if !worktreeExists(worktrees, cfg.PlantDir, name) {
			return fmt.Errorf("worktree %q does not exist", name)
		}

		branchExists, err := client.BranchExists(cfg.RootTree, name)
		if err != nil {
			return fmt.Errorf("checking if branch exists: %w", err)
		}
		if !branchExists {
			return fmt.Errorf("branch %q does not exist", name)
		}
	}

	if err := removeWorktrees(cfg.RootTree, cfg.PlantDir, worktreeNames); err != nil {
		return err
	}

	fmt.Println("Success!")
	return nil
}

// removeWorktrees removes each named worktree from plantDir and deletes the
// branch of the same name. The branch is only deleted if it is still there:
// `orchard remove` has already insisted on it, but `orchard setup` clears up
// worktrees it did not necessarily create, and a missing branch is no reason
// to leave one of those behind.
func removeWorktrees(rootTree, plantDir string, names []string) error {
	client := cliGitClient{}
	for _, name := range names {
		// 1. Remove the worktree
		worktreePath := filepath.Join(plantDir, name)
		fmt.Printf("Removing worktree at %s...\n", worktreePath)
		if err := runCmd(rootTree, "git", "worktree", "remove", "--force", worktreePath); err != nil {
			return fmt.Errorf("failed to remove worktree %q: %w", name, err)
		}

		// 2. Delete the branch
		branchExists, err := client.BranchExists(rootTree, name)
		if err != nil {
			return fmt.Errorf("checking if branch exists: %w", err)
		}
		if !branchExists {
			continue
		}
		fmt.Printf("Deleting branch %s...\n", name)
		if err := runCmd(rootTree, "git", "branch", "-D", name); err != nil {
			return fmt.Errorf("failed to delete branch %q: %w", name, err)
		}
	}
	return nil
}
