// Command orchard manages worktrees based on a designated root repository.
//
// This binary is orchard with the drivers it ships with — git and jj — plus
// whatever orchard-vcs-* plugins are on $PATH. A driver someone else wrote in
// Go is added by building your own binary from a main exactly like this one,
// with their package imported alongside:
//
//	import (
//		"github.com/rwmyers/orchard-cli/cli"
//		_ "github.com/rwmyers/orchard-cli/vcs/git"
//		_ "github.com/example/orchard-hg"
//	)
//
// Both imports are for effect: a driver package registers itself from init, and
// cli finds it through the registry in the vcs package. See vcs/doc for what a
// driver has to implement.
package main

import (
	"github.com/rwmyers/orchard-cli/cli"

	// Registers the built-in git driver.
	_ "github.com/rwmyers/orchard-cli/vcs/git"
	// Registers the built-in jj driver.
	_ "github.com/rwmyers/orchard-cli/vcs/jj"
	// Registers every orchard-vcs-* executable found on $PATH.
	_ "github.com/rwmyers/orchard-cli/vcs/plugin"
)

func main() { cli.Main() }
