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

The picker searches the full visible user and assistant transcript text while
displaying a compact session summary. Internal or encrypted provider fields are
not searched.

`recall bookmark` and `recall bookmark list` show the bookmark name in its own
column, sized to the widest name, with the conversation title beside it. Both
views search the full transcript just like the main picker, so a word from the
conversation finds a bookmark even when its name says nothing about it.

Session dates come from the transcript file, so every discovered session has
one. A bookmark marked `[missing]` has no transcript left to read, and falls back
to the title, directory, and date recorded in the bookmark file when it was
created.

## Requirements

- The `codex` and/or `claude` CLI.
- [`fzf`](https://github.com/junegunn/fzf).
- Go 1.23 or newer to build from source.
- [Hammerspoon](https://www.hammerspoon.org/) when using global hotkeys on
  macOS.

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

### macOS

Install the current development version with Homebrew from a clone:

```sh
git clone git@github.com:jborlum/recall.git
cd recall
brew install --HEAD ./Formula/recall.rb
```

For global hotkeys, install Hammerspoon and then configure recall:

```sh
brew install --cask hammerspoon
recall setup
```

Grant Hammerspoon the requested Accessibility and Terminal automation
permissions. If its `hs` command is not installed, reload the Hammerspoon
config from its menu after setup.

Note that `brew install --HEAD ./Formula/recall.rb` builds from the pushed
`main` branch rather than from your working tree, so it will not pick up
uncommitted changes.

### macOS without Homebrew

Nothing in `recall` needs Homebrew. Build it with Go and put it on your `PATH`:

```sh
git clone https://github.com/jborlum/recall.git
cd recall
go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o recall .
install -Dm755 recall "$HOME/.local/bin/recall"
```

`fzf` is the only runtime dependency. If it is not already installed, download a
release binary from [the `fzf` releases
page](https://github.com/junegunn/fzf/releases) and place it on your `PATH`.

For global hotkeys without Homebrew, download the Hammerspoon release zip from
[its releases page](https://github.com/Hammerspoon/Hammerspoon/releases),
unzip it into `/Applications`, launch it once to grant Accessibility
permission, then run `recall setup`.

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
recall setup
```

This installs `SUPER+ALT+B` for bookmarking the active session,
`SUPER+ALT+R` for the bookmark manager, and `SUPER+ALT+L` for the normal
session picker.

The setup command checks for conflicts, backs up `~/.config/hypr/bindings.lua`,
adds only missing bindings, and reloads and validates Hyprland. It is safe to
run more than once. Inspect or remove the integration with:

```sh
recall setup status
recall setup remove
```

On macOS, `recall setup` manages equivalent Hammerspoon bindings:

- `COMMAND+OPTION+B` bookmarks the active session.
- `COMMAND+OPTION+R` opens the bookmark manager.
- `COMMAND+OPTION+L` opens the normal session picker.

Manage the bindings with:

```sh
recall setup status
recall setup remove
```

### Choosing the terminal

`recall setup` opens the commands in the terminal it was run from, detected
through `TERM_PROGRAM`. Pick one explicitly with `--terminal`:

```sh
recall setup --terminal ghostty
```

The supported names are `terminal` (Terminal.app), `ghostty`, and `iterm`
(iTerm2). Terminal.app is used when the current terminal is not recognised.
Re-running `setup` with a different `--terminal` rewrites the managed block in
place, and `recall setup status` reports which terminal is configured.
`RECALL_TERMINAL` sets the default if you would rather not pass the flag.

Terminal.app and iTerm2 are driven with AppleScript. Ghostty refuses to launch
its emulator from its own CLI on macOS, so its bindings go through
`open -na Ghostty.app --args -e` instead.

To add another terminal, add an entry to `macTerminals` in `setup_macos.go`
returning the Lua that opens a window running the given shell command.

If `recall setup` reports that it could not reload Hammerspoon, reload the
config from the Hammerspoon menu. The bundled `hs` command only works when
`init.lua` loads the IPC module, which is not enabled by default:

```lua
require("hs.ipc")
```

The hotkeys run with `RECALL_NOTIFY=1` so results appear as desktop
notifications. On macOS these are delivered by `osascript`, which exits
successfully even when notifications are suppressed, so a silent hotkey usually
means the terminal application has not been granted notification permission in
System Settings > Notifications rather than that `recall` failed.

## How active sessions are found

`recall bookmark active` has to work out which session a running `codex` or
`claude` process belongs to. It tries three signals, in order of reliability:

1. The `CODEX_THREAD_ID`, `CODEX_SESSION_ID`, or `CLAUDE_SESSION_ID` variable,
   read from recall's own environment and from each provider process.
2. An open transcript file descriptor belonging to the process.
3. The process working directory, matched against the newest transcript for that
   provider and directory that was written after the process started.

Neither provider currently exports a session id or holds its transcript open, so
in practice the working directory is what identifies a session. That means a
provider process which has not written anything yet is reported as having no
active session, rather than being attributed to an older session left behind in
the same directory.

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
