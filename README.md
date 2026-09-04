# recall

Find, bookmark, resume, and fork your local Codex and Claude Code conversations,
from any directory you work in.

`recall` reads the providers' own transcript files and hands resuming and forking
back to their official CLIs. There is no daemon, index, database, or network
access. You need the `codex` and/or `claude` CLI, plus
[`fzf`](https://github.com/junegunn/fzf) for the picker.

## Install

### macOS

The repository doubles as its own Homebrew tap:

```sh
brew tap jborlum/recall https://github.com/jborlum/recall.git
brew install jborlum/recall/recall
```

Install by full name; Homebrew rejects a formula given by path. `--HEAD` builds
`main` instead of the latest release, and `brew upgrade --fetch-HEAD` updates it.

### Linux

A static binary, for machines with no Go toolchain:

```sh
curl -fsSL https://raw.githubusercontent.com/jborlum/recall/main/install.sh | sh
```

It verifies the release checksum and installs to `~/.local/bin`. Re-run it to
update; `-f` reinstalls, `PREFIX` picks another directory, `RECALL_VERSION` picks
a specific tag. Get `fzf` from your package manager or
[its releases](https://github.com/junegunn/fzf/releases).

On Arch or Omarchy, let pacman own `/usr/bin/recall` instead. `makepkg` builds
the tag named in `PKGBUILD`, not your working tree:

```sh
git clone https://github.com/jborlum/recall.git && cd recall && makepkg -si
```

### From source

Needs Go 1.23 or newer, and works the same on macOS and Linux:

```sh
git clone https://github.com/jborlum/recall.git && cd recall
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o recall .
mkdir -p ~/.local/bin && install -m755 recall ~/.local/bin/recall
```

## Usage

```sh
recall                         # search every session, then resume one
recall refresh token           # search titles and full transcript text
recall --print cache           # print matches instead of opening one
recall --provider codex auth   # only Codex sessions
recall --cwd .                 # only sessions from this directory tree
recall --limit 200 refactor    # widen the 50-result default
```

In the picker, `Enter` resumes, `Ctrl-F` forks, and `Esc` closes.

Search covers the whole visible conversation, not just the title, so a
remembered phrase finds a session its title never hints at; encrypted and
internal provider fields are skipped. The panel below the list shows every match
with the lines around it, numbered `3/12`, with your terms marked.
`Shift-Up`/`Shift-Down` and `Alt-Up`/`Alt-Down` walk through them and stop at
either end; `Ctrl-/` hides the panel. With no search term the panel shows the
conversation from the start.

### Bookmarks

```sh
recall bookmark                       # resume, fork, or delete a bookmark
recall bookmark add auth-design token # name a matching session
recall bookmark active                # name the session running right now
recall bookmark list                  # print bookmarks
recall bookmark remove auth-design    # delete the bookmark, keep the session
recall doctor                         # report discovery and stale bookmarks
```

Bookmark views search the full transcript too, so a word from the conversation
finds a bookmark whose name says nothing about it. `Ctrl-D` deletes the selected
bookmark after confirming. One marked `[missing]` has no transcript left and
falls back to what was recorded when it was created.

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

Setup edits `~/.config/hypr/bindings.lua` on Omarchy and
`~/.hammerspoon/init.lua` on macOS, touching only its own fenced block. It backs
up the file first, refuses to clobber a hotkey you already use, and rolls back if
Hyprland rejects the result. macOS hotkeys need
[Hammerspoon](https://www.hammerspoon.org/):

```sh
brew install --cask hammerspoon
recall setup
```

Setup uses the terminal it was run from and falls back to Terminal.app;
`--terminal` or `RECALL_TERMINAL` selects `terminal`, `ghostty`, or `iterm`.

Results arrive as desktop notifications. A silent hotkey on macOS usually means
the terminal lacks notification permission in System Settings rather than that
`recall` failed.

## Storage

Bookmarks live in `$XDG_STATE_HOME/recall/bookmarks.json`, or
`~/.local/state/recall/bookmarks.json`, created mode `0600`; `RECALL_BOOKMARKS`
overrides the path. Transcripts stay where their provider wrote them, and
`recall` only ever reads them:

- Codex: `$CODEX_HOME/sessions` and `archived_sessions`, else `~/.codex`.
- Claude Code: `$CLAUDE_CONFIG_DIR/projects` and `sessions`, else `~/.claude`.

## Development

```sh
go test ./...
go vet ./...
gofmt -l .
```

`scripts/release.sh 0.15.0` cuts a release: it checks the tree, runs the tests,
bumps every pinned version, writes the notes, and tags and pushes. CI attaches
the Linux binary and commits the Homebrew formula bump, so `git pull` afterwards.
`--dry-run` shows the plan without touching anything.
