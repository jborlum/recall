package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDiscoverCodexAndClaude(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("HOME", temp)
	t.Setenv("CODEX_HOME", filepath.Join(temp, "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(temp, "claude"))

	codexPath := filepath.Join(temp, "codex", "sessions", "2026", "09", "session.jsonl")
	writeFixture(t, codexPath, `{"type":"session_meta","timestamp":"2026-09-01T12:00:00Z","payload":{"id":"codex-id","cwd":"/work/api","timestamp":"2026-09-01T12:00:00Z"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>ignored</environment_context>"}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Fix refresh token rotation"}]}}
{"type":"response_item","payload":{"type":"reasoning","encrypted_content":"secret-token-never-searchable"}}
`)
	claudePath := filepath.Join(temp, "claude", "projects", "project", "claude-id.jsonl")
	writeFixture(t, claudePath, `{"type":"user","sessionId":"claude-id","cwd":"/work/web","timestamp":"2026-09-02T12:00:00Z","message":{"role":"user","content":"Explain the cache invalidation"}}
`)

	sessions, warnings := discover(options{})
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(sessions))
	}
	byProvider := map[string]session{}
	for _, item := range sessions {
		byProvider[item.Provider] = item
	}
	if got := byProvider["codex"].Title; got != "Fix refresh token rotation" {
		t.Fatalf("codex title = %q", got)
	}
	if got := byProvider["codex"].SearchText; !strings.Contains(got, "Fix refresh token rotation") {
		t.Fatalf("codex search text = %q", got)
	}
	if strings.Contains(byProvider["codex"].SearchText, "secret-token-never-searchable") {
		t.Fatal("encrypted reasoning content leaked into search text")
	}
	if got := byProvider["claude"].Title; got != "Explain the cache invalidation" {
		t.Fatalf("claude title = %q", got)
	}
}

func TestSearchAndBookmarkRanking(t *testing.T) {
	temp := t.TempDir()
	first := filepath.Join(temp, "first.jsonl")
	second := filepath.Join(temp, "second.jsonl")
	writeFixture(t, first, `{"message":"database migration"}`)
	writeFixture(t, second, `{"message":"unrelated"}`)
	sessions := []session{
		{Provider: "codex", ID: "one", Title: "Migration", Path: first, SearchText: "database migration", Updated: time.Unix(1, 0)},
		{Provider: "claude", ID: "two", Title: "Other", Path: second, SearchText: "unrelated", Updated: time.Unix(2, 0)},
	}
	bookmarks := map[string]bookmark{"db": {Provider: "codex", SessionID: "one"}}
	applyBookmarks(sessions, bookmarks)
	matches := filterAndRank(sessions, bookmarks, "database")
	if len(matches) != 1 || matches[0].ID != "one" {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	all := filterAndRank(sessions, bookmarks, "")
	if all[0].ID != "one" {
		t.Fatalf("pinned session was not ranked first: %#v", all)
	}
}

func TestBookmarksAreWrittenPrivately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "bookmarks.json")
	t.Setenv("RECALL_BOOKMARKS", path)
	want := map[string]bookmark{
		"auth": {Provider: "codex", SessionID: "abc", CreatedAt: time.Now().UTC()},
	}
	if err := saveBookmarks(want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	got, err := loadBookmarks()
	if err != nil {
		t.Fatal(err)
	}
	if got["auth"].SessionID != "abc" {
		t.Fatalf("bookmark did not round trip: %#v", got)
	}
}

func TestPathWithin(t *testing.T) {
	if !pathWithin("/work/api/sub", "/work/api") {
		t.Fatal("expected child path to match")
	}
	if pathWithin("/work/application", "/work/api") {
		t.Fatal("prefix sibling must not match")
	}
}

func TestConfirm(t *testing.T) {
	var output bytes.Buffer
	ok, err := confirm(bytes.NewBufferString("\n"), &output, "Restore? ")
	if err != nil || !ok {
		t.Fatalf("default confirmation = %v, %v", ok, err)
	}
	ok, err = confirm(bytes.NewBufferString("no\n"), &output, "Restore? ")
	if err != nil || ok {
		t.Fatalf("negative confirmation = %v, %v", ok, err)
	}
}

func TestListAndOpenAreQueriesNotCommands(t *testing.T) {
	if isCommand("list") || isCommand("open") || isCommand("fork") {
		t.Fatal("list, open, and fork should be treated as search terms")
	}
}

func TestDetectActiveSessionFromEnvironment(t *testing.T) {
	t.Setenv("CODEX_THREAD_ID", "active-id")
	t.Setenv("RECALL_PROC_ROOT", t.TempDir())
	sessions := []session{
		{Provider: "codex", ID: "inactive-id"},
		{Provider: "codex", ID: "active-id"},
	}
	active := detectActiveSessions(sessions)
	if len(active) != 1 || active[0].ID != "active-id" {
		t.Fatalf("active sessions = %#v", active)
	}
}

