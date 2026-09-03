package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// stripANSI removes the highlight codes so assertions read as plain text.
func stripANSI(value string) string {
	for {
		start := strings.Index(value, "\x1b[")
		if start < 0 {
			return value
		}
		end := strings.IndexByte(value[start:], 'm')
		if end < 0 {
			return value
		}
		value = value[:start] + value[start+end+1:]
	}
}

func previewFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeFixture(t, path,
		claudeUser("s", "First question about the deployment")+
			claudeAssistant("s", "Opening paragraph that matches nothing\nThe relay dropped because Zscaler re-authenticated\nA closing line")+
			claudeUser("s", caveat)+
			claudeAssistant("s", "Unrelated follow up"))
	return path
}

// The panel exists to show where the query hit, so a long answer must surface the
// matching line rather than its opening paragraph.
func TestPreviewShowsMatchingLinesOnly(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := previewTranscript(previewFixture(t), "zscaler", &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	text := stripANSI(out.String())
	if !strings.Contains(text, "Zscaler re-authenticated") {
		t.Errorf("matching line missing: %q", text)
	}
	if strings.Contains(text, "Opening paragraph") || strings.Contains(text, "A closing line") {
		t.Errorf("non-matching lines of the same turn were included: %q", text)
	}
	if !strings.Contains(text, "assistant") {
		t.Errorf("the speaker should be labelled: %q", text)
	}
	if !strings.Contains(out.String(), previewHit) {
		t.Error("the matched term should be highlighted")
	}
}

// A query can match a title or a directory and nothing that was said. An empty
// panel would look like a failure, so the conversation is shown instead.
func TestPreviewFallsBackToTheConversation(t *testing.T) {
	var out, errOut bytes.Buffer
	previewTranscript(previewFixture(t), "term-that-appears-nowhere", &out, &errOut)
	if text := stripANSI(out.String()); !strings.Contains(text, "First question about the deployment") {
		t.Errorf("expected the conversation as a fallback: %q", text)
	}
}

func TestPreviewWithoutQueryShowsTheConversation(t *testing.T) {
	var out, errOut bytes.Buffer
	previewTranscript(previewFixture(t), "", &out, &errOut)
	text := stripANSI(out.String())
	for _, want := range []string{"First question about the deployment", "Opening paragraph", "Unrelated follow up"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q from %q", want, text)
		}
	}
	if strings.Contains(text, "DO NOT respond") {
		t.Error("slash-command bookkeeping should not appear in a preview")
	}
}

func TestPreviewReadsCodexTranscripts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeFixture(t, path,
		`{"type":"session_meta","payload":{"id":"c","cwd":"/work"}}`+"\n"+
			`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Fix the refresh token"}]}}`+"\n"+
			`{"type":"response_item","payload":{"type":"reasoning","encrypted_content":"never-shown"}}`+"\n")
	var out, errOut bytes.Buffer
	if code := previewTranscript(path, "refresh", &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	text := stripANSI(out.String())
	if !strings.Contains(text, "Fix the refresh token") {
		t.Errorf("codex turn missing: %q", text)
	}
	if strings.Contains(text, "never-shown") {
		t.Error("encrypted reasoning content leaked into the preview")
	}
}

func TestPreviewReportsAMissingFile(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := previewTranscript(filepath.Join(t.TempDir(), "gone.jsonl"), "", &out, &errOut); code == 0 {
		t.Fatal("a missing transcript should fail")
	}
	if !strings.Contains(errOut.String(), "preview") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestHighlightKeepsOriginalCasing(t *testing.T) {
	got := highlight("A Zscaler and a zscaler", "zscaler")
	if strings.Count(got, previewHit) != 2 {
		t.Errorf("both occurrences should be marked: %q", got)
	}
	if stripANSI(got) != "A Zscaler and a zscaler" {
		t.Errorf("casing changed: %q", stripANSI(got))
	}
}

// fzf substitutes the transcript path and the query into this command, so both
// placeholders have to survive into it.
func TestPreviewCommandCarriesThePlaceholders(t *testing.T) {
	command := previewCommand()
	for _, want := range []string{"{3}", "{q}", "--preview"} {
		if !strings.Contains(command, want) {
			t.Errorf("preview command is missing %q: %s", want, command)
		}
	}
}

// The path travels in its own field, which fzf must not display.
func TestPickerLineKeepsThePathOutOfTheVisibleField(t *testing.T) {
	line := pickerLine(0, "* claude 2026-09-03  Title", "searchable words", "/tmp/t.jsonl", 60)
	fields := strings.Split(strings.TrimSuffix(line, "\n"), "\t")
	if len(fields) != 3 {
		t.Fatalf("want 3 fields, got %d: %q", len(fields), line)
	}
	if fields[2] != "/tmp/t.jsonl" {
		t.Errorf("field 3 = %q, want the transcript path", fields[2])
	}
	if strings.Contains(fields[1], "/tmp/t.jsonl") {
		t.Errorf("the path leaked into the displayed field: %q", fields[1])
	}
}
