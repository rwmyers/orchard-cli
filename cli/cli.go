// Package cli is orchard's command line, separated from package main so that a
// binary bundling third-party drivers can reuse it:
//
//	package main
//
//	import (
//		"github.com/rwmyers/orchard-cli/cli"
//		_ "github.com/rwmyers/orchard-cli/vcs/git"
//		_ "github.com/example/orchard-jj"
//	)
//
//	func main() { cli.Main() }
//
// Nothing here knows about git. Everything that touches a repository goes
// through the driver resolved for the root tree, and the parts of orchard's
// model a driver has not opted into are skipped rather than assumed — see the
// vcs package.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/rwmyers/orchard-cli/vcs"
	"github.com/spf13/cobra"
)

type plantedWorktree struct {
	Name string
	Path string
}

// filterPlantedWorktrees returns the worktrees that orchard manages: direct
// children of plantDir, excluding the root tree itself.
func filterPlantedWorktrees(worktrees []vcs.Worktree, rootTree, plantDir string) []plantedWorktree {
	var planted []plantedWorktree
	for _, wt := range worktrees {
		if wt.Path == rootTree || filepath.Dir(wt.Path) != plantDir {
			continue
		}
		planted = append(planted, plantedWorktree{Name: filepath.Base(wt.Path), Path: wt.Path})
	}
	return planted
}

func worktreeExists(worktrees []vcs.Worktree, plantDir, name string) bool {
	targetPath := filepath.Clean(filepath.Join(plantDir, name))
	for _, wt := range worktrees {
		if wt.Path == targetPath {
			return true
		}
	}
	return false
}

// Main runs orchard and exits. It is what a binary bundling extra drivers
// calls once it has imported them.
func Main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configPath string

	rootCmd := &cobra.Command{
		Use:   "orchard",
		Short: "Manage worktrees based on a designated root repository",
	}
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "path to configuration file")

	// Resolves the configuration and the driver once arg validation has
	// passed. Silences usage so that runtime failures don't dump help text;
	// arg-validation errors, which happen before RunE, still show it.
	loadOrchard := func(cmd *cobra.Command) (*orchard, error) {
		cmd.SilenceUsage = true
		return load(configPath)
	}

	var baseRef string
	addCmd := &cobra.Command{
		Use:   "add <worktree_name>...",
		Short: "Create one or more worktrees",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := loadOrchard(cmd)
			if err != nil {
				return err
			}
			return runAdd(o, args, baseRef)
		},
	}
	addCmd.Flags().StringVarP(&baseRef, "base", "b", "", "branch or commit the new worktrees start from (defaults to the root tree's HEAD)")

	var check bool
	removeCmd := &cobra.Command{
		Use:   "remove [<worktree_name>...]",
		Short: "Remove worktrees (pick list when no names are given)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := loadOrchard(cmd)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return runRemoveInteractive(o, newHuhPrompter(), check)
			}
			return runRemove(o, args, check)
		},
	}
	removeCmd.Flags().BoolVar(&check, "check", false,
		"refuse to remove a worktree holding local changes or unpushed commits")

	rootPathCmd := &cobra.Command{
		Use:   "root",
		Short: "Print the root tree path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := loadOrchard(cmd)
			if err != nil {
				return err
			}
			// Print only the path so the output can be used in command
			// substitution, e.g. cd "$(orchard root)".
			fmt.Println(o.RootTree)
			return nil
		},
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List worktrees in the plant directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := loadOrchard(cmd)
			if err != nil {
				return err
			}
			return runList(o)
		},
	}

	driversCmd := &cobra.Command{
		Use:   "drivers",
		Short: "List the version control drivers this binary was built with",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			return runDrivers(cmd.OutOrStdout())
		},
	}

	var setupOpts setupOptions
	setupCmd := &cobra.Command{
		Use:   "setup [<root_tree>]",
		Short: "Write a configuration file for a repository",
		Long: `Write an ` + configFileName + ` for a repository.

The root tree defaults to the repository containing the current directory; pass
a directory to configure that repository instead. Whichever registered driver
recognises it is recorded in the configuration. The plant directory and the
location of the configuration file are asked for interactively, and the plant
directory is created if it does not exist.

Supplying --plant-dir answers the only question without an obvious default and
so skips the prompts entirely. For this subcommand --config says where the
configuration is written rather than where it is read from.

Moving the plant directory strands anything already planted in the old one,
since orchard stops listing it. Setup offers to remove those worktrees;
--prune answers that offer up front.`,
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

	rootCmd.AddCommand(setupCmd, addCmd, removeCmd, rootPathCmd, listCmd, driversCmd)
	return rootCmd
}

