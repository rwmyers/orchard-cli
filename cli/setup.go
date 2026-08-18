package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rwmyers/orchard-cli/vcs"
)

// setupOptions carries what `orchard setup` was given on the command line.
// An empty PlantDir or ConfigPath is asked for interactively; an empty
// RootTree falls back to the current directory.
type setupOptions struct {
	RootTree   string
	PlantDir   string
	ConfigPath string
	Force      bool
	Prune      bool
}

// setupPlan is the fully resolved configuration setup is about to write. Every
// path in it is absolute, since a relative path in the configuration file
// would resolve against whichever directory orchard happens to run from.
type setupPlan struct {
	RootTree   string
	PlantDir   string
	ConfigPath string
	// VCS is the name of the driver that recognised the root tree. It is
	// written into the configuration so that later commands do not have to
	// detect it again, and so that a root tree more than one driver would
	// claim keeps the answer setup arrived at.
	VCS string
}

// setupPrompter collects the interactive input for `orchard setup`. It is an
// interface so the flow can be tested without standing up a terminal.
type setupPrompter interface {
	// SelectPath offers the suggested paths plus a free-form entry, and
	// returns "" when the user backs out.
	SelectPath(title, description string, suggestions []string) (string, error)
	// ConfirmReconfigure asks whether to replace a configuration that is
	// already in place.
	ConfirmReconfigure(title, description string) (bool, error)
	// ConfirmPrune asks whether to remove the worktrees left behind in a
	// plant directory that is being moved away from.
	ConfirmPrune(title, description string) (bool, error)
	// ConfirmSetup asks for a final yes/no before anything is written.
	ConfirmSetup(title, description string) (bool, error)
}

// otherPathLabel is the pick-list entry that opens a free-form path input, and
// otherPathValue is the value it carries. The value has to be something no
// suggestion can equal, and a path cannot contain a NUL. An empty value would
// not do: huh opens a select on the option matching the bound variable, so an
// empty one would make the free-form entry the highlighted option and scroll
// the suggestions out of view.
const (
	otherPathLabel = "Other…"
	otherPathValue = "\x00other"
)

// SelectPath shows the suggestions as a pick list with a free-form entry at the
// end. Aborting (ctrl+c or esc) returns no path rather than an error, since
// backing out is a normal outcome and not a failure.
func (p huhPrompter) SelectPath(title, description string, suggestions []string) (string, error) {
	// With nothing to suggest there is no list worth showing, only the path to
	// type.
	if len(suggestions) > 0 {
		options := make([]huh.Option[string], 0, len(suggestions)+1)
		for _, suggestion := range suggestions {
			options = append(options, huh.NewOption(suggestion, suggestion))
		}
		options = append(options, huh.NewOption(otherPathLabel, otherPathValue))

		// Starting on the first suggestion, rather than on whatever the zero
		// value happens to match, opens the list at the top.
		choice := suggestions[0]
		field := huh.NewSelect[string]().
			Title(title).
			Description(description).
			Options(options...).
			Value(&choice)
		if err := p.run(field); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return "", nil
			}
			return "", err
		}
		if choice != otherPathValue {
			return choice, nil
		}
	}

	var custom string
	input := huh.NewInput().
		Title(title).
		Description("Enter a path.").
		Value(&custom).
		Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("a path is required")
			}
			return nil
		})
	if err := p.run(input); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(custom), nil
}

// ConfirmReconfigure asks whether to go through setup again for a repository
// that is already configured.
func (p huhPrompter) ConfirmReconfigure(title, description string) (bool, error) {
	return p.confirm(title, description, "Reconfigure", "Keep")
}

// ConfirmPrune asks whether to clear up the worktrees stranded by a change of
// plant directory.
func (p huhPrompter) ConfirmPrune(title, description string) (bool, error) {
	return p.confirm(title, description, "Remove", "Leave")
}

