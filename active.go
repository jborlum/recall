package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func pinActive(sessions []session, bookmarks map[string]bookmark, args []string, opts options, in io.Reader, out, errOut io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(errOut, "usage: recall pin-active [NAME]")
		return 2
	}
	active := detectActiveSessions(sessions)
	if len(active) == 0 {
		message := "No active Codex or Claude session found"
		fmt.Fprintf(errOut, "recall: %s\n", strings.ToLower(message[:1])+message[1:])
		notify(message)
		return 1
	}

	selected := active[0]
	if len(active) > 1 {
		if !terminalIO(in, out) {
			fmt.Fprintln(errOut, "recall: multiple active sessions; run interactively to choose one")
			printSessions(out, active, opts.limit)
			return 1
		}
		var err error
		selected, err = pick(active, opts, in, out, errOut)
		if err != nil {
			if !errors.Is(err, errCancelled) {
				fmt.Fprintf(errOut, "recall: %v\n", err)
				return 1
			}
			return 130
		}
	}

	if selected.Label != "" {
		message := fmt.Sprintf("Already pinned as %s", selected.Label)
		fmt.Fprintln(out, message)
		notify(message)
		return 0
	}

	label := ""
	if len(args) == 1 {
		label = args[0]
	} else {
		if !terminalIO(in, out) {
			fmt.Fprintln(errOut, "recall: bookmark name required without a terminal")
			return 2
		}
		var err error
		label, err = askBookmarkName(in, out, selected, bookmarks)
		if err != nil {
			fmt.Fprintf(errOut, "recall: %v\n", err)
			return 1
		}
	}
	if label == "" {
		fmt.Fprintln(errOut, "recall: bookmark name cannot be empty")
		return 2
	}
	if _, exists := bookmarks[label]; exists {
		fmt.Fprintf(errOut, "recall: bookmark %q already exists\n", label)
		return 1
	}
	bookmarks[label] = newBookmark(selected)
	if err := saveBookmarks(bookmarks); err != nil {
		fmt.Fprintf(errOut, "recall: save bookmarks: %v\n", err)
		return 1
	}
	message := fmt.Sprintf("Pinned %s", label)
	fmt.Fprintf(out, "%s -> %s:%s\n", message, selected.Provider, selected.ID)
	notify(message)
	return 0
}

func newBookmark(item session) bookmark {
	return bookmark{
		Provider: item.Provider, SessionID: item.ID, CreatedAt: time.Now().UTC(),
		CachedTitle: item.Title, CachedCWD: item.CWD,
	}
}

func askBookmarkName(in io.Reader, out io.Writer, selected session, bookmarks map[string]bookmark) (string, error) {
	suggestion := availableLabel(slug(selected.Title), bookmarks)
	if suggestion == "" {
		suggestion = availableLabel(selected.Provider+"-session", bookmarks)
	}
	fmt.Fprintf(out, "Bookmark name [%s]: ", suggestion)
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return suggestion, nil
	}
	if _, exists := bookmarks[answer]; exists {
		return "", fmt.Errorf("bookmark %q already exists", answer)
	}
	return answer, nil
}

func slug(value string) string {
	var result strings.Builder
	dash := false
	for _, char := range strings.ToLower(value) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			if dash && result.Len() > 0 {
				result.WriteByte('-')
			}
			result.WriteRune(char)
			dash = false
		} else {
			dash = true
		}
		if result.Len() >= 36 {
			break
		}
	}
	return strings.Trim(result.String(), "-")
}

func availableLabel(base string, bookmarks map[string]bookmark) string {
	if base == "" {
		return ""
	}
	if _, exists := bookmarks[base]; !exists {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := base + "-" + strconv.Itoa(suffix)
		if _, exists := bookmarks[candidate]; !exists {
			return candidate
		}
	}
}

