package main

import (
	"bytes"
	"encoding/json"
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
	matches := filterAndRank(sessions, "database")
	if len(matches) != 1 || matches[0].ID != "one" {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	all := filterAndRank(sessions, "")
	if all[0].ID != "one" {
		t.Fatalf("pinned session was not ranked first: %#v", all)
	}
}

// Two bookmarks may name one session. The row can only show one label, so the
// choice has to be stable rather than depend on map iteration order.
func TestApplyBookmarksPrefersTheFirstLabelInOrder(t *testing.T) {
	sessions := []session{{Provider: "codex", ID: "one"}}
	applyBookmarks(sessions, map[string]bookmark{
		"zebra": {Provider: "codex", SessionID: "one", Note: "last"},
		"alpha": {Provider: "codex", SessionID: "one", Note: "first"},
	})
	if sessions[0].Label != "alpha" || sessions[0].Note != "first" {
		t.Fatalf("label = %q, note = %q; want alpha/first", sessions[0].Label, sessions[0].Note)
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
	// Both transcripts must look newer than the synthetic /proc entry, whose
	// modification time stands in for the process start time. A transcript last
	// written before its process began belongs to an earlier run and is skipped,
	// so fixed timestamps from 1970 would leave nothing for the process to claim.
	now := time.Now()
	sessions := []session{
		{Provider: "codex", ID: "older", CWD: cwd, Updated: now},
		{Provider: "codex", ID: "newer", CWD: cwd, Updated: now.Add(time.Minute)},
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
	// Recent timestamps for the same reason as above: each process claims one
	// transcript, and only transcripts written since it started are candidates.
	now := time.Now()
	sessions := []session{
		{Provider: "codex", ID: "older", CWD: cwd, Updated: now},
		{Provider: "codex", ID: "newer", CWD: cwd, Updated: now.Add(time.Minute)},
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
	_, _, err := pick([]session{{ID: "first"}}, options{limit: 50}, true, false, "recall> ", displayLine, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "fzf is required") {
		t.Fatalf("error = %v", err)
	}
}

// A stub fzf exercises the selection round trip without a terminal. Real fzf
// echoes back the input line it accepted, prefixed by the pressed key when
// --expect is set, and pick maps that line back to a session by its index field.
func TestPickMapsSelectionBackToItsSession(t *testing.T) {
	sessions := []session{{Provider: "codex", ID: "first"}, {Provider: "claude", ID: "second"}}
	cases := []struct {
		name       string
		allowFork  bool
		body       string
		wantID     string
		wantAction selectionAction
	}{
		// Without --expect fzf prints only the row, which used to take a separate
		// parsing path in pick.
		{"accept without expect", false, `awk -F'\t' '$1==1'`, "second", actionOpen},
		{"accept with expect", true, `printf 'enter\n'; awk -F'\t' '$1==1'`, "second", actionOpen},
		{"fork key", true, `printf 'ctrl-f\n'; awk -F'\t' '$1==0'`, "first", actionFork},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			bin := t.TempDir()
			stub := filepath.Join(bin, "fzf")
			writeFixture(t, stub, "#!/bin/sh\n"+item.body+"\n")
			if err := os.Chmod(stub, 0700); err != nil {
				t.Fatal(err)
			}
			// The stub shell needs the standard tools, and bin comes first so the
			// stub still shadows any real fzf.
			t.Setenv("PATH", bin+":/usr/bin:/bin")
			selected, action, err := pick(sessions, options{limit: 50}, item.allowFork, false,
				"recall> ", displayLine, &bytes.Buffer{})
			if err != nil {
				t.Fatal(err)
			}
			if selected.ID != item.wantID || action != item.wantAction {
				t.Fatalf("selected %q action %d, want %q action %d",
					selected.ID, action, item.wantID, item.wantAction)
			}
		})
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

// fzf consumes the ANSI conceal attribute without emitting it, so the searchable
// transcript text must be hidden by padding it past the terminal width instead.
func TestPickerLineHidesSearchTextWithoutAnsi(t *testing.T) {
	row := "* claude 2026-09-03  recall-dev  Testing recall  ~/recall"
	line := pickerLine(0, row, "provider id transcript words", 120)
	if strings.Contains(line, "\x1b") {
		t.Fatalf("picker line must not rely on ANSI escapes: %q", line)
	}
	index, rest, found := strings.Cut(strings.TrimSuffix(line, "\n"), "\t")
	if !found || index != "0" {
		t.Fatalf("index field is missing from %q", line)
	}
	if !strings.HasPrefix(rest, row) {
		t.Fatalf("visible row is not at the start of the displayed field: %q", rest)
	}
	// Everything a terminal of this width can show must be the row and padding.
	if visible := []rune(rest)[:120]; strings.TrimSpace(string(visible)) != row {
		t.Fatalf("transcript text is visible within the first 120 columns: %q", string(visible))
	}
	if !strings.Contains(rest, "transcript words") {
		t.Fatalf("transcript text was dropped and would not be searchable: %q", rest)
	}
}

func TestPickerLineAlwaysSeparatesRowFromSearchText(t *testing.T) {
	// A row longer than the width must still not run into the search text.
	line := pickerLine(7, strings.Repeat("x", 200), "SEARCHABLE", 40)
	rest := strings.SplitN(strings.TrimSuffix(line, "\n"), "\t", 2)[1]
	if !strings.Contains(rest, " SEARCHABLE") {
		t.Fatalf("row and search text were joined without a separator: %q", rest)
	}
}

func TestPickerWidthExceedsTerminal(t *testing.T) {
	if got := pickerWidth(); got <= 0 {
		t.Fatalf("pickerWidth = %d, want a positive column count", got)
	}
	if columns := terminalColumns(); columns > 0 && pickerWidth() <= columns {
		t.Fatalf("pickerWidth %d must exceed the terminal width %d", pickerWidth(), columns)
	}
}

// The search text shares a field with the visible row, so escapes and tabs from a
// transcript must not survive into it.
func TestPickerTextStripsControlCharacters(t *testing.T) {
	item := session{
		Provider: "claude", ID: "abc", Title: "red \x1b[31mtitle\x1b[0m",
		SearchText: "line one\tline two\nline three",
	}
	got := pickerText(item)
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("escape sequence survived: %q", got)
	}
	if strings.ContainsAny(got, "\t\n") {
		t.Errorf("tab or newline survived and would break the field layout: %q", got)
	}
	for _, want := range []string{"claude", "abc", "title", "line one", "line three"} {
		if !strings.Contains(got, want) {
			t.Errorf("searchable text lost %q: %q", want, got)
		}
	}
}

// Claude Code generates a short aiTitle for most sessions, which reads far better
// in a row than a truncated opening message or a compaction summary.
func TestAITitleWinsOverSummaryAndOpeningMessage(t *testing.T) {
	temp := claudeOnly(t)
	writeFixture(t, filepath.Join(temp, "claude", "projects", "p", "titled.jsonl"),
		claudeUser("titled", "Help me check the Bridgev2 backfill progress and then explain it")+
			claudeAssistant("titled", "Looking now")+
			`{"type":"summary","sessionId":"titled","summary":"A long compaction summary of everything"}`+"\n"+
			`{"type":"ai-title","sessionId":"titled","aiTitle":"Bridgev2 backfill progress"}`+"\n")

	sessions, _ := discover(options{})
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if got := sessions[0].Title; got != "Bridgev2 backfill progress" {
		t.Fatalf("title = %q, want the generated aiTitle", got)
	}
}

// Order in the file must not decide the winner: aiTitle can appear before the
// summary just as easily as after it.
func TestAITitleWinsRegardlessOfRecordOrder(t *testing.T) {
	temp := claudeOnly(t)
	writeFixture(t, filepath.Join(temp, "claude", "projects", "p", "order.jsonl"),
		`{"type":"ai-title","sessionId":"order","aiTitle":"Generated title"}`+"\n"+
			claudeUser("order", "Opening message")+
			claudeAssistant("order", "Reply")+
			`{"type":"summary","sessionId":"order","summary":"Compaction summary"}`+"\n")

	sessions, _ := discover(options{})
	if len(sessions) != 1 || sessions[0].Title != "Generated title" {
		t.Fatalf("title = %#v, want the aiTitle to win", sessions)
	}
}

// Without an aiTitle the summary is still preferred over the opening message.
func TestSummaryStillBeatsOpeningMessage(t *testing.T) {
	temp := claudeOnly(t)
	writeFixture(t, filepath.Join(temp, "claude", "projects", "p", "sum.jsonl"),
		claudeUser("sum", "Opening message")+
			`{"type":"summary","sessionId":"sum","summary":"Compaction summary"}`+"\n")

	sessions, _ := discover(options{})
	if len(sessions) != 1 || sessions[0].Title != "Compaction summary" {
		t.Fatalf("title = %#v, want the summary", sessions)
	}
}

// A transcript holding a generated title but no conversation is still empty, so
// the title must not resurrect it.
func TestAITitleAloneIsNotContent(t *testing.T) {
	temp := claudeOnly(t)
	writeFixture(t, filepath.Join(temp, "claude", "projects", "p", "shell.jsonl"),
		`{"type":"ai-title","sessionId":"shell","aiTitle":"Looks like a real session"}`+"\n"+
			`{"type":"agent-name","sessionId":"shell","agentName":"claude"}`+"\n")

	sessions, _ := discover(options{})
	if len(sessions) != 0 {
		t.Fatalf("got %#v, want no sessions", sessions)
	}
}

// Claude Code writes a caveat and a command block whenever a slash command runs.
// Opening a session that way used to title it with the caveat, which is identical
// in every transcript, so unrelated conversations shared one meaningless title.
func TestSlashCommandBoilerplateIsNotATitle(t *testing.T) {
	temp := claudeOnly(t)
	writeFixture(t, filepath.Join(temp, "claude", "projects", "p", "started-with-command.jsonl"),
		claudeUser("real", caveat)+
			claudeUser("real", "<command-name>/clear</command-name>\\n<command-args></command-args>")+
			claudeUser("real", "Help me check the Bridgev2 backfill progress")+
			claudeAssistant("real", "Looking at the backfill now"))

	sessions, _ := discover(options{})
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if got := sessions[0].Title; got != "Help me check the Bridgev2 backfill progress" {
		t.Fatalf("title = %q, want the first real message", got)
	}
	if strings.Contains(sessions[0].SearchText, "DO NOT respond") {
		t.Error("the caveat is identical everywhere and must not pollute search text")
	}
	if !strings.Contains(sessions[0].SearchText, "/clear") {
		t.Error("the command name should stay searchable")
	}
}

// A transcript holding only the bookkeeping for /clear has nothing to resume.
func TestCommandOnlySessionIsNotReported(t *testing.T) {
	temp := claudeOnly(t)
	writeFixture(t, filepath.Join(temp, "claude", "projects", "p", "just-clear.jsonl"),
		claudeUser("empty", caveat)+
			claudeUser("empty", "<command-name>/clear</command-name>"))

	sessions, _ := discover(options{})
	if len(sessions) != 0 {
		t.Fatalf("got %#v, want no sessions", sessions)
	}
}

// Invoking a skill by slash command can leave the assistant as the only speaker.
// That session is real work and must survive the rule above.
func TestSkillInvocationSessionSurvives(t *testing.T) {
	temp := claudeOnly(t)
	writeFixture(t, filepath.Join(temp, "claude", "projects", "p", "skill.jsonl"),
		claudeUser("skill", caveat)+
			claudeUser("skill", "<command-name>/cp-oncall-list-alerts</command-name>")+
			claudeAssistant("skill", "Here are the high priority alerts for this week"))

	sessions, _ := discover(options{})
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want the skill session kept", len(sessions))
	}
	if !strings.Contains(sessions[0].SearchText, "high priority alerts") {
		t.Errorf("assistant work missing from search text: %q", sessions[0].SearchText)
	}
}

const caveat = "<local-command-caveat>Caveat: The messages below were generated by the user " +
	"while running local commands. DO NOT respond to these messages.</local-command-caveat>"

// claudeOnly points discovery at an empty temporary home with no codex sessions.
func claudeOnly(t *testing.T) string {
	t.Helper()
	temp := t.TempDir()
	t.Setenv("HOME", temp)
	t.Setenv("CODEX_HOME", filepath.Join(temp, "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(temp, "claude"))
	return temp
}

func claudeUser(id, text string) string {
	return `{"type":"user","sessionId":"` + id + `","cwd":"/work","message":{"role":"user","content":` +
		quoteJSON(text) + "}}\n"
}

func claudeAssistant(id, text string) string {
	return `{"type":"assistant","sessionId":"` + id + `","cwd":"/work","message":{"role":"assistant","content":` +
		quoteJSON(text) + "}}\n"
}

func quoteJSON(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// One oversized record must not cost the rest of the transcript. A real 1.4 GB
// line here used to end the scan, silently hiding the following 260 records from
// full-text search.
func TestOversizedRecordDoesNotTruncateTranscript(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("HOME", temp)
	t.Setenv("CODEX_HOME", filepath.Join(temp, "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(temp, "claude"))

	message := func(text string) string {
		return `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + text + `"}]}}`
	}
	giant := message(strings.Repeat("x", maxTranscriptRecord+1024))
	writeFixture(t, filepath.Join(temp, "codex", "sessions", "big.jsonl"), strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"big","cwd":"/work"}}`,
		message("before the giant record"),
		giant,
		message("after the giant record"),
	}, "\n")+"\n")

	sessions, warnings := discover(options{})
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	for _, want := range []string{"before the giant record", "after the giant record"} {
		if !strings.Contains(sessions[0].SearchText, want) {
			t.Errorf("search text is missing %q", want)
		}
	}
	if strings.Contains(sessions[0].SearchText, strings.Repeat("x", 64)) {
		t.Error("the oversized record was parsed instead of skipped")
	}
}

// The setup command is compiled per platform, so the name needs no platform
// suffix. It must still be recognised as a command rather than a search term.
func TestSetupIsACommand(t *testing.T) {
	if !isCommand("setup") {
		t.Fatal("setup should be dispatched as a command")
	}
	for _, value := range []string{"setup-macos", "setup-omarchy"} {
		if isCommand(value) {
			t.Errorf("%q should no longer be a command", value)
		}
	}
}
