# recall

`recall` is a fast, local-only CLI for finding, bookmarking, resuming, and
forking Codex and Claude Code conversations across working directories.

It reads the providers' local transcripts directly and hands resume/fork
operations back to their official CLIs. There is no daemon, index, database,
or network service.

## Features

- Search Codex and Claude Code conversations by title, content, provider, or
  working directory.
- Bookmark the conversation attached to the active provider process.
- Resume, fork, or delete bookmarks from a single `fzf` picker.
- Resume and fork sessions through the provider CLI.
- Keep all bookmark data in one small local JSON file.

## Requirements

- The `codex` and/or `claude` CLI.
- [`fzf`](https://github.com/junegunn/fzf).
- Go 1.23 or newer to build from source.

## Install

### Arch Linux / Omarchy

Clone the repository and build a native pacman package:

```sh
git clone git@github.com:jborlum/recall.git
cd recall
makepkg -si
```

This installs `recall` at `/usr/bin/recall`; pacman then owns the binary and can
upgrade or remove it normally. Pacman installs the required `fzf` dependency.

### Other Linux distributions

Build directly with Go:

```sh
git clone https://github.com/jborlum/recall.git
cd recall
go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o recall .
install -Dm755 recall "$HOME/.local/bin/recall"
```

Or use the included `mise.toml`:

```sh
mise install
mise exec -- go test ./...
mise exec -- env CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o recall .
```

## Usage

```sh
recall                         # search all sessions and resume one
recall refresh token           # full-text search, then resume
recall --print cache           # print results without opening anything
recall --provider codex auth   # restrict results to Codex
recall --cwd .                 # restrict results to this directory tree
recall experiment              # search; press Ctrl+F to fork the selection

recall bookmark add auth-design token # bookmark a matching session
recall bookmark active           # bookmark the currently active session
recall bookmark                 # resume, fork, or delete bookmarks
recall bookmark list            # print bookmarks
recall bookmark remove auth-design # remove only the bookmark
recall doctor                  # report discovery and stale bookmarks
```

Global flags must appear before the command or query. Run `recall help` for the
complete command summary.

In the main picker, press `Enter` to resume or `Ctrl+F` to fork the selected
session. The bookmark manager adds `Ctrl+D` to delete a bookmark after
confirmation. Press `Esc` to close either picker.

## Global hotkeys

`recall bookmark active` detects live Codex and Claude processes. If one session is
active, it opens the bookmark-name prompt immediately; if several are active,
it first asks which one to bookmark.

For Omarchy/Hyprland, install the global hotkeys with:

```sh
recall setup-omarchy
```

The setup command checks for conflicts, backs up `~/.config/hypr/bindings.lua`,
adds only missing bindings, and reloads and validates Hyprland. It is safe to
run more than once. Inspect or remove the integration with:

```sh
recall setup-omarchy status
recall setup-omarchy remove
```

## Storage and privacy

Bookmarks are stored at `$XDG_STATE_HOME/recall/bookmarks.json`, or at
`~/.local/state/recall/bookmarks.json` when `XDG_STATE_HOME` is unset. The file
is created with mode `0600`. Set `RECALL_BOOKMARKS` to override its location.

Session transcripts remain where their provider put them:

- Codex: `$CODEX_HOME/sessions` and `$CODEX_HOME/archived_sessions`, falling
  back to `~/.codex`.
- Claude Code: `$CLAUDE_CONFIG_DIR/projects` and `$CLAUDE_CONFIG_DIR/sessions`,
  falling back to `~/.claude`.

Provider transcript formats are not stable public interfaces. The parsers are
therefore deliberately small and isolated; `recall` never edits transcript or
provider state.

## Development

```sh
go test ./...
go vet ./...
```
