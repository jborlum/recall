package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var version = "0.8.1"

type session struct {
	Provider   string
	ID         string
	Title      string
	CWD        string
	Path       string
	Updated    time.Time
	SearchText string
	Archived   bool
	Missing    bool
	Label      string
	Note       string
	Score      int
}

type bookmark struct {
	Provider    string    `json:"provider"`
	SessionID   string    `json:"session_id"`
	CreatedAt   time.Time `json:"created_at"`
	Note        string    `json:"note,omitempty"`
	CachedTitle string    `json:"cached_title,omitempty"`
	CachedCWD   string    `json:"cached_cwd,omitempty"`
}

type bookmarkFile struct {
	Version   int                 `json:"version"`
	Bookmarks map[string]bookmark `json:"bookmarks"`
}

type options struct {
	provider string
	cwd      string
	limit    int
	print    bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, in io.Reader, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("recall", flag.ContinueOnError)
	flags.SetOutput(errOut)
	var opts options
	flags.StringVar(&opts.provider, "provider", "", "only show codex or claude sessions")
	flags.StringVar(&opts.cwd, "cwd", "", "only show sessions under this directory")
	flags.IntVar(&opts.limit, "limit", 50, "maximum displayed results")
	flags.BoolVar(&opts.print, "print", false, "print matching sessions and exit")
	showVersion := flags.Bool("version", false, "print version")
	flags.Usage = func() { usage(errOut) }
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(out, version)
		return 0
	}
	if opts.limit < 1 {
		fmt.Fprintln(errOut, "recall: --limit must be positive")
		return 2
	}
	if opts.provider != "" && opts.provider != "codex" && opts.provider != "claude" {
		fmt.Fprintln(errOut, "recall: --provider must be codex or claude")
		return 2
	}

	rest := flags.Args()
	command := "open"
	if len(rest) > 0 && isCommand(rest[0]) {
		command, rest = rest[0], rest[1:]
	}
	if command == "help" {
		usage(out)
		return 0
	}
	if isPlatformSetupCommand(command) {
		return runPlatformSetup(rest, out, errOut)
	}
	if command == "bookmark" {
		if len(rest) == 0 {
			command = "bookmarks"
		} else {
			subcommand := rest[0]
			rest = rest[1:]
			switch subcommand {
			case "add":
				command = "pin"
			case "active":
				command = "pin-active"
			case "list":
				command = "pins"
			case "remove":
				command = "unpin"
			default:
				fmt.Fprintln(errOut, "usage: recall bookmark [add|active|list|remove]")
				return 2
			}
		}
	}

	bookmarks, err := loadBookmarks()
	if err != nil {
		fmt.Fprintf(errOut, "recall: read bookmarks: %v\n", err)
		return 1
	}
	if command == "unpin" {
		if len(rest) != 1 {
			fmt.Fprintln(errOut, "usage: recall bookmark remove NAME")
			return 2
		}
		if _, ok := bookmarks[rest[0]]; !ok {
			fmt.Fprintf(errOut, "recall: bookmark %q does not exist\n", rest[0])
			return 1
		}
		delete(bookmarks, rest[0])
		if err := saveBookmarks(bookmarks); err != nil {
			fmt.Fprintf(errOut, "recall: save bookmarks: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "Removed bookmark %s\n", rest[0])
		return 0
	}

	sessions, warnings := discover(opts)
	for _, warning := range warnings {
		fmt.Fprintf(errOut, "recall: warning: %v\n", warning)
	}
	applyBookmarks(sessions, bookmarks)
	if opts.print && command != "open" {
		fmt.Fprintln(errOut, "recall: --print cannot be combined with a command")
		return 2
	}

	switch command {
	case "doctor":
		return doctor(sessions, bookmarks, out)
	case "pins":
		pinned := pinnedSessions(sessions, bookmarks)
		printSessions(out, pinned, opts.limit, bookmarkRows(pinned))
		return 0
	case "pin-active":
		return pinActive(sessions, bookmarks, rest, opts, in, out, errOut)
	case "bookmarks":
		return manageBookmarks(sessions, bookmarks, opts, in, out, errOut)
	}

	label := ""
	queryArgs := rest
	if command == "pin" {
		if len(rest) == 0 {
			fmt.Fprintln(errOut, "usage: recall bookmark add NAME [QUERY]")
			return 2
		}
		label, queryArgs = rest[0], rest[1:]
		if _, exists := bookmarks[label]; exists {
			fmt.Fprintf(errOut, "recall: bookmark %q already exists\n", label)
			return 1
		}
		if len(queryArgs) == 0 {
			queryArgs = []string{label}
		}
	}

	query := strings.TrimSpace(strings.Join(queryArgs, " "))
	matches := filterAndRank(sessions, bookmarks, query)
	if len(matches) == 0 {
		fmt.Fprintln(errOut, "recall: no matching sessions")
		return 1
	}
	if opts.print || !terminalIO(in, out) {
		printSessions(out, matches, opts.limit, displayLine)
		return 0
	}

	selected, action, err := pick(matches, opts, command != "pin", false, "recall> ", displayLine, in, errOut)
	if err != nil {
		if !errors.Is(err, errCancelled) {
			fmt.Fprintf(errOut, "recall: %v\n", err)
			return 1
		}
		return 130
	}
	if command == "pin" {
		bookmarks[label] = bookmark{
			Provider: selected.Provider, SessionID: selected.ID, CreatedAt: time.Now().UTC(),
			CachedTitle: selected.Title, CachedCWD: selected.CWD,
		}
		if err := saveBookmarks(bookmarks); err != nil {
			fmt.Fprintf(errOut, "recall: save bookmarks: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "Added bookmark %s -> %s:%s\n", label, selected.Provider, selected.ID)
		return 0
	}
	if err := launch(selected, action == actionFork, in, out, errOut); err != nil {
		if errors.Is(err, errCancelled) {
			return 130
		}
		fmt.Fprintf(errOut, "recall: %v\n", err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprint(w, `recall - find and reopen local Codex and Claude conversations

Usage:
  recall [flags] [QUERY]           search, select, and resume
  recall [flags] bookmark          resume, fork, or delete bookmarks
  recall [flags] bookmark add NAME [QUERY]
                                    bookmark a selected session
  recall [flags] bookmark active [NAME]
                                    bookmark the active session
  recall [flags] bookmark list      print bookmarks
  recall bookmark remove NAME       remove a bookmark only
  recall [flags] doctor            report discovery and stale bookmarks
`)
	if setupUsage := platformSetupUsage(); setupUsage != "" {
		fmt.Fprint(w, setupUsage)
	}
	fmt.Fprint(w, `

Flags:
  --provider codex|claude
  --cwd DIR
  --limit N
  --print
  --version`)
}

func isCommand(value string) bool {
	switch value {
	case "bookmark", "doctor", "help":
		return true
	default:
		return isPlatformSetupCommand(value)
	}
}

func discover(opts options) ([]session, []error) {
	var sessions []session
	var warnings []error
	if opts.provider == "" || opts.provider == "codex" {
		found, err := discoverCodex()
		sessions = append(sessions, found...)
		if err != nil {
			warnings = append(warnings, err)
		}
	}
	if opts.provider == "" || opts.provider == "claude" {
		found, err := discoverClaude()
		sessions = append(sessions, found...)
		if err != nil {
			warnings = append(warnings, err)
		}
	}
	if opts.cwd != "" {
		root, err := filepath.Abs(opts.cwd)
		if err == nil {
			filtered := sessions[:0]
			for _, item := range sessions {
				if pathWithin(item.CWD, root) {
					filtered = append(filtered, item)
				}
			}
			sessions = filtered
		}
	}
	return dedupe(sessions), warnings
}

func discoverCodex() ([]session, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := os.Getenv("CODEX_HOME")
	if root == "" {
		root = filepath.Join(home, ".codex")
	}
	var sessions []session
	for _, source := range []struct {
		dir      string
		archived bool
	}{{filepath.Join(root, "sessions"), false}, {filepath.Join(root, "archived_sessions"), true}} {
		err := walkJSONL(source.dir, func(path string, info fs.FileInfo) {
			if item, ok := parseCodex(path, info, source.archived); ok {
				sessions = append(sessions, item)
			}
		})
		if err != nil {
			return sessions, fmt.Errorf("codex: %w", err)
		}
	}
	return sessions, nil
}

func discoverClaude() ([]session, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := os.Getenv("CLAUDE_CONFIG_DIR")
	if root == "" {
		root = filepath.Join(home, ".claude")
	}
	var sessions []session
	seen := map[string]bool{}
	for _, dir := range []string{filepath.Join(root, "projects"), filepath.Join(root, "sessions")} {
		err := walkJSONL(dir, func(path string, info fs.FileInfo) {
			if seen[path] {
				return
			}
			seen[path] = true
			if item, ok := parseClaude(path, info); ok {
				sessions = append(sessions, item)
			}
		})
		if err != nil {
			return sessions, fmt.Errorf("claude: %w", err)
		}
	}
	return sessions, nil
}

func walkJSONL(root string, fn func(string, fs.FileInfo)) error {
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		info, err := entry.Info()
		if err == nil {
			fn(path, info)
		}
		return nil
	})
}

type codexEnvelope struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Payload   struct {
		Type      string          `json:"type"`
		Role      string          `json:"role"`
		ID        string          `json:"id"`
		SessionID string          `json:"session_id"`
		CWD       string          `json:"cwd"`
		Timestamp string          `json:"timestamp"`
		Name      string          `json:"name"`
		Title     string          `json:"title"`
		Content   json.RawMessage `json:"content"`
	} `json:"payload"`
}

func parseCodex(path string, info fs.FileInfo, archived bool) (session, bool) {
	file, err := os.Open(path)
	if err != nil {
		return session{}, false
	}
	defer file.Close()
	result := session{Provider: "codex", Path: path, Updated: info.ModTime(), Archived: archived}
	scanner := newScanner(file)
	for scanner.Scan() {
		var event codexEnvelope
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event.Type == "session_meta" {
			result.ID = firstNonEmpty(event.Payload.ID, event.Payload.SessionID)
			result.CWD = event.Payload.CWD
			result.Title = firstNonEmpty(event.Payload.Name, event.Payload.Title)
			if parsed := parseTime(firstNonEmpty(event.Payload.Timestamp, event.Timestamp)); result.Updated.IsZero() && !parsed.IsZero() {
				result.Updated = parsed
			}
		}
		if event.Type == "response_item" && event.Payload.Type == "message" && (event.Payload.Role == "user" || event.Payload.Role == "assistant") {
			text := contentText(event.Payload.Content)
			result.SearchText = appendSearchText(result.SearchText, text)
			if result.Title == "" && event.Payload.Role == "user" {
				if usableTitle(text) {
					result.Title = oneLine(text, 100)
				}
			}
		}
	}
	if result.ID == "" {
		result.ID = idFromFilename(path)
	}
	return result, result.ID != ""
}

type claudeEvent struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	CWD       string          `json:"cwd"`
	Timestamp string          `json:"timestamp"`
	Summary   string          `json:"summary"`
	Message   json.RawMessage `json:"message"`
}

func parseClaude(path string, info fs.FileInfo) (session, bool) {
	file, err := os.Open(path)
	if err != nil {
		return session{}, false
	}
	defer file.Close()
	result := session{Provider: "claude", Path: path, Updated: info.ModTime()}
	scanner := newScanner(file)
	for scanner.Scan() {
		var event claudeEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		result.ID = firstNonEmpty(result.ID, event.SessionID)
		result.CWD = firstNonEmpty(result.CWD, event.CWD)
		if event.Summary != "" {
			result.Title = oneLine(event.Summary, 100)
		}
		if parsed := parseTime(event.Timestamp); parsed.After(result.Updated) {
			result.Updated = parsed
		}
		if len(event.Message) > 0 {
			var message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			}
			if json.Unmarshal(event.Message, &message) == nil && (message.Role == "user" || message.Role == "assistant") {
				text := contentText(message.Content)
				result.SearchText = appendSearchText(result.SearchText, text)
				if result.Title == "" && message.Role == "user" && usableTitle(text) {
					result.Title = oneLine(text, 100)
				}
			}
		}
	}
	if result.ID == "" {
		result.ID = idFromFilename(path)
	}
	return result, result.ID != ""
}

