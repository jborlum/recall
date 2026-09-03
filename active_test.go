package main

import (
	"bytes"
	"testing"
	"time"
)

func TestCommandProvider(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{"claude", "claude"},
		{"/Users/someone/.local/bin/claude", "claude"},
		{"claude-code", "claude"},
		{"claude-2.1.259", "claude"},
		{"node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js", "claude"},
		{"codex", "codex"},
		{"/opt/homebrew/bin/codex resume abc", "codex"},
		{"codex-cli", "codex"},
		{"node /usr/lib/node_modules/@openai/codex/bin/codex.js", "codex"},
		// Unrelated tools that merely share the prefix must not register as a
		// live provider session, or they attach bookmarks to the wrong session.
		{"claude-monitor", ""},
		{"claude-usage-tracker", ""},
		{"codex-review", ""},
		{"claudia", ""},
		{"", ""},
		{"python /Users/someone/src/.claude-plugin/qa/proxy.py", ""},
	}
	for _, item := range cases {
		if got := commandProvider(item.command); got != item.want {
			t.Errorf("commandProvider(%q) = %q, want %q", item.command, got, item.want)
		}
	}
}

func TestApplyActiveFallbacksPrefersNewestOwnableTranscript(t *testing.T) {
	now := time.Now()
	sessions := []session{
		{Provider: "claude", ID: "stale", CWD: "/work", Updated: now.Add(-72 * time.Hour)},
		{Provider: "claude", ID: "live", CWD: "/work", Updated: now.Add(-time.Minute)},
		{Provider: "claude", ID: "elsewhere", CWD: "/other", Updated: now},
		{Provider: "codex", ID: "wrong-provider", CWD: "/work", Updated: now},
	}
	active := map[int]bool{}
	applyActiveFallbacks(sessions, map[processGroup][]time.Time{
		{"claude", "/work"}: {now.Add(-10 * time.Minute)},
	}, active)
	if len(active) != 1 || !active[1] {
		t.Fatalf("active = %v, want only the live session in /work", active)
	}
}

func TestApplyActiveFallbacksSkipsTranscriptsPredatingProcess(t *testing.T) {
	now := time.Now()
	sessions := []session{{Provider: "claude", ID: "stale", CWD: "/work", Updated: now.Add(-72 * time.Hour)}}
	active := map[int]bool{}
	applyActiveFallbacks(sessions, map[processGroup][]time.Time{
		{"claude", "/work"}: {now.Add(-time.Minute)},
	}, active)
	if len(active) != 0 {
		t.Fatalf("active = %v, want none", active)
	}
}

func TestApplyActiveFallbacksWithoutStartTimeKeepsMatching(t *testing.T) {
	now := time.Now()
	sessions := []session{{Provider: "claude", ID: "stale", CWD: "/work", Updated: now.Add(-72 * time.Hour)}}
	active := map[int]bool{}
	applyActiveFallbacks(sessions, map[processGroup][]time.Time{
		{"claude", "/work"}: {{}},
	}, active)
	if len(active) != 1 || !active[0] {
		t.Fatalf("active = %v, want the session matched when no start time is known", active)
	}
}

func TestApplyActiveFallbacksIgnoresArchivedSessions(t *testing.T) {
	now := time.Now()
	sessions := []session{{Provider: "codex", ID: "archived", CWD: "/work", Updated: now, Archived: true}}
	active := map[int]bool{}
	applyActiveFallbacks(sessions, map[processGroup][]time.Time{
		{"codex", "/work"}: {now.Add(-time.Minute)},
	}, active)
	if len(active) != 0 {
		t.Fatalf("active = %v, want none", active)
	}
}

func TestSessionIDVariable(t *testing.T) {
	cases := map[string]string{
		"CODEX_THREAD_ID":   "codex",
		"CODEX_SESSION_ID":  "codex",
		"CLAUDE_SESSION_ID": "claude",
		"PATH":              "",
		"":                  "",
	}
	for name, want := range cases {
		if got := sessionIDVariable(name); got != want {
			t.Errorf("sessionIDVariable(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestNotifyStaysSilentWhenDisabled(t *testing.T) {
	t.Setenv("RECALL_NOTIFY", "")
	var errOut bytes.Buffer
	notify(&errOut, "message")
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}
