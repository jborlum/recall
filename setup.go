package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	setupBegin  = "-- BEGIN recall managed hotkeys"
	setupEnd    = "-- END recall managed hotkeys"
	bookmarkKey = "SUPER + ALT + B"
	managerKey  = "SUPER + ALT + R"
	searchKey   = "SUPER + ALT + L"
)

type bindingState struct {
	bookmark bool
	manager  bool
	search   bool
	legacy   bool
	conflict string
}

func runSetupOmarchy(args []string, out, errOut io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(errOut, "usage: recall setup-omarchy [status|remove]")
		return 2
	}
	path, err := omarchyBindingsPath()
	if err != nil {
		fmt.Fprintf(errOut, "recall: locate Omarchy bindings: %v\n", err)
		return 1
	}
	action := "install"
	if len(args) == 1 {
		action = args[0]
	}
	switch action {
	case "install":
		return installOmarchySetup(path, out, errOut)
	case "status":
		return omarchySetupStatus(path, out, errOut)
	case "remove":
		return removeOmarchySetup(path, out, errOut)
	default:
		fmt.Fprintln(errOut, "usage: recall setup-omarchy [status|remove]")
		return 2
	}
}

func omarchyBindingsPath() (string, error) {
	if explicit := os.Getenv("RECALL_HYPR_BINDINGS"); explicit != "" {
		return explicit, nil
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "hypr", "bindings.lua"), nil
}