// ConfirmSetup asks for a final yes/no before the configuration is written.
// Anything other than an explicit yes — including aborting — declines.
func (p huhPrompter) ConfirmSetup(title, description string) (bool, error) {
	return p.confirm(title, description, "Write", "Cancel")
}

// expandPath resolves a leading ~ so that paths typed at the prompts behave the
// way they do in a shell, where the expansion happens before orchard sees them.
func expandPath(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}

// absPath expands and absolutises a path the user supplied.
func absPath(path string) (string, error) {
	abs, err := filepath.Abs(expandPath(path))
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}
	return abs, nil
}

// isInside reports whether path lies below dir. Both must be clean absolute
// paths.
func isInside(dir, path string) bool {
	return strings.HasPrefix(path, dir+string(os.PathSeparator))
}

// resolveRootTree turns the directory setup was pointed at into the root of the
// repository containing it, so that running `orchard setup` from a subdirectory
// configures the repository rather than the subdirectory. It returns the driver
// that recognised the repository along with its root, since that is the one
// answer setup records without asking.
func resolveRootTree(dir string) (vcs.Driver, string, error) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, "", fmt.Errorf("determining the current directory: %w", err)
		}
		dir = wd
	}
	abs, err := absPath(dir)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("%s is not a directory", abs)
	}

	driver, root, err := vcs.Detect(abs)
	if err != nil {
		return nil, "", err
	}
	return driver, filepath.Clean(root), nil
}

// driverIgnores reports whether the repository's ignore rules already cover
// path. A driver that cannot answer is taken to mean "not ignored", which at
// worst produces a hint the user does not need.
func driverIgnores(driver vcs.Driver, rootTree, path string) bool {
	ignorer, ok := driver.(vcs.Ignorer)
	if !ok {
		return false
	}
	ignored, err := ignorer.Ignores(rootTree, path)
	return err == nil && ignored
}

// dedupePaths drops repeats while keeping the first occurrence, so that a
// suggestion coinciding with another is only offered once.
func dedupePaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	var unique []string
	for _, path := range paths {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		unique = append(unique, path)
	}
	return unique
}

// plantable reports whether dir is somewhere worktrees may be planted. It
// mirrors checkPlan so that a suggestion is never one the plan would reject.
func plantable(rootTree, dir string) bool {
	return dir != rootTree && !isInside(rootTree, dir)
}

// plantsDirName is the directory `orchard setup` offers to gather worktrees
// into, when they are not going straight beside the root tree.
const plantsDirName = "plants"

// defaultPlantDirs suggests the layouts that need no further thought:
// worktrees planted beside the root tree, in a plants directory next to it, or
// in the directory the command was run from. Anything already configured leads
// the list, so a second run can keep what the first chose.
func defaultPlantDirs(rootTree, workDir, configured string) []string {
	beside := filepath.Dir(rootTree)
	var dirs []string
	for _, dir := range []string{configured, beside, filepath.Join(beside, plantsDirName), workDir} {
		if dir != "" && plantable(rootTree, dir) {
			dirs = append(dirs, dir)
		}
	}
	return dedupePaths(dirs)
}

// defaultConfigPaths suggests the locations orchard finds on its own: the root
// tree, the directory the command was run from, and the global fallback. A
// file that is already in place leads the list.
func defaultConfigPaths(rootTree, workDir, configured string) []string {
	paths := []string{configured, filepath.Join(rootTree, configFileName)}
	if workDir != "" {
		paths = append(paths, filepath.Join(workDir, configFileName))
	}
	paths = append(paths, globalConfigPath())
	return dedupePaths(paths)
}