func TestDetectActiveSessionFromProcessCWD(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses a synthetic Linux /proc tree")
	}
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CODEX_SESSION_ID", "")
	t.Setenv("CLAUDE_SESSION_ID", "")
	proc := filepath.Join(t.TempDir(), "42")
	if err := os.MkdirAll(proc, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(proc, "comm"), "codex\n")
	cwd := t.TempDir()
	if err := os.Symlink(cwd, filepath.Join(proc, "cwd")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECALL_PROC_ROOT", filepath.Dir(proc))
	sessions := []session{
		{Provider: "codex", ID: "older", CWD: cwd, Updated: time.Unix(1, 0)},
		{Provider: "codex", ID: "newer", CWD: cwd, Updated: time.Unix(2, 0)},
	}
	active := detectActiveSessions(sessions)
	if len(active) != 1 || active[0].ID != "newer" {
		t.Fatalf("active sessions = %#v", active)
	}
}

func TestDetectsMultipleProcessesInTheSameDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses a synthetic Linux /proc tree")
	}
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CODEX_SESSION_ID", "")
	t.Setenv("CLAUDE_SESSION_ID", "")
	procRoot := t.TempDir()
	cwd := t.TempDir()
	for _, pid := range []string{"41", "42"} {
		process := filepath.Join(procRoot, pid)
		if err := os.MkdirAll(process, 0700); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(process, "comm"), "codex\n")
		if err := os.Symlink(cwd, filepath.Join(process, "cwd")); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("RECALL_PROC_ROOT", procRoot)
	sessions := []session{
		{Provider: "codex", ID: "older", CWD: cwd, Updated: time.Unix(1, 0)},
		{Provider: "codex", ID: "newer", CWD: cwd, Updated: time.Unix(2, 0)},
	}
	active := detectActiveSessions(sessions)
	if len(active) != 2 {
		t.Fatalf("active sessions = %#v", active)
	}
}

func TestAskBookmarkNameUsesAvailableSlug(t *testing.T) {
	var output bytes.Buffer
	selected := session{Title: "Fix Refresh Tokens"}
	bookmarks := map[string]bookmark{"fix-refresh-tokens": {}}
	label, err := askBookmarkName(bytes.NewBufferString("\n"), &output, selected, bookmarks)
	if err != nil {
		t.Fatal(err)
	}
	if label != "fix-refresh-tokens-2" {
		t.Fatalf("label = %q", label)
	}
}

func TestParseSelection(t *testing.T) {
	index, action, err := parseSelection("enter\n2\trow\n")
	if err != nil || index != 2 || action != actionOpen {
		t.Fatalf("open selection = %d, %d, %v", index, action, err)
	}
	index, action, err = parseSelection("ctrl-f\n1\trow\n")
	if err != nil || index != 1 || action != actionFork {
		t.Fatalf("fork selection = %d, %d, %v", index, action, err)
	}
	index, action, err = parseSelection("ctrl-d\n1\trow\n")
	if err != nil || index != 1 || action != actionDelete {
		t.Fatalf("delete selection = %d, %d, %v", index, action, err)
	}
	index, action, err = parseSelection("0\trow\n")
	if err != nil || index != 0 || action != actionOpen {
		t.Fatalf("plain selection = %d, %d, %v", index, action, err)
	}
}

