# orchard-vcs-demo

A complete orchard VCS driver as an **executable**, in Python, with no
dependency on orchard's source. It exists to show that the plugin protocol is
real and usable from outside Go.

It is backed by git, so it can be run against a real repository and compared
against the built-in git driver. The point is not what it drives — it is that it
drives anything at all from outside the binary.

```bash
cp orchard-vcs-demo ~/bin/
orchard drivers
```

```
DRIVER  BRANCHES  UPDATE  BASE  IGNORE  INSPECT  SOURCE
demo    yes       yes     yes   yes     yes      plugin /home/me/bin/orchard-vcs-demo (0.1.0)
git     yes       yes     yes   yes     yes      built-in
```

Point a repository at it with `vcs = demo` in `orchard.conf`, and everything
that touches the repository then goes through this script:

```bash
orchard add feat-a          # → add-worktree
orchard list                # → list-worktrees
orchard remove feat-a --check   # → inspect, then remove-worktree, delete-branch
```

You can also drive it by hand, which is the quickest way to develop one:

```bash
$ echo '{"api_version":1}' | ./orchard-vcs-demo describe
{"api_version": 1, "name": "demo", "version": "0.1.0", "capabilities": [...]}

$ echo '{"api_version":1,"params":{"dir":"/tmp"}}' | ./orchard-vcs-demo detect
{"not_repository": true}
```

## What to look at

- **`CAPABILITIES`** near the top is the whole of the capability system from a
  plugin's side. Removing `"branches"` from that list models a system whose
  worktrees are not tied to a named branch, and orchard immediately stops asking
  about branches — no other change anywhere.
- **`detect`** returns `{"not_repository": true}` rather than an error.
  Declining is the normal outcome for every driver but one, and has to be
  distinguishable from being broken.
- **`inspect`** answers the questions that decide whether orchard may reclaim a
  worktree on its own initiative. Anything it cannot determine must be reported
  as work-in-progress.
- **`fail()`** is the error convention: non-zero exit with `{"error": "..."}` on
  stdout. Orchard quotes the message, so it is worth writing for a person.
- **stderr** carries the `$ git ...` trace. Orchard logs it tagged with the
  plugin name and never parses it.

The protocol is specified in [docs/PLUGINS.md](../../docs/PLUGINS.md).
