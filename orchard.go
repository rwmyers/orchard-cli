package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	defer file.Close()

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

func runCmd(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("Running: %s %s (in %s)\n", name, strings.Join(args, " "), dir)
	return cmd.Run()
}

func main() {
	configPath := flag.String("config", "", "Path to configuration file")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Println("Usage: orchard [-config <config_path>] <worktree_name> [<base_branch_or_commit>]")
		os.Exit(1)
	}

	worktreeName := args[0]
	var baseBranch string
	if len(args) > 1 {
		baseBranch = args[1]
	}

	actualConfigPath := *configPath
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
		fmt.Fprintf(os.Stderr, "Error reading config (path: %s): %v\n", actualConfigPath, err)
		os.Exit(1)
	}

	fmt.Printf("Loaded config: root_tree=%s, plant_dir=%s\n", cfg.RootTree, cfg.PlantDir)

	// 1. Git update root_tree
	fmt.Println("Updating root repository...")
	if err := runCmd(cfg.RootTree, "git", "pull"); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to git pull in root_tree: %v\n", err)
		os.Exit(1)
	}

	if err := runCmd(cfg.RootTree, "git", "submodule", "update", "--init", "--recursive"); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to update submodules in root_tree: %v\n", err)
		os.Exit(1)
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
		fmt.Fprintf(os.Stderr, "Failed to create worktree: %v\n", err)
		os.Exit(1)
	}

	// 3. Initialize submodules in the new worktree
	fmt.Println("Initializing submodules in the new worktree...")
	if err := runCmd(newWorktreePath, "git", "submodule", "update", "--init", "--recursive"); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize submodules in the new worktree: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Success!")
}