func TestPickerRequiresFZF(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, _, err := pick([]session{{ID: "first"}}, options{limit: 50}, true, false, "recall> ", displayLine, bytes.NewReader(nil), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "fzf is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestDeleteConfirmationDefaultsToNo(t *testing.T) {
	var output bytes.Buffer
	ok, err := confirmNo(bytes.NewBufferString("\n"), &output, "Delete? ")
	if err != nil || ok {
		t.Fatalf("default confirmation = %v, %v", ok, err)
	}
}

func TestLaunchUsesProviderResumeCommand(t *testing.T) {
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "launch.log")
	script := filepath.Join(bin, "codex")
	writeFixture(t, script, "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$RECALL_LAUNCH_LOG\"\n")
	if err := os.Chmod(script, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("RECALL_LAUNCH_LOG", logPath)
	cwd := t.TempDir()
	if err := launch(session{Provider: "codex", ID: "session-123", CWD: cwd}, false, bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "resume session-123\n" {
		t.Fatalf("command = %q", got)
	}
}

func writeFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

// runeIndex reports the column of a substring. strings.Index returns a byte
// offset, which differs between rows once a truncated title contains a
// multi-byte ellipsis, and would compare column alignment incorrectly.
func runeIndex(row, substring string) int {
	byteIndex := strings.Index(row, substring)
	if byteIndex < 0 {
		return -1
	}
	return len([]rune(row[:byteIndex]))
}

func TestBookmarkRowsSeparateNameFromTitle(t *testing.T) {
	updated := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	pinned := []session{
		{Provider: "claude", Label: "mac-support", Title: "Testing recall on macOS", CWD: "/data/one", Updated: updated},
		{Provider: "codex", Label: "hypr", Title: "A far longer conversation title that has to be truncated to fit its column", CWD: "/data/two", Updated: updated},
	}
	render := bookmarkRows(pinned)
	offsets := map[string]int{}
	for _, item := range pinned {
		row := render(item)
		if !strings.Contains(row, item.Label) {
			t.Errorf("row is missing the bookmark name: %q", row)
		}
		if strings.Contains(row, item.Label+" — ") {
			t.Errorf("row still glues the name to the title: %q", row)
		}
		offset := runeIndex(row, item.CWD)
		if offset < 0 {
			t.Fatalf("row is missing its directory: %q", row)
		}
		offsets[item.CWD] = offset
	}
	if offsets["/data/one"] != offsets["/data/two"] {
		t.Fatalf("directory column is not aligned across rows: %v", offsets)
	}
	row := render(pinned[0])
	if strings.Index(row, "mac-support") > strings.Index(row, "Testing recall") {
		t.Fatalf("the name should come before the conversation title: %q", row)
	}
}

// A bookmarked row used to be truncated before the label was attached, which
// pushed the directory column out of line with every unbookmarked row.
func TestDisplayLineAlignsBookmarkedRows(t *testing.T) {
	updated := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	plain := session{Provider: "claude", Title: "Short title", CWD: "/data/plain", Updated: updated}
	marked := session{
		Provider: "claude", Label: "a-very-long-bookmark-name",
		Title: "A long conversation title that would previously overflow the column entirely",
		CWD:   "/data/marked", Updated: updated,
	}
	first := runeIndex(displayLine(plain), "/data/plain")
	second := runeIndex(displayLine(marked), "/data/marked")
	if first < 0 || second < 0 {
		t.Fatalf("rows are missing their directories: %q %q", displayLine(plain), displayLine(marked))
	}
	if first != second {
		t.Fatalf("directory column moved: plain=%d bookmarked=%d", first, second)
	}
}

func TestBookmarkLabelWidthBounds(t *testing.T) {
	if got := bookmarkLabelWidth(nil); got != 8 {
		t.Errorf("empty list gave %d, want the 8 column minimum", got)
	}
	if got := bookmarkLabelWidth([]session{{Label: "twelve-chars"}}); got != 12 {
		t.Errorf("width = %d, want 12 to fit the widest name", got)
	}
	if got := bookmarkLabelWidth([]session{{Label: strings.Repeat("x", 50)}}); got != 36 {
		t.Errorf("width = %d, want the 36 column maximum", got)
	}
}

func TestPadRightCountsRunes(t *testing.T) {
	if got := padRight("æøå", 6); len([]rune(got)) != 6 {
		t.Errorf("padRight(%q, 6) = %q, want six runes", "æøå", got)
	}
	if got := padRight("abcdef", 3); got != "abcdef" {
		t.Errorf("padRight must not truncate, got %q", got)
	}
}

// A bookmark whose transcript is gone has no session to read a date from, but the
// bookmark file records when it was created.
func TestMissingBookmarkShowsItsCreationDate(t *testing.T) {
	created := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	bookmarks := map[string]bookmark{
		"gone": {Provider: "claude", SessionID: "absent", CreatedAt: created, CachedTitle: "Old chat", CachedCWD: "/work"},
	}
	pinned := pinnedSessions(nil, bookmarks)
	if len(pinned) != 1 || !pinned[0].Missing {
		t.Fatalf("pinned = %#v", pinned)
	}
	if !pinned[0].Updated.Equal(created) {
		t.Fatalf("Updated = %v, want the bookmark creation time %v", pinned[0].Updated, created)
	}
	row := bookmarkRows(pinned)(pinned[0])
	if want := created.Local().Format("2006-01-02"); !strings.Contains(row, want) {
		t.Fatalf("row %q does not show %s", row, want)
	}
	if strings.Contains(row, "----------") {
		t.Fatalf("row still has a blank date: %q", row)
	}
}

// Every discovered session gets its date from the transcript file, so the blank
// placeholder should only ever appear for a bookmark with no recorded date.
func TestDiscoveredSessionsAlwaysHaveADate(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("HOME", temp)
	t.Setenv("CODEX_HOME", filepath.Join(temp, "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(temp, "claude"))
	// No timestamp field anywhere, so the date can only come from the file itself.
	writeFixture(t, filepath.Join(temp, "claude", "projects", "p", "no-timestamp.jsonl"),
		`{"type":"user","sessionId":"no-timestamp","cwd":"/work","message":{"role":"user","content":"hello"}}`+"\n")

	sessions, _ := discover(options{})
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].Updated.IsZero() {
		t.Fatal("session has no date despite the transcript file having a modification time")
	}
	if strings.Contains(displayLine(sessions[0]), "----------") {
		t.Fatalf("row has a blank date: %q", displayLine(sessions[0]))
	}
}
