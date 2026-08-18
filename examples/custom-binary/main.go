// Command orchard-custom is orchard built with a driver that does not live in
// orchard's repository.
//
// This is the whole cost of extending orchard, and this module is the proof it
// works: a separate go.mod, importing nothing but orchard's public packages,
// producing a binary that drives one more system than the stock one.
package main

import (
	"github.com/rwmyers/orchard-cli/cli"

	// Each import registers a driver. Drop any of them and this binary no
	// longer drives that system; add your own and it does.
	_ "github.com/example/orchard-custom/hg"
	_ "github.com/rwmyers/orchard-cli/vcs/git"
	_ "github.com/rwmyers/orchard-cli/vcs/jj"
	_ "github.com/rwmyers/orchard-cli/vcs/plugin"
)

func main() { cli.Main() }