func appendSearchText(existing, text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return existing
	}
	if existing != "" {
		existing += " "
	}
	return existing + text
}

func newScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	return scanner
}

func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var direct string
	if json.Unmarshal(raw, &direct) == nil {
		return direct
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var result strings.Builder
		for _, part := range parts {
			if part.Text != "" {
				if result.Len() > 0 {
					result.WriteByte(' ')
				}
				result.WriteString(part.Text)
			}
		}
		return result.String()
	}
	return ""
}

func usableTitle(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.HasPrefix(value, "<environment_context>") && !strings.HasPrefix(value, "# AGENTS.md")
}

func idFromFilename(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if index := strings.LastIndex(name, "-"); index >= 0 && len(name)-index > 20 {
		candidate := name[index+1:]
		if strings.Count(candidate, "-") == 0 {
			return candidate
		}
	}
	return name
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func dedupe(items []session) []session {
	seen := map[string]int{}
	result := make([]session, 0, len(items))
	for _, item := range items {
		key := item.Provider + "\x00" + item.ID
		if index, ok := seen[key]; ok {
			if item.Updated.After(result[index].Updated) {
				result[index] = item
			}
			continue
		}
		seen[key] = len(result)
		result = append(result, item)
	}
	return result
}

func pathWithin(path, root string) bool {
	if path == "" {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, abs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func applyBookmarks(sessions []session, bookmarks map[string]bookmark) {
	labels := make([]string, 0, len(bookmarks))
	for label := range bookmarks {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for index := range sessions {
		for _, label := range labels {
			item := bookmarks[label]
			if item.Provider == sessions[index].Provider && item.SessionID == sessions[index].ID {
				sessions[index].Label = label
				sessions[index].Note = item.Note
				break
			}
		}
	}
}

func filterAndRank(sessions []session, bookmarks map[string]bookmark, query string) []session {
	tokens := strings.Fields(strings.ToLower(query))
	result := make([]session, 0, len(sessions))
	for _, item := range sessions {
		metadata := strings.ToLower(strings.Join([]string{item.Provider, item.ID, item.Title, item.CWD, item.Label, item.Note}, " "))
		metadataMatch := containsAll(metadata, tokens)
		contentMatch := false
		if len(tokens) > 0 && !metadataMatch {
			contentMatch = containsAll(strings.ToLower(item.SearchText), tokens)
		}
		if len(tokens) > 0 && !metadataMatch && !contentMatch {
			continue
		}
		item.Score = score(item, tokens, metadataMatch)
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		return result[i].Updated.After(result[j].Updated)
	})
	return result
}

func containsAll(haystack string, tokens []string) bool {
	for _, token := range tokens {
		if !strings.Contains(haystack, token) {
			return false
		}
	}
	return true
}

func score(item session, tokens []string, metadataMatch bool) int {
	value := 0
	if item.Label != "" {
		value += 1000
	}
	if len(tokens) == 0 {
		return value
	}
	query := strings.Join(tokens, " ")
	if strings.EqualFold(item.Label, query) {
		value += 1000
	}
	if strings.Contains(strings.ToLower(item.Title), query) {
		value += 300
	}
	if strings.Contains(strings.ToLower(item.CWD), query) {
		value += 150
	}
	if strings.Contains(strings.ToLower(item.ID), query) {
		value += 100
	}
	if metadataMatch {
		value += 50
	}
	return value
}

func bookmarksPath() (string, error) {
	if explicit := os.Getenv("RECALL_BOOKMARKS"); explicit != "" {
		return explicit, nil
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "recall", "bookmarks.json"), nil
}

func loadBookmarks() (map[string]bookmark, error) {
	path, err := bookmarksPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bookmark{}, nil
	}
	if err != nil {
		return nil, err
	}
	var stored bookmarkFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, err
	}
	if stored.Bookmarks == nil {
		stored.Bookmarks = map[string]bookmark{}
	}
	return stored.Bookmarks, nil
}

func saveBookmarks(bookmarks map[string]bookmark) error {
	path, err := bookmarksPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".bookmarks-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(bookmarkFile{Version: 1, Bookmarks: bookmarks}); err != nil {
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

func pinnedSessions(sessions []session, bookmarks map[string]bookmark) []session {
	byKey := make(map[string]session, len(sessions))
	for _, item := range sessions {
		byKey[item.Provider+"\x00"+item.ID] = item
	}
	labels := make([]string, 0, len(bookmarks))
	for label := range bookmarks {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	result := make([]session, 0, len(labels))
	for _, label := range labels {
		pin := bookmarks[label]
		item, ok := byKey[pin.Provider+"\x00"+pin.SessionID]
		if !ok {
			// The transcript is gone, so fall back to what the bookmark itself
			// recorded. Its creation date is the only date left for the row, and it
			// reads better than a blank column next to the [missing] marker.
			item = session{
				Provider: pin.Provider, ID: pin.SessionID, Title: pin.CachedTitle,
				CWD: pin.CachedCWD, Updated: pin.CreatedAt, Missing: true,
			}
		}
		item.Label = label
		item.Note = pin.Note
		result = append(result, item)
	}
	return result
}

func doctor(sessions []session, bookmarks map[string]bookmark, out io.Writer) int {
	counts := map[string]int{}
	known := map[string]bool{}
	for _, item := range sessions {
		counts[item.Provider]++
		known[item.Provider+"\x00"+item.ID] = true
	}
	stale := 0
	for label, pin := range bookmarks {
		if !known[pin.Provider+"\x00"+pin.SessionID] {
			fmt.Fprintf(out, "stale bookmark: %s -> %s:%s\n", label, pin.Provider, pin.SessionID)
			stale++
		}
	}
	fmt.Fprintf(out, "codex=%d claude=%d bookmarks=%d stale=%d\n", counts["codex"], counts["claude"], len(bookmarks), stale)
	if stale > 0 {
		return 1
	}
	return 0
}

// rowWidth is the combined width of the columns between the date and the working
// directory, so every view lines the directory up in the same place.
const rowWidth = 72

// rowRenderer formats one row. Bookmark views size a label column across the
// whole list, so a renderer is built from the list rather than being a plain
// function of a single session.
type rowRenderer func(session) string

// bookmarkRows renders the bookmark name in its own column, with the
// conversation title beside it.
func bookmarkRows(sessions []session) rowRenderer {
	labelWidth := bookmarkLabelWidth(sessions)
	return func(item session) string { return displayBookmarkLine(item, labelWidth) }
}

// bookmarkLabelWidth sizes the label column to the widest name in the list, so
// short names leave no wide gap and long ones are not truncated more than needed.
func bookmarkLabelWidth(sessions []session) int {
	width := 8
	for _, item := range sessions {
		if length := len([]rune(item.Label)); length > width {
			width = length
		}
	}
	if width > 36 {
		width = 36
	}
	return width
}

func printSessions(out io.Writer, sessions []session, limit int, render rowRenderer) {
	if len(sessions) < limit {
		limit = len(sessions)
	}
	for _, item := range sessions[:limit] {
		fmt.Fprintln(out, render(item))
	}
}

func displayLine(item session) string {
	title := item.Title
	if title == "" {
		title = "(untitled)"
	}
	if item.Label != "" {
		title = item.Label + " — " + title
	}
	// Truncate after the label is attached, or a bookmarked row overflows the
	// column and shifts the working directory out of line with every other row.
	return fmt.Sprintf("%s %-6s %s  %s  %s%s", rowMark(item), item.Provider, rowDate(item),
		padRight(oneLine(title, rowWidth), rowWidth), shortHome(item.CWD), rowStatus(item))
}

func displayBookmarkLine(item session, labelWidth int) string {
	titleWidth := rowWidth - labelWidth - 2
	title := item.Title
	if title == "" {
		title = "(untitled)"
	}
	return fmt.Sprintf("%s %-6s %s  %s  %s  %s%s", rowMark(item), item.Provider, rowDate(item),
		padRight(oneLine(item.Label, labelWidth), labelWidth),
		padRight(oneLine(title, titleWidth), titleWidth), shortHome(item.CWD), rowStatus(item))
}

func rowMark(item session) string {
	if item.Label != "" {
		return "*"
	}
	return " "
}

func rowStatus(item session) string {
	switch {
	case item.Missing:
		return " [missing]"
	case item.Archived:
		return " [archived]"
	}
	return ""
}

func rowDate(item session) string {
	if item.Updated.IsZero() {
		return "----------"
	}
	return item.Updated.Local().Format("2006-01-02")
}

// padRight pads to width by rune count. Titles routinely contain non-ASCII, and
// the %-Ns verb pads by bytes, which would misalign the columns that follow.
func padRight(value string, width int) string {
	if pad := width - len([]rune(value)); pad > 0 {
		return value + strings.Repeat(" ", pad)
	}
	return value
}

// fallbackPickerWidth is used when the terminal size is unavailable, and
// pickerWidthMargin absorbs a window being widened while the picker is open.
const (
	fallbackPickerWidth = 240
	pickerWidthMargin   = 80
)

func pickerWidth() int {
	columns := terminalColumns()
	if columns <= 0 {
		columns = fallbackPickerWidth
	}
	return columns + pickerWidthMargin
}

// pickerLine builds one fzf input line. Field 1 carries the row index so the
// selection can be mapped back to a session; field 2 holds the visible row
// followed by the searchable transcript text, padded out past the terminal width
// so fzf truncates the transcript out of view.
//
// fzf cannot display one field while searching another: --with-nth rewrites the
// line before --nth is applied, so searchable text has to live inside the
// displayed field. An earlier version relied on the ANSI conceal attribute
// instead, which fzf consumes without emitting, leaving the transcript on screen.
func pickerLine(index int, row, searchText string, width int) string {
	padding := width - len([]rune(row))
	if padding < 1 {
		padding = 1
	}
	return fmt.Sprintf("%d\t%s%s%s\n", index, row, strings.Repeat(" ", padding), searchText)
}

// pickerText is the text fzf searches. Fields collapses every run of whitespace,
// which also removes the tabs that separate the picker's own fields. Control
// characters are dropped as well: this text shares a field with the visible row,
// so an escape sequence surviving from a transcript could colour or corrupt it.
func pickerText(item session) string {
	joined := strings.Join([]string{
		item.Provider, item.ID, item.Title, item.CWD, item.Label, item.Note, item.SearchText,
	}, " ")
	printable := strings.Map(func(char rune) rune {
		if unicode.IsControl(char) && !unicode.IsSpace(char) {
			return -1
		}
		return char
	}, joined)
	return strings.Join(strings.Fields(printable), " ")
}

func oneLine(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

func shortHome(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && (path == home || strings.HasPrefix(path, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

var errCancelled = errors.New("selection cancelled")

type selectionAction int

const (
	actionOpen selectionAction = iota
	actionFork
	actionDelete
)

func terminalIO(in io.Reader, out io.Writer) bool {
	input, inputOK := in.(*os.File)
	output, outputOK := out.(*os.File)
	if !inputOK || !outputOK {
		return false
	}
	inInfo, inErr := input.Stat()
	outInfo, outErr := output.Stat()
	return inErr == nil && outErr == nil && inInfo.Mode()&os.ModeCharDevice != 0 && outInfo.Mode()&os.ModeCharDevice != 0
}

func pick(sessions []session, opts options, allowFork, allowDelete bool, prompt string, render rowRenderer, in io.Reader, errOut io.Writer) (session, selectionAction, error) {
	limit := opts.limit
	if len(sessions) < limit {
		limit = len(sessions)
	}
	candidates := sessions[:limit]
	path, err := exec.LookPath("fzf")
	if err != nil {
		return session{}, actionOpen, errors.New("fzf is required")
	}
	var input strings.Builder
	width := pickerWidth()
	for index, item := range candidates {
		input.WriteString(pickerLine(index, render(item), pickerText(item), width))
	}
	var selected bytes.Buffer
	// --with-nth=2 displays only the second field. --nth must not be set alongside
	// it: fzf applies --with-nth first and then indexes --nth into the result, so
	// any --nth here would search fields that no longer exist and match nothing.
	// Every row is padded past the terminal width on purpose, so fzf's truncation
	// marker would show on all of them and mean nothing.
	arguments := []string{
		"--ansi", "--delimiter=\\t", "--with-nth=2", "--ellipsis=",
		"--prompt=" + prompt, "--height=80%", "--reverse",
	}
	if allowDelete {
		arguments = append(arguments, "--expect=enter,ctrl-f,ctrl-d", "--header=Enter: resume   Ctrl-F: fork   Ctrl-D: delete   Esc: close")
	} else if allowFork {
		arguments = append(arguments, "--expect=enter,ctrl-f", "--header=Enter: resume   Ctrl-F: fork   Esc: close")
	}
	cmd := exec.Command(path, arguments...)
	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stdout = &selected
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return session{}, actionOpen, errCancelled
	}
	index := 0
	action := actionOpen
	if allowFork || allowDelete {
		index, action, err = parseSelection(selected.String())
	} else {
		field := strings.SplitN(strings.TrimSpace(selected.String()), "\t", 2)[0]
		index, err = strconv.Atoi(field)
	}
	if err != nil || index < 0 || index >= len(candidates) {
		return session{}, actionOpen, errors.New("invalid picker result")
	}
	return candidates[index], action, nil
}

func launch(item session, fork bool, in io.Reader, out, errOut io.Writer) error {
	if item.Archived && item.Provider == "codex" {
		confirmed, err := confirm(in, out, "Session is archived. Restore it? [Y/n] ")
		if err != nil {
			return err
		}
		if !confirmed {
			return errCancelled
		}
		restore := exec.Command("codex", "unarchive", item.ID)
		restore.Stdin, restore.Stdout, restore.Stderr = in, out, errOut
		if err := restore.Run(); err != nil {
			return fmt.Errorf("restore archived session: %w", err)
		}
	}
	var command string
	var args []string
	if item.Provider == "codex" {
		command = "codex"
		if fork {
			args = []string{"fork", item.ID}
		} else {
			args = []string{"resume", item.ID}
		}
	} else {
		command = "claude"
		args = []string{"--resume", item.ID}
		if fork {
			args = append(args, "--fork-session")
		}
	}
	cmd := exec.Command(command, args...)
	if info, err := os.Stat(item.CWD); err == nil && info.IsDir() {
		cmd.Dir = item.CWD
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s: %w", command, err)
	}
	return nil
}

func confirm(in io.Reader, out io.Writer, prompt string) (bool, error) {
	fmt.Fprint(out, prompt)
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "", "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, errors.New("expected yes or no")
	}
}
