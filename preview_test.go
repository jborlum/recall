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

// A hit has to arrive with enough of the conversation around it to read, but not
// the whole turn, or a long answer buries what matched.
func TestPreviewShowsAHitWithSurroundingContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "long.jsonl")
	writeFixture(t, path, claudeAssistant("s", strings.Join([]string{
		"far above one", "far above two", "just above",
		"The relay dropped because Zscaler re-authenticated",
		"just below", "far below one", "far below two",
	}, "\n")))
	var out, errOut bytes.Buffer
	if code := previewTranscript(path, "zscaler", &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	text := stripANSI(out.String())
	for _, want := range []string{"Zscaler re-authenticated", "just above", "just below", "assistant"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q from %q", want, text)
		}
	}
	for _, unwanted := range []string{"far above one", "far below two"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("line beyond the context window was included: %q", unwanted)
		}
	}
	if !strings.Contains(out.String(), previewHit) {
		t.Error("the matched term should be highlighted")
	}
}

// Cycling through matches only makes sense if each is labelled with its position.
func TestPreviewNumbersEveryMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "many.jsonl")
	turn := func(n string) string {
		return claudeAssistant("s", "padding\npadding\nhit about zscaler "+n+"\npadding\npadding")
	}
	writeFixture(t, path, turn("one")+turn("two")+turn("three"))
	var out, errOut bytes.Buffer
	previewTranscript(path, "zscaler", &out, &errOut)
	text := stripANSI(out.String())
	for _, want := range []string{"── 1/3 ──", "── 2/3 ──", "── 3/3 ──"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing label %q from %q", want, text)
		}
	}
}

// Matches close together would otherwise repeat the same context lines twice.
func TestPreviewMergesAdjacentMatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adjacent.jsonl")
	writeFixture(t, path, claudeAssistant("s", "zscaler first\nmiddle\nzscaler second"))
	var out, errOut bytes.Buffer
	previewTranscript(path, "zscaler", &out, &errOut)
	text := stripANSI(out.String())
	if !strings.Contains(text, "── 1/1 ──") {
		t.Errorf("adjacent matches should merge into one window: %q", text)
	}
	if strings.Count(text, "middle") != 1 {
		t.Errorf("context repeated: %q", text)
	}
}

// fzf matches terms anywhere in the transcript, so they need not share a line.
// A line holding all of them is the better hit, but one term is still a hit.
func TestPreviewFallsBackToPartialTermMatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spread.jsonl")
	writeFixture(t, path,
		claudeUser("s", "a question about zscaler")+
			claudeAssistant("s", "an answer about wireguard"))
	var out, errOut bytes.Buffer
	previewTranscript(path, "zscaler wireguard", &out, &errOut)
	text := stripANSI(out.String())
	if !strings.Contains(text, "zscaler") || !strings.Contains(text, "wireguard") {
		t.Errorf("both terms should appear as separate matches: %q", text)
	}
	if !strings.Contains(text, "── 1/2 ──") {
		t.Errorf("want two numbered matches: %q", text)
	}
}

// A line holding every term is preferred over lines holding only one.
func TestPreviewPrefersLinesHoldingEveryTerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "both.jsonl")
	writeFixture(t, path,
		claudeAssistant("s", "only zscaler here\n\n\n\n\n\nzscaler and wireguard together"))
	var out, errOut bytes.Buffer
	previewTranscript(path, "zscaler wireguard", &out, &errOut)
	text := stripANSI(out.String())
	if !strings.Contains(text, "zscaler and wireguard together") {
		t.Errorf("the line with both terms should be the match: %q", text)
	}
	if strings.Contains(text, "only zscaler here") {
		t.Errorf("a line with one term should not be shown when a better one exists: %q", text)
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

func TestCodexPreviewHidesBootstrapMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeFixture(t, path,
		`{"type":"session_meta","payload":{"id":"c","cwd":"/work","thread_source":"user","source":"cli"}}`+"\n"+
			`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context><cwd>/work</cwd></environment_context>"}]}}`+"\n"+
			`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /work"}]}}`+"\n"+
			`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Fix the refresh token"}]}}`+"\n")
	var out, errOut bytes.Buffer
	if code := previewTranscript(path, "", &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	text := stripANSI(out.String())
	if !strings.Contains(text, "Fix the refresh token") {
		t.Errorf("visible user turn missing: %q", text)
	}
	for _, hidden := range []string{"environment_context", "AGENTS.md"} {
		if strings.Contains(text, hidden) {
			t.Errorf("%q leaked into preview: %q", hidden, text)
		}
	}
}

func TestCodexPreviewHidesInternalSubagentTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guardian.jsonl")
	writeFixture(t, path,
		`{"type":"session_meta","payload":{"id":"guardian","cwd":"/work","thread_source":"guardian_review","source":{"subagent":{"other":"guardian"}}}}`+"\n"+
			`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"The following is the Codex agent history whose request action you are assessing"}]}}`+"\n")
	var out, errOut bytes.Buffer
	if code := previewTranscript(path, "request", &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	if strings.TrimSpace(stripANSI(out.String())) != "" {
		t.Fatalf("internal subagent transcript leaked into preview: %q", out.String())
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
