# orchard-custom

Orchard built with a driver that does not live in orchard's repository.

This module is the proof that the extension mechanism works: a separate
`go.mod`, importing nothing but orchard's public packages, producing a binary
that drives one more system than the stock one.

```bash
go build ./... && ./orchard-custom drivers
```

```
DRIVER  BRANCHES  UPDATE  BASE  IGNORE  INSPECT  SOURCE
git     yes       yes     yes   yes     yes      built-in
hg      no        yes     yes   no      no       built-in
jj      no        yes     yes   no      yes      built-in
```

`main.go` is the entire cost of using a third-party driver — import it for
effect, call `cli.Main()`.

## About the hg driver

`hg/hg.go` is a **skeleton, for copying rather than for using**. It has not been
run against real Mercurial; treat every command in it as a guess to be checked.
What is worth copying is the structure, and in particular the interfaces it
*omits*: `vcs.Brancher` is left out because `hg share` working copies are not
tied to a named branch, and orchard responds by dropping every branch step.

It also has an honest hole. `ListWorktrees` returns nothing, because `hg share`
records its shares nowhere and a real driver would have to keep its own record.
Finding that sort of gap is most of the work of writing a driver, so it is left
visible rather than papered over.

For real, working drivers read [`vcs/git`](../../vcs/git) and
[`vcs/jj`](../../vcs/jj); for the protocol alternative see
[`examples/orchard-vcs-demo`](../orchard-vcs-demo).
