package main

import (
	"bytes"
	"os"
	"path/filepath"
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
		{Provider: "codex", ID: "one", Title: "Migration", Path: first, Updated: time.Unix(1, 0)},
		{Provider: "claude", ID: "two", Title: "Other", Path: second, Updated: time.Unix(2, 0)},
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

func TestParseBookmarkSelection(t *testing.T) {
	index, action, err := parseBookmarkSelection("enter\n2\trow\n")
	if err != nil || index != 2 || action != actionOpen {
		t.Fatalf("open selection = %d, %d, %v", index, action, err)
	}
	index, action, err = parseBookmarkSelection("ctrl-d\n1\trow\n")
	if err != nil || index != 1 || action != actionDelete {
		t.Fatalf("delete selection = %d, %d, %v", index, action, err)
	}
	index, action, err = parseBookmarkSelection("0\trow\n")
	if err != nil || index != 0 || action != actionOpen {
		t.Fatalf("plain selection = %d, %d, %v", index, action, err)
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
