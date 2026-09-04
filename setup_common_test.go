package main

import (
	"strings"
	"testing"
)

// A hotkey always starts a top-level session. Inheriting
// CLAUDE_CODE_CHILD_SESSION from the daemon that fired it makes claude treat
// the session as nested and stop writing a transcript — silently losing the
// thing recall exists to find.
func TestHotkeyEnvPrefixClearsInheritedSession(t *testing.T) {
	prefix := hotkeyEnvPrefix()
	if !strings.HasPrefix(prefix, "env ") {
		t.Fatalf("prefix is not an env(1) call: %q", prefix)
	}
	if !strings.HasSuffix(prefix, " RECALL_NOTIFY=1") {
		t.Fatalf("prefix no longer announces the launch: %q", prefix)
	}
	for _, name := range staleSessionEnv {
		if !strings.Contains(prefix, "-u "+name) {
			t.Errorf("prefix does not clear %s: %q", name, prefix)
		}
	}
}

// Only per-session identity may be cleared. Wiping configuration would break
// every launch on a machine that configures a model or a gateway by
// environment, which is how the enterprise setups do it.
func TestHotkeyEnvPrefixKeepsConfiguration(t *testing.T) {
	prefix := hotkeyEnvPrefix()
	for _, keep := range []string{"PATH", "HOME", "SHELL", "ANTHROPIC_MODEL", "ANTHROPIC_API_KEY", "AWS_PROFILE"} {
		if strings.Contains(prefix, "-u "+keep) {
			t.Errorf("prefix clears %s, which is configuration and not session identity: %q", keep, prefix)
		}
	}
}