// runDrivers reports what this binary can drive and what each driver supports,
// which is the quickest way to tell whether a third-party driver made it in.
func runDrivers(out io.Writer) error {
	registered := vcs.Registered()
	if len(registered) == 0 {
		_, err := fmt.Fprintln(out, "No drivers are registered; this binary cannot manage any repository.")
		return err
	}

	// A driver that failed to load reports no capabilities, which on its own
	// looks like a driver that simply does very little. The source column
	// carries the reason so the difference is visible.
	var broken []string

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "DRIVER\tBRANCHES\tUPDATE\tBASE\tIGNORE\tINSPECT\tSOURCE")
	for _, d := range registered {
		caps := vcs.CapabilitiesOf(d)
		source, err := vcs.StatusOf(d)
		if err != nil {
			source += "  [failed]"
			broken = append(broken, fmt.Sprintf("%s: %v", d.Name(), err))
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", d.Name(),
			yesNo(caps.Branches), yesNo(caps.Update), yesNo(caps.BaseRef),
			yesNo(caps.Ignores), yesNo(caps.Inspect), source)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	for _, reason := range broken {
		_, _ = fmt.Fprintf(out, "\n%s\n", reason)
	}
	return nil
}

func yesNo(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
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

func runAdd(o *orchard, worktreeNames []string, baseRef string) error {
	fmt.Printf("Loaded config: root_tree=%s, plant_dir=%s, vcs=%s\n", o.RootTree, o.PlantDir, o.driver.Name())

	// Validate every name before creating anything, so a bad name later in the
	// list doesn't leave a half-planted batch behind.
	if dup := duplicateName(worktreeNames); dup != "" {
		return fmt.Errorf("worktree %q is named more than once", dup)
	}
	// A base the driver cannot honour is refused rather than ignored, so that
	// nobody ends up with a batch quietly planted on the wrong commit.
	if baseRef != "" && !o.caps.BaseRef {
		return fmt.Errorf("the %s driver does not support --base", o.driver.Name())
	}

	worktrees, err := o.driver.ListWorktrees(o.RootTree)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}
	brancher, _ := o.driver.(vcs.Brancher)
	for _, name := range worktreeNames {
		if worktreeExists(worktrees, o.PlantDir, name) {
			return fmt.Errorf("worktree %q already exists", name)
		}
		if brancher == nil {
			continue
		}
		branchExists, err := brancher.BranchExists(o.RootTree, name)
		if err != nil {
			return fmt.Errorf("checking if branch exists: %w", err)
		}
		if branchExists {
			return fmt.Errorf("branch %q already exists", name)
		}
	}

	// 1. Bring the root tree up to date, for a driver that can.
	if updater, ok := o.driver.(vcs.Updater); ok {
		fmt.Println("Updating root repository...")
		if err := updater.UpdateRoot(o.RootTree); err != nil {
			return fmt.Errorf("failed to update root_tree: %w", err)
		}
	}

	// The base is resolved after the update, since it may be a ref that only
	// arrives with it.
	if baseRef != "" {
		baseExists, err := o.driver.(vcs.BaseResolver).BaseExists(o.RootTree, baseRef)
		if err != nil {
			return fmt.Errorf("checking base %q: %w", baseRef, err)
		}
		if !baseExists {
			return fmt.Errorf("base %q is not a branch or commit in the root tree", baseRef)
		}
	}

	// 2. Create a worktree for each name.
	for _, name := range worktreeNames {
		newWorktreePath := filepath.Join(o.PlantDir, name)
		fmt.Printf("Creating new worktree at %s...\n", newWorktreePath)

		if err := o.driver.AddWorktree(vcs.AddRequest{
			Root: o.RootTree,
			Name: name,
			Path: newWorktreePath,
			Base: baseRef,
		}); err != nil {
			return fmt.Errorf("failed to create worktree %q: %w", name, err)
		}
	}

	fmt.Println("Success!")
	return nil
}

func runList(o *orchard) error {
	worktrees, err := o.driver.ListWorktrees(o.RootTree)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}

	// Print name and path only, tab-separated, so the output is easy to
	// consume from scripts (e.g. cut, awk).
	for _, wt := range filterPlantedWorktrees(worktrees, o.RootTree, o.PlantDir) {
		fmt.Printf("%s\t%s\n", wt.Name, wt.Path)
	}
	return nil
}

