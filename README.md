# recall

`recall` finds, bookmarks, resumes, and forks your local Codex and Claude Code
conversations across every working directory you use them in.

It reads the providers' own transcript files and hands resuming and forking back
to their official CLIs. There is no daemon, index, database, or network access.

## Requirements

- The `codex` and/or `claude` CLI.
- [`fzf`](https://github.com/junegunn/fzf) for the picker.
- Go 1.23 or newer to build from source.
- [Hammerspoon](https://www.hammerspoon.org/) for global hotkeys on macOS.

## Install

### Arch Linux / Omarchy

```sh
git clone https://github.com/jborlum/recall.git
cd recall
makepkg -si
```

Pacman then owns `/usr/bin/recall` and pulls in `fzf`. Note that `makepkg`
builds the release tag named in `PKGBUILD`, not your working tree.

### Linux, from a release binary

For a remote box or a devbox with no Go toolchain and no package manager of its
own:

```sh
curl -fsSL https://raw.githubusercontent.com/jborlum/recall/main/install.sh | sh
```

The script resolves the latest release, verifies its `SHA256SUMS` entry, and
lands a static `linux/amd64` binary in `~/.local/bin/recall`. Re-run it to
update; it exits early when that release is already installed, and `-f`
reinstalls anyway. `PREFIX` chooses another directory and `RECALL_VERSION` picks
a specific tag:

```sh
curl -fsSL .../install.sh | RECALL_VERSION=v0.11.0 PREFIX=/usr/local/bin sh
```

`fzf` still has to come from the box's own package manager, or from
[the `fzf` releases page](https://github.com/junegunn/fzf/releases).

### macOS

This repository doubles as its own Homebrew tap, so no clone is needed:

```sh
brew tap jborlum/recall https://github.com/jborlum/recall.git
brew install jborlum/recall/recall
```

Homebrew requires formulae to live in a tap, so installing the formula by path
is rejected. Installing by full name also records it as trusted, which Homebrew
requires for taps outside homebrew-core.

Add `--HEAD` to build the current `main` instead of the latest release, and
upgrade a `--HEAD` install with `brew upgrade --fetch-HEAD`.

For global hotkeys:

```sh
brew install --cask hammerspoon
recall setup
```

### From source, without a package manager

Works the same on macOS and Linux, and needs no Homebrew:

```sh
git clone https://github.com/jborlum/recall.git
cd recall
go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o recall .
mkdir -p ~/.local/bin
install -m755 recall ~/.local/bin/recall
```

`fzf` is the only runtime dependency. Without a package manager, take a binary
from [the `fzf` releases page](https://github.com/junegunn/fzf/releases) and put
it on your `PATH`. For hotkeys on macOS, unzip a
[Hammerspoon release](https://github.com/Hammerspoon/Hammerspoon/releases) into
`/Applications` and launch it once to grant Accessibility permission.

## Usage

```sh
recall                         # search every session, then resume one
recall refresh token           # search titles and full transcript text
recall --print cache           # print matches instead of opening one
recall --provider codex auth   # only Codex sessions
recall --cwd .                 # only sessions from this directory tree
```

In the picker, `Enter` resumes, `Ctrl-F` forks, and `Esc` closes.

Searching covers the whole visible conversation, not just the title, so a
remembered phrase finds the session even if nothing in its title hints at it.
Encrypted and internal provider fields are excluded.

The list keeps its provider, date, and title columns while you type. The panel
below shows every match in the selected conversation, each numbered `3/12` and
given the lines around it, with your terms marked. `Shift-Up`/`Shift-Down` and
`Alt-Up`/`Alt-Down` walk through the matches and stop at either end;
`Ctrl-/` hides the panel. With no search term the panel shows the conversation
from the start.

Only the selected transcript is ever read for the panel, so the cost does not
grow with the number of sessions.

```sh
recall bookmark                       # resume, fork, or delete a bookmark
recall bookmark add auth-design token # name a matching session
recall bookmark active                # name the session running right now
recall bookmark list                  # print bookmarks
recall bookmark remove auth-design    # delete the bookmark, keep the session
recall doctor                         # report discovery and stale bookmarks
```

The bookmark views put the name in its own column beside the conversation
title, and search the full transcript as well, so a word from the conversation
finds a bookmark whose name says nothing about it. `Ctrl-D` deletes the
selected bookmark after confirming. A bookmark marked `[missing]` has no
transcript left and falls back to what was recorded when it was created.

Global flags go before the command or query. `recall help` lists everything.

## Global hotkeys

`recall setup` installs three hotkeys that open a terminal running `recall`:

| Action | Omarchy | macOS |
| --- | --- | --- |
| Bookmark the active session | `SUPER+ALT+B` | `CMD+OPT+B` |
| Bookmark manager | `SUPER+ALT+R` | `CMD+OPT+R` |
| Session picker | `SUPER+ALT+L` | `CMD+OPT+L` |

```sh
recall setup            # install, safe to re-run
recall setup status     # show what is configured
recall setup remove     # take the bindings out again
```

Setup refuses to clobber a hotkey you already use, backs up the file it edits,
and only touches its own fenced block. On Omarchy it edits
`~/.config/hypr/bindings.lua` and rolls back if Hyprland rejects the result; on
macOS it edits `~/.hammerspoon/init.lua`.

`recall bookmark active` finds the live `codex` or `claude` process and asks for
a name, first asking which session if several are running. A provider that has
not written to its transcript yet counts as having no active session, because
the working directory is the only signal either provider currently exposes, and
guessing would attach the bookmark to an older session in the same directory.

Hotkeys run with `RECALL_NOTIFY=1` so the result arrives as a desktop
notification. On macOS these go through `osascript`, which reports success even
when notifications are blocked, so a silent hotkey usually means the terminal
lacks notification permission in System Settings rather than that `recall`
failed. If setup could not reload Hammerspoon, reload it from its menu; the
bundled `hs` command only works if your `init.lua` has `require("hs.ipc")`.

### Choosing the terminal on macOS

Setup uses the terminal it was run from, via `TERM_PROGRAM`, and falls back to
Terminal.app. Override it explicitly:

```sh
recall setup --terminal ghostty
```

`terminal`, `ghostty`, and `iterm` are supported; `RECALL_TERMINAL` sets the
default. Re-running with a different `--terminal` rewrites the block in place.
To add another terminal, add an entry to `macTerminals` in `setup_macos.go`
returning Lua that opens a window running the given shell command.

## Storage

Bookmarks live in `$XDG_STATE_HOME/recall/bookmarks.json`, or
`~/.local/state/recall/bookmarks.json`, created mode `0600`. `RECALL_BOOKMARKS`
overrides the path.

Transcripts stay where their provider wrote them, and `recall` only reads them:

- Codex: `$CODEX_HOME/sessions` and `archived_sessions`, else `~/.codex`.
- Claude Code: `$CLAUDE_CONFIG_DIR/projects` and `sessions`, else `~/.claude`.

These formats are not stable public interfaces, so the parsers are deliberately
small, tolerant, and isolated.

## Development

```sh
go test ./...
go vet ./...
gofmt -l .
```

### Releasing

```sh
scripts/release.sh 0.14.0        # or: mise run release 0.14.0
```

The version is pinned in four places because three packaging channels each
restate the git tag in their own format, so the script owns all of them. It
refuses to run on a dirty tree, a branch other than `main`, a `main` that has
diverged from the remote, or a tag that already exists; runs the tests; bumps
`main.go` and `PKGBUILD`; derives `.SRCINFO` from `PKGBUILD`; opens an editor
prefilled with the commits since the last tag for the release notes; then
commits, tags, pushes, and creates the release. Add `--dry-run` to see all of
that without touching anything, or `-n FILE` to take the notes from a file.

CI finishes the job. The `release` workflow refuses to build a tag whose pinned
versions disagree with it, then builds `linux/amd64` with the version stamped in
and attaches the binary and `SHA256SUMS` to the release — the assets `install.sh`
downloads, without which a release cannot be installed on Linux. A second job
computes the source tarball's checksum, repoints `Formula/recall.rb`, and commits
that to `main`, so the formula's `sha256` is never wrong and never typed by hand.
Run `git pull` afterwards to pick it up.

`scripts/sync-packaging.sh` holds the `.SRCINFO` and formula rewrites for both
callers. Both of its passes are idempotent, so running it against an
already-released tag is a safe no-op.
