//go:build darwin

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pinTerminal keeps tests independent of the terminal the suite happens to run
// in, since setup otherwise detects it from TERM_PROGRAM.
func pinTerminal(t *testing.T, name string) {
	t.Helper()
	t.Setenv("RECALL_TERMINAL", name)
	t.Setenv("TERM_PROGRAM", "")
}

func TestMacOSSetupLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".hammerspoon", "init.lua")
	t.Setenv("RECALL_HAMMERSPOON_CONFIG", path)
	t.Setenv("RECALL_SETUP_NO_RELOAD", "1")
	pinTerminal(t, "terminal")

	var output bytes.Buffer
	if code := runSetupMacOS(nil, &output, &bytes.Buffer{}); code != 0 {
		t.Fatalf("setup exit = %d", code)
	}
	configured, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contents := string(configured)
	if !strings.Contains(contents, macSetupBegin) || !strings.Contains(contents, `"B"`) ||
		!strings.Contains(contents, "bookmark active") || !strings.Contains(contents, "bookmark") {
		t.Fatalf("missing managed hotkeys:\n%s", configured)
	}

	output.Reset()
	if code := runSetupMacOS(nil, &output, &bytes.Buffer{}); code != 0 || !strings.Contains(output.String(), "already configured") {
		t.Fatalf("second setup: exit=%d output=%q", code, output.String())
	}

	output.Reset()
	if code := runSetupMacOS([]string{"status"}, &output, &bytes.Buffer{}); code != 0 {
		t.Fatalf("status exit = %d", code)
	}
	if !strings.Contains(output.String(), "terminal: terminal") {
		t.Fatalf("status does not report the terminal: %q", output.String())
	}

	if code := runSetupMacOS([]string{"remove"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("remove exit = %d", code)
	}
	removed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(removed), macSetupBegin) {
		t.Fatalf("hotkeys were not removed:\n%s", removed)
	}
}

func TestMacOSSetupPreservesExistingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.lua")
	writeFixture(t, path, "-- personal config\n")
	t.Setenv("RECALL_HAMMERSPOON_CONFIG", path)
	t.Setenv("RECALL_SETUP_NO_RELOAD", "1")
	pinTerminal(t, "terminal")

	if code := runSetupMacOS(nil, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("setup exit = %d", code)
	}
	configured, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configured), "-- personal config") {
		t.Fatal("existing Hammerspoon config was removed")
	}
}

func TestMacOSSetupDetectsConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.lua")
	writeFixture(t, path, `hs.hotkey.bind({"cmd", "alt"}, "B", function() end)`+"\n")
	t.Setenv("RECALL_HAMMERSPOON_CONFIG", path)
	t.Setenv("RECALL_SETUP_NO_RELOAD", "1")
	pinTerminal(t, "terminal")

	var errors bytes.Buffer
	if code := runSetupMacOS(nil, &bytes.Buffer{}, &errors); code != 1 {
		t.Fatalf("setup exit = %d", code)
	}
	if !strings.Contains(errors.String(), "already assigned") {
		t.Fatalf("error = %q", errors.String())
	}
}

// Switching terminals has to rewrite the managed block in place rather than
// reporting that the hotkeys are already configured.
func TestMacOSSetupSwitchesTerminal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.lua")
	t.Setenv("RECALL_HAMMERSPOON_CONFIG", path)
	t.Setenv("RECALL_SETUP_NO_RELOAD", "1")
	pinTerminal(t, "terminal")

	if code := runSetupMacOS(nil, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("install exit = %d", code)
	}
	var output bytes.Buffer
	if code := runSetupMacOS([]string{"--terminal", "ghostty"}, &output, &bytes.Buffer{}); code != 0 {
		t.Fatalf("switch exit = %d output = %q", code, output.String())
	}
	configured, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contents := string(configured)
	if strings.Contains(contents, `tell application \"Terminal\"`) {
		t.Fatalf("Terminal.app bindings survived the switch:\n%s", contents)
	}
	if !strings.Contains(contents, "Ghostty.app") || !strings.Contains(contents, macTerminalMarker+"ghostty") {
		t.Fatalf("ghostty bindings were not written:\n%s", contents)
	}
	if strings.Count(contents, macSetupBegin) != 1 {
		t.Fatalf("managed block was duplicated:\n%s", contents)
	}
}