// existingSetup finds a configuration already in place for the root tree and
// reads what it says. The lookup covers everywhere setup can write and
// everywhere orchard reads, so a second run recognises the first. A file that
// cannot be parsed still counts as found, since replacing it is exactly what
// setup is for; the configuration is nil in that case.
func existingSetup(rootTree, workDir, explicit string) (string, *Config) {
	var candidates []string
	if explicit != "" {
		candidates = []string{explicit}
	} else {
		candidates = append(candidates, filepath.Join(rootTree, configFileName))
		if workDir != "" {
			candidates = append(candidates, filepath.Join(workDir, configFileName))
		}
		candidates = dedupePaths(append(candidates, globalConfigPath()))
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		cfg, err := parseConfig(candidate)
		if err != nil {
			return candidate, nil
		}
		return candidate, cfg
	}
	return "", nil
}

// strandedWorktrees returns the worktrees that a move of the plant directory
// would leave behind. Once the configuration points somewhere else, orchard
// stops listing them, so nothing short of raw git can remove them — while the
// branches they hold on to still block `orchard add` from reusing those names.
//
// It reports nothing unless the previous configuration was for this same root
// tree: a configuration found in the working directory or at the global path
// may well belong to another repository, whose worktrees are none of setup's
// business.
func strandedWorktrees(driver vcs.Driver, rootTree string, previous *Config, plantDir string) []plantedWorktree {
	if previous == nil || previous.RootTree != rootTree || previous.PlantDir == plantDir {
		return nil
	}
	worktrees, err := driver.ListWorktrees(rootTree)
	if err != nil {
		return nil
	}
	return filterPlantedWorktrees(worktrees, rootTree, previous.PlantDir)
}

// worktreeNames pulls the names out of a list of planted worktrees.
func worktreeNames(planted []plantedWorktree) []string {
	names := make([]string, len(planted))
	for i, wt := range planted {
		names[i] = wt.Name
	}
	return names
}

// existingSummary describes the configuration already in place, so that the
// choice to replace it is an informed one.
func existingSummary(cfg *Config) string {
	if cfg == nil {
		return "It cannot be read as an orchard configuration."
	}
	return "It currently says:\n\nroot_tree = " + cfg.RootTree + "\nplant_dir = " + cfg.PlantDir
}

// renderConfig produces the configuration file contents.
func renderConfig(plan setupPlan) string {
	return fmt.Sprintf(`# Written by orchard setup.

# The repository worktrees are created from.
root_tree = %s

# The directory new worktrees are planted in.
plant_dir = %s

# The version control driver that manages the root tree.
vcs = %s
`, plan.RootTree, plan.PlantDir, plan.VCS)
}

// checkPlan rejects layouts orchard cannot manage, before anything is created.
func checkPlan(plan setupPlan) error {
	if plan.PlantDir == plan.RootTree || isInside(plan.RootTree, plan.PlantDir) {
		return fmt.Errorf("plant directory %s is inside the root tree; worktrees must be planted outside the repository", plan.PlantDir)
	}
	if info, err := os.Stat(plan.PlantDir); err == nil && !info.IsDir() {
		return fmt.Errorf("plant directory %s is not a directory", plan.PlantDir)
	}
	return nil
}

// setupSummary describes the plan for the confirmation prompt, calling out
// everything that happens to the filesystem beyond writing the file itself.
func setupSummary(plan setupPlan, plantDirExists, configExists bool, pruning []plantedWorktree, withBranches bool) string {
	lines := []string{
		"root_tree = " + plan.RootTree,
		"plant_dir = " + plan.PlantDir,
		"vcs = " + plan.VCS,
	}
	if !plantDirExists {
		lines = append(lines, "", plan.PlantDir+" will be created.")
	}
	if configExists {
		lines = append(lines, "", plan.ConfigPath+" already exists and will be replaced.")
	}
	if len(pruning) > 0 {
		lines = append(lines, "", fmt.Sprintf("%d %s will be removed first:", len(pruning), removalSubject(withBranches)),
			confirmDescription(worktreeNames(pruning)))
	}
	return strings.Join(lines, "\n")
}

