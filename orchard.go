package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type Config struct {
	RootTree string
	PlantDir string
}

func readConfig(filename string) (*Config, error) {
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

func (g cliGitClient) WorktreeExists(rootTree, plantDir, name string) (bool, error) {
	worktrees, err := g.listWorktrees(rootTree)
	if err != nil {
		return false, err
	}
	return worktreeExists(worktrees, plantDir, name), nil
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

	addCmd := &cobra.Command{
		Use:   "add <worktree_name> [<base_branch_or_commit>]",
		Short: "Create a new worktree",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			baseBranch := ""
			if len(args) > 1 {
				baseBranch = args[1]
			}
			return runAdd(cfg, args[0], baseBranch)
		},
	}

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
				return runRemoveInteractive(cfg)
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

	rootCmd.AddCommand(addCmd, removeCmd, rootPathCmd, listCmd)
	return rootCmd
}

func resolveConfig(configPath string) (*Config, error) {
	actualConfigPath := configPath
	if actualConfigPath == "" {
		if _, err := os.Stat("orchard.conf"); err == nil {
			actualConfigPath = "orchard.conf"
		} else {
			home, err := os.UserHomeDir()
			if err == nil {
				fallbackPath := filepath.Join(home, ".config", "orchard", "orchard.conf")
				if _, err := os.Stat(fallbackPath); err == nil {
					actualConfigPath = fallbackPath
				}
			}
		}
	}
	if actualConfigPath == "" {
		actualConfigPath = "orchard.conf" // Fallback to trigger file missing error in readConfig
	}

	cfg, err := readConfig(actualConfigPath)
	if err != nil {
		return nil, fmt.Errorf("path %s: %w", actualConfigPath, err)
	}
	return cfg, nil
}

func runAdd(cfg *Config, worktreeName, baseBranch string) error {
	fmt.Printf("Loaded config: root_tree=%s, plant_dir=%s\n", cfg.RootTree, cfg.PlantDir)

	client := cliGitClient{}
	exists, err := client.WorktreeExists(cfg.RootTree, cfg.PlantDir, worktreeName)
	if err != nil {
		return fmt.Errorf("checking if worktree exists: %w", err)
	}
	if exists {
		return fmt.Errorf("worktree %q already exists", worktreeName)
	}

	if baseBranch == "" {
		branchExists, err := client.BranchExists(cfg.RootTree, worktreeName)
		if err != nil {
			return fmt.Errorf("checking if branch exists: %w", err)
		}
		if branchExists {
			return fmt.Errorf("branch %q already exists", worktreeName)
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

	// 2. Create the new worktree
	newWorktreePath := filepath.Join(cfg.PlantDir, worktreeName)
	fmt.Printf("Creating new worktree at %s...\n", newWorktreePath)

	var gitArgs []string
	gitArgs = append(gitArgs, "worktree", "add", newWorktreePath)

	if baseBranch != "" {
		gitArgs = append(gitArgs, baseBranch)
	} else {
		// Create new branch with the worktree name
		gitArgs = append(gitArgs, "-b", worktreeName)
	}

	if err := runCmd(cfg.RootTree, "git", gitArgs...); err != nil {
		return fmt.Errorf("failed to create worktree: %w", err)
	}

	// 3. Initialize submodules in the new worktree
	fmt.Println("Initializing submodules in the new worktree...")
	if err := runCmd(newWorktreePath, "git", "submodule", "update", "--init", "--recursive"); err != nil {
		return fmt.Errorf("failed to initialize submodules in the new worktree: %w", err)
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

// pickWorktrees presents a toggle pick list of the planted worktrees and
// returns the names left selected. Numbers (several per line allowed) toggle
// entries, 'a' toggles all, an empty line confirms the selection, and 'q' or
// EOF aborts with no names selected.
func pickWorktrees(scanner *bufio.Scanner, out io.Writer, planted []plantedWorktree) ([]string, error) {
	selected := make([]bool, len(planted))
	_, _ = fmt.Fprintln(out, "Select worktrees to remove (numbers toggle, 'a' toggles all, Enter confirms, 'q' aborts):")
	for {
		for i, wt := range planted {
			mark := " "
			if selected[i] {
				mark = "x"
			}
			_, _ = fmt.Fprintf(out, "  %d. [%s] %s\n", i+1, mark, wt.Name)
		}
		_, _ = fmt.Fprint(out, "> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return nil, err
			}
			_, _ = fmt.Fprintln(out)
			return nil, nil
		}
		line := strings.TrimSpace(scanner.Text())
		switch line {
		case "":
			var names []string
			for i, wt := range planted {
				if selected[i] {
					names = append(names, wt.Name)
				}
			}
			return names, nil
		case "q":
			return nil, nil
		case "a":
			all := true
			for _, s := range selected {
				if !s {
					all = false
					break
				}
			}
			for i := range selected {
				selected[i] = !all
			}
			continue
		}

		// Validate the whole line before toggling so a typo doesn't apply
		// half the toggles.
		var toggles []int
		valid := true
		for _, tok := range strings.Fields(line) {
			n, err := strconv.Atoi(tok)
			if err != nil || n < 1 || n > len(planted) {
				_, _ = fmt.Fprintf(out, "invalid selection: %s\n", tok)
				valid = false
				break
			}
			toggles = append(toggles, n-1)
		}
		if !valid {
			continue
		}
		for _, i := range toggles {
			selected[i] = !selected[i]
		}
	}
}

// confirmRemoval asks for a final yes/no before the destructive removal.
// Anything other than an explicit yes (including EOF) declines.
func confirmRemoval(scanner *bufio.Scanner, out io.Writer, names []string) (bool, error) {
	_, _ = fmt.Fprintf(out, "Remove %d worktree(s) (%s)? [y/N] ", len(names), strings.Join(names, ", "))
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		_, _ = fmt.Fprintln(out)
		return false, nil
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes", nil
}

func runRemoveInteractive(cfg *Config) error {
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

	// One scanner shared with the confirmation prompt so no buffered input
	// is lost between the two reads.
	scanner := bufio.NewScanner(os.Stdin)
	names, err := pickWorktrees(scanner, os.Stdout, planted)
	if err != nil {
		return fmt.Errorf("reading selection: %w", err)
	}
	if len(names) == 0 {
		fmt.Println("No worktrees selected.")
		return nil
	}

	confirmed, err := confirmRemoval(scanner, os.Stdout, names)
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

	for _, name := range worktreeNames {
		// 1. Remove the worktree
		worktreePath := filepath.Join(cfg.PlantDir, name)
		fmt.Printf("Removing worktree at %s...\n", worktreePath)
		if err := runCmd(cfg.RootTree, "git", "worktree", "remove", "--force", worktreePath); err != nil {
			return fmt.Errorf("failed to remove worktree %q: %w", name, err)
		}

		// 2. Delete the branch
		fmt.Printf("Deleting branch %s...\n", name)
		if err := runCmd(cfg.RootTree, "git", "branch", "-D", name); err != nil {
			return fmt.Errorf("failed to delete branch %q: %w", name, err)
		}
	}

	fmt.Println("Success!")
	return nil
}