func detectActiveSessions(sessions []session) []session {
	byID := make(map[string]int, len(sessions)*2)
	byPath := make(map[string]int, len(sessions))
	for index, item := range sessions {
		byID[item.Provider+"\x00"+item.ID] = index
		if resolved, err := filepath.EvalSymlinks(item.Path); err == nil {
			byPath[resolved] = index
		} else {
			byPath[filepath.Clean(item.Path)] = index
		}
	}
	active := map[int]bool{}
	for _, key := range []struct{ provider, value string }{
		{"codex", os.Getenv("CODEX_THREAD_ID")},
		{"codex", os.Getenv("CODEX_SESSION_ID")},
		{"claude", os.Getenv("CLAUDE_SESSION_ID")},
	} {
		if index, ok := byID[key.provider+"\x00"+key.value]; ok && key.value != "" {
			active[index] = true
		}
	}

	procRoot := os.Getenv("RECALL_PROC_ROOT")
	if procRoot == "" {
		procRoot = "/proc"
	}
	entries, _ := os.ReadDir(procRoot)
	fallbacks := map[string]int{}
	for _, entry := range entries {
		if !entry.IsDir() || !numeric(entry.Name()) {
			continue
		}
		processRoot := filepath.Join(procRoot, entry.Name())
		matched := false
		if data, err := os.ReadFile(filepath.Join(processRoot, "environ")); err == nil {
			for _, field := range strings.Split(string(data), "\x00") {
				pair := strings.SplitN(field, "=", 2)
				if len(pair) != 2 {
					continue
				}
				provider := ""
				switch pair[0] {
				case "CODEX_THREAD_ID", "CODEX_SESSION_ID":
					provider = "codex"
				case "CLAUDE_SESSION_ID":
					provider = "claude"
				}
				if index, ok := byID[provider+"\x00"+pair[1]]; ok && provider != "" {
					active[index], matched = true, true
				}
			}
		}

		provider := processProvider(processRoot)
		if provider == "" {
			continue
		}
		if descriptors, err := os.ReadDir(filepath.Join(processRoot, "fd")); err == nil {
			for _, descriptor := range descriptors {
				target, err := os.Readlink(filepath.Join(processRoot, "fd", descriptor.Name()))
				if err != nil {
					continue
				}
				if resolved, err := filepath.EvalSymlinks(target); err == nil {
					target = resolved
				}
				if index, ok := byPath[filepath.Clean(target)]; ok {
					active[index], matched = true, true
				}
			}
		}
		if matched {
			continue
		}
		cwd, err := os.Readlink(filepath.Join(processRoot, "cwd"))
		if err != nil {
			continue
		}
		fallbacks[provider+"\x00"+cwd]++
	}
	for key, count := range fallbacks {
		parts := strings.SplitN(key, "\x00", 2)
		var candidates []int
		for index, item := range sessions {
			if item.Provider == parts[0] && !item.Archived && samePath(item.CWD, parts[1]) {
				candidates = append(candidates, index)
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			return sessions[candidates[i]].Updated.After(sessions[candidates[j]].Updated)
		})
		if len(candidates) < count {
			count = len(candidates)
		}
		for _, index := range candidates[:count] {
			active[index] = true
		}
	}

	result := make([]session, 0, len(active))
	for index := range active {
		result = append(result, sessions[index])
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Updated.After(result[j].Updated) })
	return result
}

func numeric(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func processProvider(root string) string {
	data, _ := os.ReadFile(filepath.Join(root, "comm"))
	name := strings.ToLower(strings.TrimSpace(string(data)))
	if name == "codex" || strings.HasPrefix(name, "codex-") {
		return "codex"
	}
	if name == "claude" || strings.HasPrefix(name, "claude-") {
		return "claude"
	}
	if command, err := os.ReadFile(filepath.Join(root, "cmdline")); err == nil {
		arguments := strings.Split(string(command), "\x00")
		if len(arguments) > 2 {
			arguments = arguments[:2]
		}
		for _, argument := range arguments {
			argument = strings.ToLower(argument)
			base := filepath.Base(argument)
			if base == "codex" || strings.Contains(argument, "/@openai/codex/") {
				return "codex"
			}
			if base == "claude" || strings.Contains(argument, "/@anthropic-ai/claude-code/") {
				return "claude"
			}
		}
	}
	return ""
}

func samePath(left, right string) bool {
	leftPath, leftErr := filepath.EvalSymlinks(left)
	rightPath, rightErr := filepath.EvalSymlinks(right)
	if leftErr == nil {
		left = leftPath
	}
	if rightErr == nil {
		right = rightPath
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func notify(message string) {
	if os.Getenv("RECALL_NOTIFY") == "" {
		return
	}
	if path, err := exec.LookPath("notify-send"); err == nil {
		_ = exec.Command(path, "Recall", message).Run()
	}
}