// An upgrade that changes the generated Lua must refresh an existing block even
// though the configured terminal name is unchanged.
func TestMacOSSetupRefreshesOutdatedBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.lua")
	stale := macSetupBegin + "\n" + macTerminalMarker + "ghostty\n-- stale body\n" + macSetupEnd + "\n"
	writeFixture(t, path, stale)
	t.Setenv("RECALL_HAMMERSPOON_CONFIG", path)
	t.Setenv("RECALL_SETUP_NO_RELOAD", "1")
	pinTerminal(t, "ghostty")

	var output bytes.Buffer
	if code := runSetupMacOS(nil, &output, &bytes.Buffer{}); code != 0 {
		t.Fatalf("setup exit = %d", code)
	}
	if strings.Contains(output.String(), "already configured") {
		t.Fatalf("stale block was left in place: %q", output.String())
	}
	configured, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configured), "-- stale body") {
		t.Fatalf("stale body survived:\n%s", configured)
	}
	if !strings.Contains(string(configured), "hs.hotkey.bind") {
		t.Fatalf("bindings were not written:\n%s", configured)
	}
}

func TestMacOSSetupRejectsUnknownTerminal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.lua")
	t.Setenv("RECALL_HAMMERSPOON_CONFIG", path)
	t.Setenv("RECALL_SETUP_NO_RELOAD", "1")

	var errors bytes.Buffer
	if code := runSetupMacOS([]string{"--terminal", "nope"}, &bytes.Buffer{}, &errors); code != 2 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(errors.String(), "unknown terminal") || !strings.Contains(errors.String(), "ghostty") {
		t.Fatalf("error = %q", errors.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("config was written despite an unknown terminal")
	}
}

func TestMacOSSetupRequiresTerminalValue(t *testing.T) {
	var errors bytes.Buffer
	if code := runSetupMacOS([]string{"--terminal"}, &bytes.Buffer{}, &errors); code != 2 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(errors.String(), "requires a name") {
		t.Fatalf("error = %q", errors.String())
	}
}

func TestDefaultMacTerminalFollowsTermProgram(t *testing.T) {
	cases := map[string]string{
		"ghostty":        "ghostty",
		"iTerm.app":      "iterm",
		"Apple_Terminal": "terminal",
		"":               "terminal",
		"something-else": "terminal",
	}
	for value, want := range cases {
		t.Setenv("RECALL_TERMINAL", "")
		t.Setenv("TERM_PROGRAM", value)
		if got := defaultMacTerminal(); got != want {
			t.Errorf("TERM_PROGRAM=%q gave %q, want %q", value, got, want)
		}
	}
}

func TestRecallTerminalOverridesTermProgram(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	t.Setenv("RECALL_TERMINAL", "terminal")
	if got := defaultMacTerminal(); got != "terminal" {
		t.Fatalf("defaultMacTerminal = %q, want terminal", got)
	}
}

func TestHammerspoonSnippetQuotesExecutablePath(t *testing.T) {
	for _, terminal := range macTerminalNames() {
		configured := string(appendMacOSBindings(nil, "/Applications/Recall Tool/recall", terminal))
		if !strings.Contains(configured, "'/Applications/Recall Tool/recall'") {
			t.Errorf("%s: executable path is not shell quoted:\n%s", terminal, configured)
		}
	}
}

