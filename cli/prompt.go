package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
)

// prompter collects the interactive input for `orchard remove` when no names
// are given. It is an interface so the removal flow can be tested without
// standing up a terminal.
type prompter interface {
	PickWorktrees(planted []plantedWorktree) ([]string, error)
	ConfirmRemoval(names []string, withBranches bool) (bool, error)
}

// removalSubject describes what a removal is about to destroy. Only a driver
// that ties worktrees to branches loses one, so the wording follows the
// driver rather than assuming git's model.
func removalSubject(withBranches bool) string {
	if withBranches {
		return "worktree(s) and their branches"
	}
	return "worktree(s)"
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
func (p huhPrompter) ConfirmRemoval(names []string, withBranches bool) (bool, error) {
	return p.confirm(fmt.Sprintf("Remove %d %s?", len(names), removalSubject(withBranches)),
		confirmDescription(names), "Remove", "Cancel")
}
