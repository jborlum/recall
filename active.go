package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
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
		notify(errOut, message)
		return 1
	}

	selected := active[0]
	if len(active) > 1 {
		if !terminalIO(in, out) {
			fmt.Fprintln(errOut, "recall: multiple active sessions; run interactively to choose one")
			printSessions(out, active, opts.limit, displayLine)
			return 1
		}
		var err error
		selected, _, err = pick(active, opts, false, false, "active> ", displayLine, errOut)
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
		notify(errOut, message)
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
	notify(errOut, message)
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
		byID[sessionKey(item.Provider, item.ID)] = index
		if resolved, err := filepath.EvalSymlinks(item.Path); err == nil {
			byPath[resolved] = index
		} else {
			byPath[filepath.Clean(item.Path)] = index
		}
	}
	active := map[int]bool{}
	for _, name := range []string{"CODEX_THREAD_ID", "CODEX_SESSION_ID", "CLAUDE_SESSION_ID"} {
		value := os.Getenv(name)
		if index, ok := byID[sessionKey(sessionIDVariable(name), value)]; ok && value != "" {
			active[index] = true
		}
	}
	detectPlatformActiveSessions(sessions, byID, byPath, active)

	result := make([]session, 0, len(active))
	for index := range active {
		result = append(result, sessions[index])
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Updated.After(result[j].Updated) })
	return result
}

// activeFallbackSlack absorbs small differences between the reported process
// start time and the last transcript write.
const activeFallbackSlack = 2 * time.Second

// processGroup identifies the running provider processes that share a working
// directory.
type processGroup struct {
	provider string
	cwd      string
}

// applyActiveFallbacks attributes provider processes to sessions by working
// directory. Neither provider holds its transcript open, so for Claude this is
// the only signal available and it carries the whole feature.
//
// Each group holds one start time per running process in that directory. Every
// process claims the newest transcript it could still own, so a process that has
// not written anything yet cannot be credited with an older session left behind
// in the same directory.
func applyActiveFallbacks(sessions []session, fallbacks map[processGroup][]time.Time, active map[int]bool) {
	for group, starts := range fallbacks {
		var candidates []int
		for index, item := range sessions {
			if item.Provider == group.provider && !item.Archived && samePath(item.CWD, group.cwd) {
				candidates = append(candidates, index)
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			return sessions[candidates[i]].Updated.After(sessions[candidates[j]].Updated)
		})
		// Newest process first, so it takes the newest transcript and leaves the
		// older ones available to the processes that could actually own them.
		ordered := append([]time.Time(nil), starts...)
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].After(ordered[j]) })
		claimed := map[int]bool{}
		for _, start := range ordered {
			for _, index := range candidates {
				if claimed[index] || !writtenSince(sessions[index], start) {
					continue
				}
				active[index], claimed[index] = true, true
				break
			}
		}
	}
}

// writtenSince reports whether a transcript could belong to a process started at
// start. A live session is appended to while its process runs, so a transcript
// last written before the process began belongs to an earlier run. A zero start
// time means the platform could not report one, which skips the check.
func writtenSince(item session, start time.Time) bool {
	if start.IsZero() {
		return true
	}
	return !item.Updated.Before(start.Add(-activeFallbackSlack))
}

// sessionIDVariable maps a provider's session-id environment variable to its
// provider name, and returns "" for anything else.
func sessionIDVariable(name string) string {
	switch name {
	case "CODEX_THREAD_ID", "CODEX_SESSION_ID":
		return "codex"
	case "CLAUDE_SESSION_ID":
		return "claude"
	}
	return ""
}

// providerAliases lists the exact executable names each provider ships under.
var providerAliases = map[string]string{
	"codex": "codex", "codex-cli": "codex",
	"claude": "claude", "claude-cli": "claude", "claude-code": "claude",
}

func commandProvider(command string) string {
	command = strings.ToLower(strings.TrimSpace(command))
	for _, argument := range strings.Fields(command) {
		if strings.Contains(argument, "/@openai/codex/") {
			return "codex"
		}
		if strings.Contains(argument, "/@anthropic-ai/claude-code/") {
			return "claude"
		}
		base := filepath.Base(argument)
		if provider, ok := providerAliases[base]; ok {
			return provider
		}
		for _, provider := range []string{"codex", "claude"} {
			if versionSuffixed(base, provider) {
				return provider
			}
		}
	}
	return ""
}

// versionSuffixed matches version-pinned executables such as claude-2.1.259
// without matching unrelated tools that merely share the prefix, like
// claude-monitor, which would otherwise be mistaken for a live session.
func versionSuffixed(base, provider string) bool {
	if !strings.HasPrefix(base, provider+"-") {
		return false
	}
	suffix := base[len(provider)+1:]
	if suffix == "" {
		return false
	}
	for _, char := range suffix {
		if !unicode.IsDigit(char) && char != '.' {
			return false
		}
	}
	return true
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

func notify(errOut io.Writer, message string) {
	if os.Getenv("RECALL_NOTIFY") == "" {
		return
	}
	if err := platformNotify(message); err != nil {
		fmt.Fprintf(errOut, "recall: notify: %v\n", err)
	}
}
