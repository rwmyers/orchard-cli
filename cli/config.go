package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rwmyers/orchard-cli/vcs"
)

type Config struct {
	RootTree string
	PlantDir string
	// VCS names the driver that manages the root tree. It is optional: when
	// empty, orchard asks each registered driver to recognise the root tree.
	// Setting it skips that, which matters for a directory more than one
	// driver would claim.
	VCS string
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
		case "vcs":
			cfg.VCS = val
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

// orchard is a resolved configuration together with the driver managing its
// root tree. Every subcommand but `setup` works from one, so that the driver is
// settled — and any complaint about it reported — before anything is done.
type orchard struct {
	*Config
	driver vcs.Driver
	caps   vcs.Capabilities
}

// resolveDriver picks the driver for cfg's root tree: the one the `vcs` key
// names, or failing that whichever registered driver recognises the tree.
func resolveDriver(cfg *Config) (vcs.Driver, error) {
	if cfg.VCS != "" {
		driver, ok := vcs.Lookup(cfg.VCS)
		if !ok {
			return nil, fmt.Errorf("no driver named %q is registered (have %v)", cfg.VCS, vcs.Names())
		}
		return driver, nil
	}
	driver, _, err := vcs.Detect(cfg.RootTree)
	if err != nil {
		return nil, err
	}
	return driver, nil
}

func load(configPath string) (*orchard, error) {
	cfg, err := resolveConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	driver, err := resolveDriver(cfg)
	if err != nil {
		return nil, err
	}
	return &orchard{Config: cfg, driver: driver, caps: vcs.CapabilitiesOf(driver)}, nil
}