func installOmarchySetup(path string, out, errOut io.Writer) int {
	data, mode, err := readBindings(path)
	if err != nil {
		fmt.Fprintf(errOut, "recall: read %s: %v\n", path, err)
		return 1
	}
	binary, err := os.Executable()
	if err != nil {
		fmt.Fprintf(errOut, "recall: locate executable: %v\n", err)
		return 1
	}
	if resolved, resolveErr := filepath.EvalSymlinks(binary); resolveErr == nil {
		binary = resolved
	}
	state := inspectBindings(string(data))
	if state.conflict != "" {
		fmt.Fprintf(errOut, "recall: hotkey conflict: %s\n", state.conflict)
		return 1
	}
	managed := strings.Contains(string(data), setupBegin) && strings.Contains(string(data), setupEnd)
	if state.bookmark && state.manager && state.search && managed && !state.legacy {
		fmt.Fprintln(out, "Recall hotkeys are already configured")
		return 0
	}
	if conflict := systemHotkeyConflict(state); conflict != "" {
		fmt.Fprintf(errOut, "recall: hotkey conflict: %s\n", conflict)
		return 1
	}
	base := data
	if state.bookmark || state.manager || managed {
		base = removeRecallBindings(data)
		state = bindingState{}
	}
	updated := appendManagedBindings(base, binary, state)
	backup, err := replaceBindings(path, data, updated, mode)
	if err != nil {
		fmt.Fprintf(errOut, "recall: install hotkeys: %v\n", err)
		return 1
	}
	if err := reloadAndValidateHyprland(); err != nil {
		if restoreErr := atomicWrite(path, data, mode); restoreErr != nil {
			fmt.Fprintf(errOut, "recall: Hyprland validation failed (%v); rollback also failed: %v\n", err, restoreErr)
			return 1
		}
		_ = reloadHyprland()
		fmt.Fprintf(errOut, "recall: Hyprland validation failed; restored previous config: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, "Installed SUPER+ALT+B (bookmark active), SUPER+ALT+R (bookmark manager), and SUPER+ALT+L (session picker)")
	fmt.Fprintf(out, "Backup: %s\n", backup)
	return 0
}

func omarchySetupStatus(path string, out, errOut io.Writer) int {
	data, _, err := readBindings(path)
	if err != nil {
		fmt.Fprintf(errOut, "recall: read %s: %v\n", path, err)
		return 1
	}
	state := inspectBindings(string(data))
	if state.conflict != "" {
		fmt.Fprintf(out, "conflict: %s\n", state.conflict)
		return 1
	}
	fmt.Fprintf(out, "%s: %s\n", bookmarkKey, setupWord(state.bookmark))
	fmt.Fprintf(out, "%s: %s\n", managerKey, setupWord(state.manager))
	fmt.Fprintf(out, "%s: %s\n", searchKey, setupWord(state.search))
	if state.bookmark && state.manager && state.search {
		return 0
	}
	return 1
}

func removeOmarchySetup(path string, out, errOut io.Writer) int {
	data, mode, err := readBindings(path)
	if err != nil {
		fmt.Fprintf(errOut, "recall: read %s: %v\n", path, err)
		return 1
	}
	updated := removeRecallBindings(data)
	if bytes.Equal(data, updated) {
		fmt.Fprintln(out, "Recall hotkeys are already absent")
		return 0
	}
	backup, err := replaceBindings(path, data, updated, mode)
	if err != nil {
		fmt.Fprintf(errOut, "recall: remove hotkeys: %v\n", err)
		return 1
	}
	if err := reloadAndValidateHyprland(); err != nil {
		if restoreErr := atomicWrite(path, data, mode); restoreErr != nil {
			fmt.Fprintf(errOut, "recall: Hyprland validation failed (%v); rollback also failed: %v\n", err, restoreErr)
			return 1
		}
		_ = reloadHyprland()
		fmt.Fprintf(errOut, "recall: Hyprland validation failed; restored previous config: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, "Removed recall hotkeys")
	fmt.Fprintf(out, "Backup: %s\n", backup)
	return 0
}

func readBindings(path string) ([]byte, os.FileMode, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	return data, info.Mode().Perm(), nil
}

func inspectBindings(contents string) bindingState {
	state := bindingState{}
	for _, call := range bindingCalls(contents) {
		switch {
		case strings.Contains(call.text, `"`+bookmarkKey+`"`):
			if isRecallBookmarkBinding(call.text) {
				state.bookmark = true
				if strings.Contains(call.text, " pin-active\"") {
					state.legacy = true
				}
			} else {
				state.conflict = bookmarkKey + " is already assigned"
			}
		case strings.Contains(call.text, `"`+managerKey+`"`):
			if isRecallManagerBinding(call.text) {
				state.manager = true
				if strings.Contains(call.text, " bookmarks\"") {
					state.legacy = true
				}
			} else {
				state.conflict = managerKey + " is already assigned"
			}
		case strings.Contains(call.text, `"`+searchKey+`"`):
			if isRecallSearchBinding(call.text) {
				state.search = true
			} else {
				state.conflict = searchKey + " is already assigned"
			}
		}
	}
	return state
}

func setupWord(configured bool) string {
	if configured {
		return "configured"
	}
	return "missing"
}

func appendManagedBindings(data []byte, binary string, state bindingState) []byte {
	result := strings.TrimRight(string(data), "\n") + "\n\n" + setupBegin + "\n"
	if !state.bookmark {
		result += omarchyBinding(bookmarkKey, "Bookmark AI conversation", binary+" bookmark active")
	}
	if !state.manager {
		result += omarchyBinding(managerKey, "Recall bookmarked conversation", binary+" bookmark")
	}
	if !state.search {
		result += omarchyBinding(searchKey, "Recall session picker", binary)
	}
	result += setupEnd + "\n"
	return []byte(result)
}

func omarchyBinding(key, description, command string) string {
	return fmt.Sprintf("o.bind(\n  %q,\n  %q,\n  %q\n)\n\n", key, description,
		"setsid uwsm-app -- xdg-terminal-exec --app-id=org.omarchy.terminal --title=Recall -e env RECALL_NOTIFY=1 "+command)
}

type bindingCall struct {
	start int
	end   int
	text  string
}

func bindingCalls(contents string) []bindingCall {
	lines := strings.SplitAfter(contents, "\n")
	offsets := make([]int, len(lines))
	offset := 0
	for i, line := range lines {
		offsets[i] = offset
		offset += len(line)
	}
	var calls []bindingCall
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") || (!strings.Contains(line, "o.bind(") && !strings.Contains(line, "hl.bind(")) {
			continue
		}
		depth := 0
		started := false
		endLine := index
		for ; endLine < len(lines); endLine++ {
			for _, char := range lines[endLine] {
				switch char {
				case '(':
					depth++
					started = true
				case ')':
					depth--
				}
			}
			if started && depth <= 0 {
				break
			}
		}
		end := len(contents)
		if endLine+1 < len(offsets) {
			end = offsets[endLine+1]
		}
		calls = append(calls, bindingCall{start: offsets[index], end: end, text: contents[offsets[index]:end]})
		index = endLine
	}
	return calls
}

func removeRecallBindings(data []byte) []byte {
	contents := string(data)
	if begin := strings.Index(contents, setupBegin); begin >= 0 {
		if end := strings.Index(contents[begin:], setupEnd); end >= 0 {
			end += begin + len(setupEnd)
			if end < len(contents) && contents[end] == '\n' {
				end++
			}
			contents = strings.TrimRight(contents[:begin], "\n") + "\n" + strings.TrimLeft(contents[end:], "\n")
		}
	}
	calls := bindingCalls(contents)
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		known := (strings.Contains(call.text, `"`+bookmarkKey+`"`) && isRecallBookmarkBinding(call.text)) ||
			(strings.Contains(call.text, `"`+managerKey+`"`) && isRecallManagerBinding(call.text)) ||
			(strings.Contains(call.text, `"`+searchKey+`"`) && isRecallSearchBinding(call.text))
		if known {
			contents = contents[:call.start] + contents[call.end:]
		}
	}
	return []byte(strings.TrimRight(contents, "\n") + "\n")
}

func isRecallBookmarkBinding(call string) bool {
	return strings.Contains(call, "Bookmark AI conversation") &&
		(strings.Contains(call, " bookmark active\"") || strings.Contains(call, " pin-active\""))
}

func isRecallManagerBinding(call string) bool {
	return strings.Contains(call, "Recall bookmarked conversation") &&
		(strings.Contains(call, " bookmark\"") || strings.Contains(call, " bookmarks\""))
}

func isRecallSearchBinding(call string) bool {
	return strings.Contains(call, "Recall session picker")
}

func replaceBindings(path string, previous, updated []byte, mode os.FileMode) (string, error) {
	backup := path + ".bak.recall-" + time.Now().Format("20060102-150405.000000000")
	file, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return "", err
	}
	if _, err := file.Write(previous); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := atomicWrite(path, updated, mode); err != nil {
		return "", err
	}
	return backup, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".recall-bindings-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func systemHotkeyConflict(local bindingState) string {
	if os.Getenv("RECALL_SETUP_NO_RELOAD") != "" {
		return ""
	}
	path, err := exec.LookPath("omarchy")
	if err != nil {
		return ""
	}
	output, err := exec.Command(path, "menu", "keybindings", "--print").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(output), "\n") {
		normalized := strings.ReplaceAll(strings.TrimSpace(line), " + ", " ")
		if !local.bookmark && strings.HasPrefix(normalized, "SUPER ALT B ") && !strings.Contains(line, "Bookmark AI conversation") {
			return strings.TrimSpace(line)
		}
		if !local.manager && strings.HasPrefix(normalized, "SUPER ALT R ") && !strings.Contains(line, "Recall bookmarked conversation") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func reloadAndValidateHyprland() error {
	if os.Getenv("RECALL_SETUP_NO_RELOAD") != "" {
		return nil
	}
	if err := reloadHyprland(); err != nil {
		return err
	}
	path, err := exec.LookPath("hyprctl")
	if err != nil {
		return errors.New("hyprctl not found")
	}
	output, err := exec.Command(path, "configerrors").CombinedOutput()
	if err != nil {
		return fmt.Errorf("hyprctl configerrors: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if message := strings.TrimSpace(string(output)); message != "" {
		return errors.New(message)
	}
	return nil
}

func reloadHyprland() error {
	path, err := exec.LookPath("hyprctl")
	if err != nil {
		return errors.New("hyprctl not found")
	}
	output, err := exec.Command(path, "reload").CombinedOutput()
	if err != nil {
		return fmt.Errorf("hyprctl reload: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
