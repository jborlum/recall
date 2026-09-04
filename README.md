# recall

Find, bookmark, resume, and fork your local Codex and Claude Code conversations.

`recall` reads the providers' own transcript files and hands resuming and forking
back to their official CLIs. You need the `codex` and/or `claude` CLI, plus
[`fzf`](https://github.com/junegunn/fzf) for the picker.

## Install

### macOS

The repository doubles as its own Homebrew tap:

```sh
brew tap jborlum/recall https://github.com/jborlum/recall.git
brew install jborlum/recall/recall
```

### Linux

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

Search covers the whole visible conversation; encrypted and
internal provider fields are skipped.

### Bookmarks

```sh
recall bookmark                       # resume, fork, or delete a bookmark
recall bookmark add auth-design token # name a matching session
recall bookmark active                # name the session running right now
recall bookmark list                  # print bookmarks
recall bookmark remove auth-design    # delete the bookmark, keep the session
recall doctor                         # report discovery and stale bookmarks
```

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

macOS hotkeys need [Hammerspoon](https://www.hammerspoon.org/):

```sh
brew install --cask hammerspoon
recall setup
```

Setup uses the terminal it was run from and falls back to Terminal.app;
`--terminal` or `RECALL_TERMINAL` selects `terminal`, `ghostty`, or `iterm`.

## Storage

Bookmarks live in `$XDG_STATE_HOME/recall/bookmarks.json`, or
`~/.local/state/recall/bookmarks.json`; `RECALL_BOOKMARKS` overrides the path. 

- Codex: `$CODEX_HOME/sessions` and `archived_sessions`, else `~/.codex`.
- Claude Code: `$CLAUDE_CONFIG_DIR/projects` and `sessions`, else `~/.claude`.