func runRemoveInteractive(o *orchard, p prompter, check bool) error {
	worktrees, err := o.driver.ListWorktrees(o.RootTree)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}
	planted := filterPlantedWorktrees(worktrees, o.RootTree, o.PlantDir)
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

	confirmed, err := p.ConfirmRemoval(names, o.caps.Branches)
	if err != nil {
		return fmt.Errorf("reading confirmation: %w", err)
	}
	if !confirmed {
		fmt.Println("Aborted.")
		return nil
	}
	return runRemove(o, names, check)
}

// checkRemovable refuses the whole removal if any worktree still holds work.
// It reports every offender rather than the first, so that a batch is fixed in
// one pass instead of one refusal at a time.
func checkRemovable(o *orchard, worktrees []vcs.Worktree, names []string) error {
	if !o.caps.Inspect {
		return fmt.Errorf("the %s driver cannot check whether a worktree still holds work, so --check is not available",
			o.driver.Name())
	}
	inspector := o.driver.(vcs.Inspector)

	byName := make(map[string]vcs.Worktree, len(worktrees))
	for _, wt := range worktrees {
		byName[filepath.Base(wt.Path)] = wt
	}

	var held []string
	for _, name := range names {
		state, err := inspector.Inspect(o.RootTree, byName[name])
		if err != nil {
			return fmt.Errorf("checking worktree %q: %w", name, err)
		}
		if state.Safe() {
			continue
		}
		switch {
		case state.Dirty && state.Unpublished:
			held = append(held, name+" (local changes, unpushed commits)")
		case state.Dirty:
			held = append(held, name+" (local changes)")
		default:
			held = append(held, name+" (unpushed commits)")
		}
	}
	if len(held) > 0 {
		return fmt.Errorf("refusing to remove %d worktree(s) that still hold work:\n  %s\nre-run without --check to remove them anyway",
			len(held), strings.Join(held, "\n  "))
	}
	return nil
}

func runRemove(o *orchard, worktreeNames []string, check bool) error {
	fmt.Printf("Loaded config: root_tree=%s, plant_dir=%s, vcs=%s\n", o.RootTree, o.PlantDir, o.driver.Name())

	// Validate every name before removing anything, so a bad name later in
	// the list doesn't leave the removal half-done.
	worktrees, err := o.driver.ListWorktrees(o.RootTree)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}
	brancher, _ := o.driver.(vcs.Brancher)
	for _, name := range worktreeNames {
		if !worktreeExists(worktrees, o.PlantDir, name) {
			return fmt.Errorf("worktree %q does not exist", name)
		}
		if brancher == nil {
			continue
		}
		branchExists, err := brancher.BranchExists(o.RootTree, name)
		if err != nil {
			return fmt.Errorf("checking if branch exists: %w", err)
		}
		if !branchExists {
			return fmt.Errorf("branch %q does not exist", name)
		}
	}

	// Checked before anything is removed, so a batch holding work is refused
	// whole rather than part-way through.
	if check {
		if err := checkRemovable(o, worktrees, worktreeNames); err != nil {
			return err
		}
	}

	if err := removeWorktrees(o.driver, o.RootTree, o.PlantDir, worktreeNames); err != nil {
		return err
	}

	fmt.Println("Success!")
	return nil
}

// removeWorktrees removes each named worktree from plantDir and, for a driver
// that ties worktrees to branches, deletes the branch of the same name. The
// branch is only deleted if it is still there: `orchard remove` has already
// insisted on it, but `orchard setup` clears up worktrees it did not
// necessarily create, and a missing branch is no reason to leave one of those
// behind.
func removeWorktrees(driver vcs.Driver, rootTree, plantDir string, names []string) error {
	brancher, _ := driver.(vcs.Brancher)
	for _, name := range names {
		// 1. Remove the worktree
		worktreePath := filepath.Join(plantDir, name)
		fmt.Printf("Removing worktree at %s...\n", worktreePath)
		if err := driver.RemoveWorktree(vcs.RemoveRequest{
			Root: rootTree,
			Name: name,
			Path: worktreePath,
		}); err != nil {
			return fmt.Errorf("failed to remove worktree %q: %w", name, err)
		}

		// 2. Delete the branch, for a driver that has one
		if brancher == nil {
			continue
		}
		branchExists, err := brancher.BranchExists(rootTree, name)
		if err != nil {
			return fmt.Errorf("checking if branch exists: %w", err)
		}
		if !branchExists {
			continue
		}
		fmt.Printf("Deleting branch %s...\n", name)
		if err := brancher.DeleteBranch(rootTree, name); err != nil {
			return fmt.Errorf("failed to delete branch %q: %w", name, err)
		}
	}
	return nil
}
