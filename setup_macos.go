//go:build darwin

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	macSetupBegin = "-- BEGIN recall managed macOS hotkeys"
	macSetupEnd   = "-- END recall managed macOS hotkeys"
)

func isPlatformSetupCommand(value string) bool { return value == "setup-macos" }

func runPlatformSetup(args []string, out, errOut io.Writer) int {
	return runSetupMacOS(args, out, errOut)
}

func platformSetupUsage() string {
	return "  recall setup-macos [status|remove]\n                                    manage macOS global hotkeys\n"
}

func runSetupMacOS(args []string, out, errOut io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(errOut, "usage: recall setup-macos [status|remove]")
		return 2
	}
	action := "install"
	if len(args) == 1 {
		action = args[0]
	}
	path, err := hammerspoonConfigPath()
	if err != nil {
		fmt.Fprintf(errOut, "recall: locate Hammerspoon config: %v\n", err)
		return 1
	}
	switch action {
	case "install":
		return installMacOSSetup(path, out, errOut)
	case "status":
		return macOSSetupStatus(path, out, errOut)
	case "remove":
		return removeMacOSSetup(path, out, errOut)
	default:
		fmt.Fprintln(errOut, "usage: recall setup-macos [status|remove]")
		return 2
	}
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

func installMacOSSetup(path string, out, errOut io.Writer) int {
	data, mode, exists, err := readOptionalConfig(path)
	if err != nil {
		fmt.Fprintf(errOut, "recall: read %s: %v\n", path, err)
		return 1
	}
	if strings.Contains(string(data), macSetupBegin) && strings.Contains(string(data), macSetupEnd) {
		fmt.Fprintln(out, "Recall macOS hotkeys are already configured")
		return 0
	}
	withoutRecall := removeMarkedBlock(data, macSetupBegin, macSetupEnd)
	if conflict := hammerspoonConflict(string(withoutRecall)); conflict != "" {
		fmt.Fprintf(errOut, "recall: hotkey conflict: %s\n", conflict)
		return 1
	}
	binary, err := os.Executable()
	if err != nil {
		fmt.Fprintf(errOut, "recall: locate executable: %v\n", err)
		return 1
	}
	updated := appendMacOSBindings(withoutRecall, binary)
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
	fmt.Fprintln(out, "Installed COMMAND+OPTION+B (bookmark active), COMMAND+OPTION+R (bookmark manager), and COMMAND+OPTION+L (session picker)")
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
	configured := strings.Contains(string(data), macSetupBegin) && strings.Contains(string(data), macSetupEnd)
	fmt.Fprintf(out, "COMMAND + OPTION + B: %s\n", setupWord(configured))
	fmt.Fprintf(out, "COMMAND + OPTION + R: %s\n", setupWord(configured))
	fmt.Fprintf(out, "COMMAND + OPTION + L: %s\n", setupWord(configured))
	if configured {
		return 0
	}
	return 1
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

func appendMacOSBindings(data []byte, binary string) []byte {
	command := func(arguments string) string {
		shell := "env RECALL_NOTIFY=1 " + shellQuote(binary)
		if arguments != "" {
			shell += " " + arguments
		}
		script := "tell application \"Terminal\"\nactivate\ndo script " + macAppleScriptQuote(shell) + "\nend tell"
		return strconv.Quote(script)
	}
	block := macSetupBegin + `
local function recallRun(script)
  hs.osascript.applescript(script)
end

hs.hotkey.bind({"cmd", "alt"}, "B", function()
  recallRun(` + command("bookmark active") + `)
end)

hs.hotkey.bind({"cmd", "alt"}, "R", function()
  recallRun(` + command("bookmark") + `)
end)

hs.hotkey.bind({"cmd", "alt"}, "L", function()
  recallRun(` + command("") + `)
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
