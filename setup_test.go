package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOmarchySetupLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hypr", "bindings.lua")
	writeFixture(t, path, "-- personal bindings\n")
	t.Setenv("RECALL_HYPR_BINDINGS", path)
	t.Setenv("RECALL_SETUP_NO_RELOAD", "1")

	var output bytes.Buffer
	if code := runSetupOmarchy(nil, &output, &bytes.Buffer{}); code != 0 {
		t.Fatalf("setup exit = %d", code)
	}
	configured, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configured), setupBegin) || !strings.Contains(string(configured), " pin-active") || !strings.Contains(string(configured), " bookmarks") {
		t.Fatalf("missing managed bindings:\n%s", configured)
	}

	output.Reset()
	if code := runSetupOmarchy(nil, &output, &bytes.Buffer{}); code != 0 || !strings.Contains(output.String(), "already configured") {
		t.Fatalf("second setup: exit=%d output=%q", code, output.String())
	}
	if code := runSetupOmarchy([]string{"status"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("status exit = %d", code)
	}
	if code := runSetupOmarchy([]string{"remove"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("remove exit = %d", code)
	}
	removed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(removed), "recall pin-active") || strings.Contains(string(removed), setupBegin) {
		t.Fatalf("bindings were not removed:\n%s", removed)
	}
}

func TestOmarchySetupDetectsConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bindings.lua")
	writeFixture(t, path, `o.bind("SUPER + ALT + B", "Browser", "chromium")`+"\n")
	t.Setenv("RECALL_HYPR_BINDINGS", path)
	t.Setenv("RECALL_SETUP_NO_RELOAD", "1")

	var errors bytes.Buffer
	if code := runSetupOmarchy(nil, &bytes.Buffer{}, &errors); code != 1 {
		t.Fatalf("setup exit = %d", code)
	}
	if !strings.Contains(errors.String(), "already assigned") {
		t.Fatalf("error = %q", errors.String())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), setupBegin) {
		t.Fatal("conflicting config was modified")
	}
}

func TestRemoveRecognizesManualRecallBindings(t *testing.T) {
	contents := "-- keep\n" + omarchyBinding(bookmarkKey, "Bookmark AI conversation", "/usr/bin/recall pin-active") +
		omarchyBinding(managerKey, "Recall bookmarked conversation", "/usr/bin/recall bookmarks")
	updated := string(removeRecallBindings([]byte(contents)))
	if strings.Contains(updated, "recall pin-active") || strings.Contains(updated, "recall bookmarks") {
		t.Fatalf("manual recall bindings remain:\n%s", updated)
	}
	if !strings.Contains(updated, "-- keep") {
		t.Fatal("unrelated configuration was removed")
	}
}

func TestSetupMigratesManualRecallBindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bindings.lua")
	contents := "-- keep\n" + omarchyBinding(bookmarkKey, "Bookmark AI conversation", "/old/recall pin-active") +
		omarchyBinding(managerKey, "Recall bookmarked conversation", "/old/recall bookmarks")
	writeFixture(t, path, contents)
	t.Setenv("RECALL_HYPR_BINDINGS", path)
	t.Setenv("RECALL_SETUP_NO_RELOAD", "1")

	if code := runSetupOmarchy(nil, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("setup exit = %d", code)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(updated), setupBegin) != 1 || strings.Contains(string(updated), "/old/recall") {
		t.Fatalf("manual bindings were not migrated:\n%s", updated)
	}
}