// Ghostty asks the running instance for a window over AppleScript, which is
// cheaper than the second app instance open(1) starts, and keeps open(1) as the
// fallback for a cold or pre-1.3 Ghostty.
func TestGhosttySnippetPrefersRunningInstance(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	configured := string(appendMacOSBindings(nil, "/opt/homebrew/bin/recall", "ghostty"))
	for _, want := range []string{"hs.osascript.applescript", "new window with configuration", "/usr/bin/open", "-na", "Ghostty.app", "--args", "-e", "/bin/zsh"} {
		if !strings.Contains(configured, want) {
			t.Fatalf("missing %q in ghostty bindings:\n%s", want, configured)
		}
	}
}

// Both Ghostty paths run the real command, so both have to clear the inherited
// session. Asserted as an invariant rather than a count: nothing may set
// RECALL_NOTIFY without also dropping the parent's child-session marker.
func TestBindingsAlwaysClearInheritedSession(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	for _, terminal := range macTerminalNames() {
		configured := string(appendMacOSBindings(nil, "/opt/homebrew/bin/recall", terminal))
		announced := strings.Count(configured, "RECALL_NOTIFY=1")
		cleared := strings.Count(configured, "-u CLAUDE_CODE_CHILD_SESSION")
		if announced == 0 {
			t.Errorf("%s: no command announces the launch:\n%s", terminal, configured)
			continue
		}
		if announced != cleared {
			t.Errorf("%s: %d commands set RECALL_NOTIFY but only %d clear the child-session marker:\n%s",
				terminal, announced, cleared, configured)
		}
	}
}

// Addressing a Ghostty that is not running would launch it and let it open its
// own initial window on top of the scripted one, so the script has to check
// first and hand the cold start to open(1).
func TestGhosttySnippetGuardsAgainstColdGhostty(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	configured := string(appendMacOSBindings(nil, "/opt/homebrew/bin/recall", "ghostty"))
	if !strings.Contains(configured, `is running then`) {
		t.Fatalf("ghostty bindings do not check whether Ghostty is running:\n%s", configured)
	}
	if !strings.Contains(configured, `if not hs.osascript.applescript(`) {
		t.Fatalf("ghostty bindings do not fall back when the script fails:\n%s", configured)
	}
}

// Neither open(1) nor a scripted Ghostty window starts a login shell, so
// without -l -i the PATH entries that locate fzf and the provider CLIs are
// missing and resuming a session fails. Both paths have to name one.
func TestGhosttySnippetUsesInteractiveLoginShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	configured := string(appendMacOSBindings(nil, "/opt/homebrew/bin/recall", "ghostty"))
	if !strings.Contains(configured, `"/bin/zsh", "-l", "-i", "-c"`) {
		t.Fatalf("ghostty open(1) fallback does not use an interactive login shell:\n%s", configured)
	}
	if !strings.Contains(configured, `/bin/zsh -l -i -c `) {
		t.Fatalf("ghostty scripted window does not use an interactive login shell:\n%s", configured)
	}
}

func TestLoginShellFallsBackForUnusableShell(t *testing.T) {
	for _, value := range []string{"", "zsh", "relative/zsh"} {
		t.Setenv("SHELL", value)
		if got := loginShell(); got != "/bin/sh" {
			t.Errorf("SHELL=%q gave %q, want /bin/sh", value, got)
		}
	}
	t.Setenv("SHELL", "/usr/local/bin/fish")
	if got := loginShell(); got != "/usr/local/bin/fish" {
		t.Errorf("loginShell = %q, want the configured absolute shell", got)
	}
}

func TestHammerspoonSnippetIsValidLua(t *testing.T) {
	luac, err := exec.LookPath("luac")
	if err != nil {
		t.Skip("luac is not installed")
	}
	for _, terminal := range macTerminalNames() {
		path := filepath.Join(t.TempDir(), terminal+".lua")
		writeFixture(t, path, string(appendMacOSBindings(nil, "/opt/homebrew/bin/recall", terminal)))
		if output, err := exec.Command(luac, "-p", path).CombinedOutput(); err != nil {
			t.Errorf("%s: invalid generated Lua: %v: %s", terminal, err, output)
		}
	}
}