// strandedSummary lists the worktrees a move would leave behind, and where.
func strandedSummary(previousPlantDir string, stranded []plantedWorktree, withBranches bool) string {
	reserved := ""
	if withBranches {
		reserved = ", and their branches keep the names reserved"
	}
	return fmt.Sprintf("They stay in %s, where orchard can no longer see them%s.\n\n%s",
		previousPlantDir, reserved, confirmDescription(worktreeNames(stranded)))
}

// setupHints explains what the chosen location means for day-to-day use: how
// orchard will find the file, and whether it is about to show up in git status.
func setupHints(plan setupPlan, ignored bool) []string {
	var hints []string
	switch {
	case plan.ConfigPath == globalConfigPath():
		hints = append(hints, "orchard finds this configuration from any directory.")
	case filepath.Base(plan.ConfigPath) == configFileName:
		hints = append(hints,
			fmt.Sprintf("orchard finds this configuration when run from %s.", filepath.Dir(plan.ConfigPath)),
			fmt.Sprintf("From anywhere else, pass --config %s.", plan.ConfigPath))
	default:
		hints = append(hints, fmt.Sprintf("The file is not named %s, so pass --config %s to use it.", configFileName, plan.ConfigPath))
	}
	if isInside(plan.RootTree, plan.ConfigPath) && !ignored {
		hints = append(hints, fmt.Sprintf("It sits inside the repository; add %s to .gitignore to keep it out of commits.",
			filepath.Base(plan.ConfigPath)))
	}
	return hints
}

// writeSetup creates the plant directory and the configuration file.
func writeSetup(plan setupPlan) error {
	if err := os.MkdirAll(plan.PlantDir, 0o755); err != nil {
		return fmt.Errorf("creating plant directory %s: %w", plan.PlantDir, err)
	}
	configDir := filepath.Dir(plan.ConfigPath)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", configDir, err)
	}
	if err := os.WriteFile(plan.ConfigPath, []byte(renderConfig(plan)), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", plan.ConfigPath, err)
	}
	return nil
}

// resolveConfigDestination turns a path the user gave into the file to write.
// Pointing at a directory is taken as "put the file in here", which is what a
// path ending in a directory name is asking for.
func resolveConfigDestination(path string) (string, error) {
	resolved, err := absPath(path)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(resolved); err == nil && info.IsDir() {
		resolved = filepath.Join(resolved, configFileName)
	}
	return resolved, nil
}

