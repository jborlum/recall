//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDarwinDetectsOpenTranscript(t *testing.T) {
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CODEX_SESSION_ID", "")
	t.Setenv("CLAUDE_SESSION_ID", "")
	bin := t.TempDir()
	ps := filepath.Join(bin, "ps")
	lsof := filepath.Join(bin, "lsof")
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	cwd := t.TempDir()
	writeFixture(t, transcript, "")
	writeFixture(t, ps, "#!/bin/sh\nprintf '123 /opt/homebrew/bin/codex\\n'\n")
	writeFixture(t, lsof, "#!/bin/sh\nprintf 'p123\\nfcwd\\nn"+cwd+"\\nf8\\nn"+transcript+"\\n'\n")
	if err := os.Chmod(ps, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lsof, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECALL_PS", ps)
	t.Setenv("RECALL_LSOF", lsof)
	sessions := []session{{Provider: "codex", ID: "active", Path: transcript, CWD: cwd, Updated: time.Now()}}
	active := detectActiveSessions(sessions)
	if len(active) != 1 || active[0].ID != "active" {
		t.Fatalf("active sessions = %#v", active)
	}
}
