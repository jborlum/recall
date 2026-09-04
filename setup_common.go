package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

func setupWord(configured bool) string {
	if configured {
		return "configured"
	}
	return "missing"
}

// staleSessionEnv are variables a hotkey-launched session must not inherit.
//
// A hotkey always starts a TOP-LEVEL session, but the process that fires it
// inherits whatever environment it was launched with — and a hotkey daemon
// started from inside a Claude Code session carries that session's markers.
// (Editing the hotkey config with a coding agent is a very good way to get
// there: on macOS, open(1) hands the caller's environment to the app it
// launches, so `open -a Hammerspoon` from an agent shell is enough.)
//
// CLAUDE_CODE_CHILD_SESSION then tells the new claude it is a nested
// invocation and it stops writing a transcript — silently losing the very
// thing recall exists to find. The others name a parent that is already gone,
// down to a messaging socket for a dead pid.
//
// Cleared per launch rather than in the daemon, because the daemon is a
// desktop app nobody is going to restart on a schedule. Only Claude's
// variables are listed: Codex's equivalents, if any, are not known here and
// guessing at names would clear nothing while looking like it did.
var staleSessionEnv = []string{
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_PID",
	"CLAUDE_CODE_MESSAGING_SOCKET",
	"CLAUDE_CODE_MESSAGING_TOKEN",
}

// hotkeyEnvPrefix is the env(1) invocation every hotkey command runs through:
// it announces the launch with RECALL_NOTIFY and drops any inherited session
// identity. Real configuration (ANTHROPIC_MODEL, credentials, PATH) is left
// alone — only per-session identity is cleared.
func hotkeyEnvPrefix() string {
	parts := make([]string, 0, 2*len(staleSessionEnv)+2)
	parts = append(parts, "env")
	for _, name := range staleSessionEnv {
		parts = append(parts, "-u", name)
	}
	return strings.Join(append(parts, "RECALL_NOTIFY=1"), " ")
}

// removeMarkedBlock cuts the region between two markers, inclusive, leaving a
// single newline where it stood. Both platforms fence their generated bindings
// this way so that a hand-edited config keeps everything outside the fence.
func removeMarkedBlock(data []byte, beginMarker, endMarker string) []byte {
	contents := string(data)
	begin := strings.Index(contents, beginMarker)
	if begin < 0 {
		return data
	}
	endRelative := strings.Index(contents[begin:], endMarker)
	if endRelative < 0 {
		return data
	}
	end := begin + endRelative + len(endMarker)
	if end < len(contents) && contents[end] == '\n' {
		end++
	}
	return []byte(strings.TrimRight(contents[:begin], "\n") + "\n" + strings.TrimLeft(contents[end:], "\n"))
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
