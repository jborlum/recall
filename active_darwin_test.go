//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stubProcessTools installs fake ps and lsof executables. The ps stub answers
// the -Ewwo environment listing separately from the default process listing, and
// every lsof call returns the same records.
func stubProcessTools(t *testing.T, listing, environments, lsofRecords string) {
	t.Helper()
	bin := t.TempDir()
	psPath := filepath.Join(bin, "ps")
	lsofPath := filepath.Join(bin, "lsof")
	writeExecutable(t, psPath, "#!/bin/sh\ncase \"$1\" in\n-Ewwo) printf '%s' "+shellQuote(environments)+" ;;\n*) printf '%s' "+shellQuote(listing)+" ;;\nesac\n")
	writeExecutable(t, lsofPath, "#!/bin/sh\nprintf '%s' "+shellQuote(lsofRecords)+"\n")
	t.Setenv("RECALL_PS", psPath)
	t.Setenv("RECALL_LSOF", lsofPath)
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	writeFixture(t, path, contents)
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
}

func clearSessionEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CODEX_SESSION_ID", "")
	t.Setenv("CLAUDE_SESSION_ID", "")
}

func activeIDs(sessions []session) []string {
	result := make([]string, 0, len(sessions))
	for _, item := range detectActiveSessions(sessions) {
		result = append(result, item.ID)
	}
	return result
}

// Claude Code does not hold its transcript open, so lsof reports only the
// working directory and detection has to fall back to matching on it.
func TestDarwinDetectsSessionByWorkingDirectory(t *testing.T) {
	clearSessionEnv(t)
	cwd := t.TempDir()
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	writeFixture(t, transcript, "")
	stubProcessTools(t,
		"501 05:00 /Users/someone/.local/bin/claude\n",
		"501 claude PATH=/usr/bin\n",
		"p501\nfcwd\nn"+cwd+"\nftxt\nn/usr/lib/dyld\n")

	sessions := []session{{Provider: "claude", ID: "live", Path: transcript, CWD: cwd, Updated: time.Now()}}
	if got := activeIDs(sessions); len(got) != 1 || got[0] != "live" {
		t.Fatalf("active ids = %v, want [live]", got)
	}
}

// A process that started after the newest transcript in its directory was last
// written cannot own that transcript, so nothing should be reported as active
// rather than bookmarking a session from an earlier run.
func TestDarwinIgnoresTranscriptOlderThanProcess(t *testing.T) {
	clearSessionEnv(t)
	cwd := t.TempDir()
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	writeFixture(t, transcript, "")
	stubProcessTools(t,
		"501 01:00 /Users/someone/.local/bin/claude\n",
		"501 claude PATH=/usr/bin\n",
		"p501\nfcwd\nn"+cwd+"\n")

	sessions := []session{{Provider: "claude", ID: "stale", Path: transcript, CWD: cwd, Updated: time.Now().Add(-48 * time.Hour)}}
	if got := activeIDs(sessions); len(got) != 0 {
		t.Fatalf("active ids = %v, want none", got)
	}
}

// Two processes in one directory should light up two transcripts, and the older
// abandoned session must not be included.
func TestDarwinMatchesOneSessionPerProcess(t *testing.T) {
	clearSessionEnv(t)
	cwd := t.TempDir()
	root := t.TempDir()
	stubProcessTools(t,
		"501 10:00 /usr/local/bin/claude\n502 09:00 /usr/local/bin/claude\n",
		"501 claude PATH=/usr/bin\n502 claude PATH=/usr/bin\n",
		"p501\nfcwd\nn"+cwd+"\n")

	sessions := []session{}
	for index, name := range []string{"newest", "second", "abandoned"} {
		path := filepath.Join(root, name+".jsonl")
		writeFixture(t, path, "")
		updated := time.Now().Add(-time.Duration(index) * time.Minute)
		if name == "abandoned" {
			updated = time.Now().Add(-72 * time.Hour)
		}
		sessions = append(sessions, session{Provider: "claude", ID: name, Path: path, CWD: cwd, Updated: updated})
	}
	got := activeIDs(sessions)
	if len(got) != 2 {
		t.Fatalf("active ids = %v, want two entries", got)
	}
	for _, id := range got {
		if id == "abandoned" {
			t.Fatalf("active ids = %v, must not include the abandoned session", got)
		}
	}
}

