//go:build darwin

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	macSetupBegin = "-- BEGIN recall managed macOS hotkeys"
	macSetupEnd   = "-- END recall managed macOS hotkeys"
	// macTerminalMarker records the chosen terminal inside the managed block so
	// that status can report it and install can switch terminals in place.
	macTerminalMarker = "-- recall terminal: "
	macSetupUsage     = "usage: recall setup [--terminal NAME] [status|remove]"
)

// macTerminals maps the names accepted by --terminal to the Lua that opens a new
// window running shell.
var macTerminals = map[string]func(shell string) string{
	"terminal": func(shell string) string {
		return luaAppleScript("tell application \"Terminal\"\nactivate\ndo script " + macAppleScriptQuote(shell) + "\nend tell")
	},
	"iterm": func(shell string) string {
		return luaAppleScript("tell application \"iTerm\"\nactivate\ncreate window with default profile command " + macAppleScriptQuote(shell) + "\nend tell")
	},
	"ghostty": func(shell string) string {
		// Ghostty refuses to launch the emulator from its own CLI on macOS, so the
		// window is opened through open(1) instead of AppleScript.
		return luaOpenApplication("Ghostty.app", "-e", loginShell(), "-l", "-i", "-c", shell)
	},
}

// loginShell reports the shell that hotkey commands should run in. Terminal.app
// and iTerm2 open an interactive login shell themselves, but open(1) does not, so
// terminals launched that way have to name one. It must be interactive as well as
// a login shell: on a typical macOS setup the entries that locate fzf and the
// provider CLIs live in an interactive startup file such as ~/.zshrc, and a
// non-interactive shell would leave them off PATH.
func loginShell() string {
	if shell := os.Getenv("SHELL"); filepath.IsAbs(shell) {
		return shell
	}
	return "/bin/sh"
}

func isPlatformSetupCommand(value string) bool { return value == "setup" }

func runPlatformSetup(args []string, out, errOut io.Writer) int {
	return runSetupMacOS(args, out, errOut)
}

func platformSetupUsage() string {
	return "  recall setup [--terminal NAME] [status|remove]\n                                    manage macOS global hotkeys\n"
}

func runSetupMacOS(args []string, out, errOut io.Writer) int {
	terminal := defaultMacTerminal()
	var positional []string
	for index := 0; index < len(args); index++ {
		switch argument := args[index]; {
		case argument == "--terminal":
			if index+1 >= len(args) {
				fmt.Fprintln(errOut, "recall: --terminal requires a name")
				return 2
			}
			index++
			terminal = args[index]
		case strings.HasPrefix(argument, "--terminal="):
			terminal = strings.TrimPrefix(argument, "--terminal=")
		default:
			positional = append(positional, argument)
		}
	}
	if len(positional) > 1 {
		fmt.Fprintln(errOut, macSetupUsage)
		return 2
	}
	if _, known := macTerminals[terminal]; !known {
		fmt.Fprintf(errOut, "recall: unknown terminal %q; choose one of %s\n", terminal, strings.Join(macTerminalNames(), ", "))
		return 2
	}
	action := "install"
	if len(positional) == 1 {
		action = positional[0]
	}
	path, err := hammerspoonConfigPath()
	if err != nil {
		fmt.Fprintf(errOut, "recall: locate Hammerspoon config: %v\n", err)
		return 1
	}
	switch action {
	case "install":
		return installMacOSSetup(path, terminal, out, errOut)
	case "status":
		return macOSSetupStatus(path, out, errOut)
	case "remove":
		return removeMacOSSetup(path, out, errOut)
	default:
		fmt.Fprintln(errOut, macSetupUsage)
		return 2
	}
}