func runSetup(opts setupOptions, p setupPrompter) error {
	driver, rootTree, err := resolveRootTree(opts.RootTree)
	if err != nil {
		return err
	}
	caps := vcs.CapabilitiesOf(driver)
	// The directory the command was run from is worth offering as an answer to
	// both questions. It is not fatal for it to be unavailable.
	workDir, err := os.Getwd()
	if err != nil {
		workDir = ""
	}

	explicit := ""
	if opts.ConfigPath != "" {
		if explicit, err = resolveConfigDestination(opts.ConfigPath); err != nil {
			return err
		}
	}

	// The prompts only fill in what the flags left out, so supplying the plant
	// directory — the one value with no obvious default — makes the whole run
	// non-interactive.
	interactive := opts.PlantDir == ""

	// A repository that is already set up gets the choice of going round
	// again, with its current answers leading the suggestions.
	existingPath, existing := existingSetup(rootTree, workDir, explicit)
	if interactive {
		if existingPath != "" {
			again, err := p.ConfirmReconfigure(fmt.Sprintf("%s is already set up. Reconfigure it?", filepath.Base(existingPath)),
				existingPath+"\n\n"+existingSummary(existing))
			if err != nil {
				return fmt.Errorf("reading confirmation: %w", err)
			}
			if !again {
				fmt.Printf("Keeping %s.\n", existingPath)
				return nil
			}
		}
	}

	configuredPlantDir := ""
	if existing != nil {
		configuredPlantDir = existing.PlantDir
	}

	plantDir := opts.PlantDir
	if plantDir == "" {
		plantDir, err = p.SelectPath(
			"Where should worktrees be planted?",
			fmt.Sprintf("Root tree: %s\n`orchard add` creates each worktree in this directory.", rootTree),
			defaultPlantDirs(rootTree, workDir, configuredPlantDir))
		if err != nil {
			return fmt.Errorf("reading the plant directory: %w", err)
		}
		if plantDir == "" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	configPath := explicit
	if configPath == "" && interactive {
		configPath, err = p.SelectPath(
			fmt.Sprintf("Where should %s be written?", configFileName),
			fmt.Sprintf("orchard reads %s from the working directory, then falls back to the global path.", configFileName),
			defaultConfigPaths(rootTree, workDir, existingPath))
		if err != nil {
			return fmt.Errorf("reading the configuration path: %w", err)
		}
		if configPath == "" {
			fmt.Println("Aborted.")
			return nil
		}
	}
	if configPath == "" {
		configPath = filepath.Join(rootTree, configFileName)
	}

	plan := setupPlan{RootTree: rootTree, VCS: driver.Name()}
	if plan.PlantDir, err = absPath(plantDir); err != nil {
		return err
	}
	if plan.ConfigPath, err = resolveConfigDestination(configPath); err != nil {
		return err
	}
	if err := checkPlan(plan); err != nil {
		return err
	}

	plantDirExists := false
	if info, err := os.Stat(plan.PlantDir); err == nil && info.IsDir() {
		plantDirExists = true
	}
	configExists := false
	if _, err := os.Stat(plan.ConfigPath); err == nil {
		configExists = true
	}

	// Moving the plant directory strands whatever is planted in the old one.
	stranded := strandedWorktrees(driver, rootTree, existing, plan.PlantDir)
	prune := opts.Prune
	if len(stranded) > 0 && !prune && interactive {
		prune, err = p.ConfirmPrune(
			fmt.Sprintf("Remove the %d worktree(s) planted in the previous directory?", len(stranded)),
			strandedSummary(existing.PlantDir, stranded, caps.Branches))
		if err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
	}
	if len(stranded) > 0 && !prune {
		// Only the directory named by the configuration being replaced is
		// known, so once this move is done these worktrees are a step further
		// out of reach. Say how to get back to them.
		fmt.Printf("Leaving %d worktree(s) in %s, where orchard will no longer list them:\n",
			len(stranded), existing.PlantDir)
		for _, name := range worktreeNames(stranded) {
			fmt.Printf("  %s\n", name)
		}
		if caps.Branches {
			fmt.Println("Their branches keep those names reserved.")
		}
		fmt.Printf("To reach them again, point orchard back at that directory with\n"+
			"`orchard setup %s --plant-dir %s --force`.\n", rootTree, existing.PlantDir)
		if !interactive {
			fmt.Println("Pass --prune to remove them instead.")
		}
		stranded = nil
	}

	if interactive {
		confirmed, err := p.ConfirmSetup(fmt.Sprintf("Write %s?", plan.ConfigPath),
			setupSummary(plan, plantDirExists, configExists, stranded, caps.Branches))
		if err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
		if !confirmed {
			fmt.Println("Aborted.")
			return nil
		}
	} else if configExists && !opts.Force {
		return fmt.Errorf("%s already exists; pass --force to overwrite it", plan.ConfigPath)
	}

	// Clearing up happens before the configuration moves, so that a removal
	// that goes wrong leaves the old configuration in place and the worktrees
	// still reachable through `orchard remove`.
	if len(stranded) > 0 {
		if err := removeWorktrees(driver, rootTree, existing.PlantDir, worktreeNames(stranded)); err != nil {
			return err
		}
	}

	if err := writeSetup(plan); err != nil {
		return err
	}

	fmt.Printf("Wrote %s\n\n%s\n", plan.ConfigPath, renderConfig(plan))
	for _, hint := range setupHints(plan, driverIgnores(driver, plan.RootTree, plan.ConfigPath)) {
		fmt.Println(hint)
	}
	return nil
}