// A session id exported into the process environment is exact, so it wins over
// the working-directory guess and suppresses the fallback entirely.
func TestDarwinDetectsSessionFromProcessEnvironment(t *testing.T) {
	clearSessionEnv(t)
	cwd := t.TempDir()
	root := t.TempDir()
	for _, name := range []string{"exact", "newer"} {
		writeFixture(t, filepath.Join(root, name+".jsonl"), "")
	}
	stubProcessTools(t,
		"501 05:00 /usr/local/bin/claude\n",
		"501 claude PATH=/usr/bin CLAUDE_SESSION_ID=exact\n",
		"p501\nfcwd\nn"+cwd+"\n")

	sessions := []session{
		{Provider: "claude", ID: "exact", Path: filepath.Join(root, "exact.jsonl"), CWD: cwd, Updated: time.Now().Add(-time.Hour)},
		{Provider: "claude", ID: "newer", Path: filepath.Join(root, "newer.jsonl"), CWD: cwd, Updated: time.Now()},
	}
	if got := activeIDs(sessions); len(got) != 1 || got[0] != "exact" {
		t.Fatalf("active ids = %v, want [exact]", got)
	}
}

// The command column must be read without ps -E, or an inherited variable such
// as _=/usr/local/bin/claude would look like a running provider.
func TestDarwinIgnoresProviderNamesInEnvironment(t *testing.T) {
	clearSessionEnv(t)
	cwd := t.TempDir()
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	writeFixture(t, transcript, "")
	stubProcessTools(t,
		"501 05:00 /bin/zsh\n",
		"501 /bin/zsh _=/usr/local/bin/claude\n",
		"p501\nfcwd\nn"+cwd+"\n")

	sessions := []session{{Provider: "claude", ID: "live", Path: transcript, CWD: cwd, Updated: time.Now()}}
	if got := activeIDs(sessions); len(got) != 0 {
		t.Fatalf("active ids = %v, want none", got)
	}
}

// Codex may keep its transcript open, in which case the descriptor is an exact
// match and no working-directory guessing is needed.
func TestDarwinDetectsOpenTranscript(t *testing.T) {
	clearSessionEnv(t)
	cwd := t.TempDir()
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	writeFixture(t, transcript, "")
	stubProcessTools(t,
		"123 05:00 /opt/homebrew/bin/codex\n",
		"123 codex PATH=/usr/bin\n",
		"p123\nfcwd\nn"+cwd+"\nf8\nn"+transcript+"\n")

	sessions := []session{{Provider: "codex", ID: "active", Path: transcript, CWD: cwd, Updated: time.Now().Add(-72 * time.Hour)}}
	if got := activeIDs(sessions); len(got) != 1 || got[0] != "active" {
		t.Fatalf("active ids = %v, want [active]", got)
	}
}

func TestParseElapsed(t *testing.T) {
	cases := []struct {
		value string
		want  time.Duration
		ok    bool
	}{
		{"00:05", 5 * time.Second, true},
		{"18:03", 18*time.Minute + 3*time.Second, true},
		{"01:18:03", time.Hour + 18*time.Minute + 3*time.Second, true},
		{"2-03:04:05", 51*time.Hour + 4*time.Minute + 5*time.Second, true},
		{"", 0, false},
		{"05", 0, false},
		{"a:b", 0, false},
		{"1:2:3:4", 0, false},
	}
	for _, item := range cases {
		got, ok := parseElapsed(item.value)
		if ok != item.ok || got != item.want {
			t.Errorf("parseElapsed(%q) = %v, %v; want %v, %v", item.value, got, ok, item.want, item.ok)
		}
	}
}

func TestProcessStartUnparseableElapsedSkipsCheck(t *testing.T) {
	if start := processStart(time.Now(), "bogus"); !start.IsZero() {
		t.Fatalf("processStart = %v, want zero so the age check is skipped", start)
	}
}