func macTerminalNames() []string {
	names := make([]string, 0, len(macTerminals))
	for name := range macTerminals {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// defaultMacTerminal picks the terminal recall is running in, so that setup run
// from Ghostty configures Ghostty rather than Terminal.app.
func defaultMacTerminal() string {
	if explicit := os.Getenv("RECALL_TERMINAL"); explicit != "" {
		return explicit
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "ghostty":
		return "ghostty"
	case "iTerm.app":
		return "iterm"
	}
	return "terminal"
}

// configuredMacTerminal reports the terminal recorded in an installed block. The
// terminal is empty for blocks written before the marker existed.
func configuredMacTerminal(data []byte) (string, bool) {
	contents := string(data)
	if !strings.Contains(contents, macSetupBegin) || !strings.Contains(contents, macSetupEnd) {
		return "", false
	}
	begin := strings.Index(contents, macTerminalMarker)
	if begin < 0 {
		return "", true
	}
	line := contents[begin+len(macTerminalMarker):]
	if end := strings.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	return strings.TrimSpace(line), true
}

func luaAppleScript(script string) string {
	return "hs.osascript.applescript(" + strconv.Quote(script) + ")"
}

func luaOpenApplication(application string, arguments ...string) string {
	quoted := []string{strconv.Quote("-na"), strconv.Quote(application), strconv.Quote("--args")}
	for _, argument := range arguments {
		quoted = append(quoted, strconv.Quote(argument))
	}
	return "hs.task.new(\"/usr/bin/open\", nil, {" + strings.Join(quoted, ", ") + "}):start()"
}

func hammerspoonConfigPath() (string, error) {
	if explicit := os.Getenv("RECALL_HAMMERSPOON_CONFIG"); explicit != "" {
		return explicit, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hammerspoon", "init.lua"), nil
}

func installMacOSSetup(path, terminal string, out, errOut io.Writer) int {
	data, mode, exists, err := readOptionalConfig(path)
	if err != nil {
		fmt.Fprintf(errOut, "recall: read %s: %v\n", path, err)
		return 1
	}
	_, configured := configuredMacTerminal(data)
	withoutRecall := removeMarkedBlock(data, macSetupBegin, macSetupEnd)
	binary, err := os.Executable()
	if err != nil {
		fmt.Fprintf(errOut, "recall: locate executable: %v\n", err)
		return 1
	}
	updated := appendMacOSBindings(withoutRecall, binary, terminal)
	// Compare the rendered block rather than just the terminal name, so that an
	// upgrade which changes the generated Lua still refreshes the config.
	if exists && bytes.Equal(updated, data) {
		fmt.Fprintf(out, "Recall macOS hotkeys are already configured for %s\n", terminal)
		return 0
	}
	if conflict := hammerspoonConflict(string(withoutRecall)); conflict != "" {
		fmt.Fprintf(errOut, "recall: hotkey conflict: %s\n", conflict)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		fmt.Fprintf(errOut, "recall: create Hammerspoon config directory: %v\n", err)
		return 1
	}
	backup := ""
	if exists {
		backup, err = replaceBindings(path, data, updated, mode)
	} else {
		err = atomicWrite(path, updated, mode)
	}
	if err != nil {
		fmt.Fprintf(errOut, "recall: install macOS hotkeys: %v\n", err)
		return 1
	}
	reloaded := reloadHammerspoon()
	verb := "Installed"
	if configured {
		verb = "Reconfigured"
	}
	fmt.Fprintf(out, "%s COMMAND+OPTION+B (bookmark active), COMMAND+OPTION+R (bookmark manager), and COMMAND+OPTION+L (session picker) in %s\n", verb, terminal)
	if backup != "" {
		fmt.Fprintf(out, "Backup: %s\n", backup)
	}
	if !reloaded {
		fmt.Fprintln(out, "Reload the Hammerspoon config to activate the hotkeys")
	}
	return 0
}

func macOSSetupStatus(path string, out, errOut io.Writer) int {
	data, _, _, err := readOptionalConfig(path)
	if err != nil {
		fmt.Fprintf(errOut, "recall: read %s: %v\n", path, err)
		return 1
	}
	terminal, configured := configuredMacTerminal(data)
	fmt.Fprintf(out, "COMMAND + OPTION + B: %s\n", setupWord(configured))
	fmt.Fprintf(out, "COMMAND + OPTION + R: %s\n", setupWord(configured))
	fmt.Fprintf(out, "COMMAND + OPTION + L: %s\n", setupWord(configured))
	if !configured {
		return 1
	}
	if terminal == "" {
		terminal = "unknown"
	}
	fmt.Fprintf(out, "terminal: %s\n", terminal)
	return 0
}

func removeMacOSSetup(path string, out, errOut io.Writer) int {
	data, mode, exists, err := readOptionalConfig(path)
	if err != nil {
		fmt.Fprintf(errOut, "recall: read %s: %v\n", path, err)
		return 1
	}
	if !exists || !strings.Contains(string(data), macSetupBegin) {
		fmt.Fprintln(out, "Recall macOS hotkeys are already absent")
		return 0
	}
	updated := removeMarkedBlock(data, macSetupBegin, macSetupEnd)
	backup, err := replaceBindings(path, data, updated, mode)
	if err != nil {
		fmt.Fprintf(errOut, "recall: remove macOS hotkeys: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, "Removed recall macOS hotkeys")
	fmt.Fprintf(out, "Backup: %s\n", backup)
	if !reloadHammerspoon() {
		fmt.Fprintln(out, "Reload the Hammerspoon config to deactivate the hotkeys")
	}
	return 0
}

func readOptionalConfig(path string) ([]byte, os.FileMode, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0600, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, false, err
	}
	return data, info.Mode().Perm(), true, nil
}

func appendMacOSBindings(data []byte, binary, terminal string) []byte {
	launch := macTerminals[terminal]
	command := func(arguments string) string {
		shell := "env RECALL_NOTIFY=1 " + shellQuote(binary)
		if arguments != "" {
			shell += " " + arguments
		}
		return launch(shell)
	}
	block := macSetupBegin + "\n" + macTerminalMarker + terminal + `
hs.hotkey.bind({"cmd", "alt"}, "B", function()
  ` + command("bookmark active") + `
end)

hs.hotkey.bind({"cmd", "alt"}, "R", function()
  ` + command("bookmark") + `
end)

hs.hotkey.bind({"cmd", "alt"}, "L", function()
  ` + command("") + `
end)
` + macSetupEnd + "\n"
	base := strings.TrimRight(string(data), "\n")
	if base != "" {
		base += "\n\n"
	}
	return []byte(base + block)
}

func removeMarkedBlock(data []byte, beginMarker, endMarker string) []byte {
	contents := string(data)
	begin := strings.Index(contents, beginMarker)
	if begin < 0 {
		return data
	}
	endRelative := strings.Index(contents[begin:], endMarker)
	if endRelative < 0 {
		return data
	}
	end := begin + endRelative + len(endMarker)
	if end < len(contents) && contents[end] == '\n' {
		end++
	}
	contents = strings.TrimRight(contents[:begin], "\n") + "\n" + strings.TrimLeft(contents[end:], "\n")
	return []byte(contents)
}

func hammerspoonConflict(contents string) string {
	for _, key := range []string{"B", "R", "L"} {
		pattern := `(?is)hs\.hotkey\.bind\s*\(\s*\{([^}]*)\}\s*,\s*["']` + key + `["']`
		match := regexp.MustCompile(pattern).FindStringSubmatch(contents)
		if len(match) > 1 && strings.Contains(strings.ToLower(match[1]), "cmd") && strings.Contains(strings.ToLower(match[1]), "alt") {
			return "COMMAND + OPTION + " + key + " is already assigned"
		}
	}
	return ""
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func macAppleScriptQuote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func reloadHammerspoon() bool {
	if os.Getenv("RECALL_SETUP_NO_RELOAD") != "" {
		return true
	}
	hs, err := exec.LookPath("hs")
	if err != nil {
		return false
	}
	return exec.Command(hs, "-c", "hs.reload()").Run() == nil
}
