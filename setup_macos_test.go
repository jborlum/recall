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

func TestMacOSSetupLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".hammerspoon", "init.lua")
	t.Setenv("RECALL_HAMMERSPOON_CONFIG", path)
	t.Setenv("RECALL_SETUP_NO_RELOAD", "1")

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
	if code := runSetupMacOS([]string{"status"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("status exit = %d", code)
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

	var errors bytes.Buffer
	if code := runSetupMacOS(nil, &bytes.Buffer{}, &errors); code != 1 {
		t.Fatalf("setup exit = %d", code)
	}
	if !strings.Contains(errors.String(), "already assigned") {
		t.Fatalf("error = %q", errors.String())
	}
}

func TestHammerspoonSnippetQuotesExecutablePath(t *testing.T) {
	configured := string(appendMacOSBindings(nil, "/Applications/Recall Tool/recall"))
	if !strings.Contains(configured, "'/Applications/Recall Tool/recall'") {
		t.Fatalf("executable path is not shell quoted:\n%s", configured)
	}
}

func TestHammerspoonSnippetIsValidLua(t *testing.T) {
	luac, err := exec.LookPath("luac")
	if err != nil {
		t.Skip("luac is not installed")
	}
	path := filepath.Join(t.TempDir(), "init.lua")
	writeFixture(t, path, string(appendMacOSBindings(nil, "/opt/homebrew/bin/recall")))
	if output, err := exec.Command(luac, "-p", path).CombinedOutput(); err != nil {
		t.Fatalf("invalid generated Lua: %v: %s", err, output)
	}
}
