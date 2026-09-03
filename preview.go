package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// The picker shows the conversation in fzf's preview panel, which keeps the
// formatted row in the list intact: the searchable transcript text has to share
// the row's field for fzf to match it, and a match far to the right used to drag
// that text into view. The panel is rendered by re-running recall with --preview,
// so only the selected session is ever read.
const (
	// previewContext is how many lines of the conversation are shown either side
	// of a matching line, so a hit arrives with enough around it to read.
	previewContext = 2
	// previewMaxHits and previewMaxLines only guard against pathological
	// transcripts; both are far above anything observed, and reaching either says
	// so in the panel rather than truncating in silence. Neither bounds the cost
	// of a redraw, which is dominated by parsing the file.
	previewMaxHits  = 500
	previewMaxLines = 8000
	previewHit      = "\x1b[7m"
	previewRole     = "\x1b[1;36m"
	previewReset    = "\x1b[0m"
	previewNote     = "\x1b[2m"
)

// previewTurn is one thing said, split into lines so hits can be shown with
// their surroundings.
type previewTurn struct {
	role  string
	lines []string
}

// previewMatch is one window of a turn around one or more matching lines.
type previewMatch struct {
	role  string
	lines []string
}

// previewCommand is the shell command fzf runs for the selected row. {3} is the
// transcript path and {q} the current query, both quoted by fzf. A missing path
// means a bookmark whose transcript is gone, so the panel says so rather than
// reporting a failure to open it.
func previewCommand() string {
	binary, err := os.Executable()
	if err != nil {
		binary = "recall"
	}
	quoted := shellQuote(binary)
	return "[ -f {3} ] && " + quoted + " --preview {3} -- {q} || echo '(no transcript to preview)'"
}

func previewTranscript(path, query string, out, errOut io.Writer) int {
	var turns []previewTurn
	err := transcriptTurns(path, func(role, text string) {
		turns = append(turns, previewTurn{role, strings.Split(strings.TrimRight(text, " \t\n"), "\n")})
	})
	if err != nil {
		fmt.Fprintf(errOut, "recall: preview %s: %v\n", path, err)
		return 1
	}
	tokens := strings.Fields(strings.ToLower(query))
	if matches := previewMatches(turns, tokens); len(matches) > 0 {
		writeMatches(out, matches)
		return 0
	}
	// A query can match a title or a directory and nothing that was said, so the
	// conversation is shown rather than an empty panel.
	writeConversation(out, turns)
	return 0
}

// previewMatches finds the windows worth showing. A line holding every term is
// the best hit, so those are preferred; failing that, fzf may have matched terms
// spread across the conversation, and a line holding any of them still shows
// where the search landed.
func previewMatches(turns []previewTurn, tokens []string) []previewMatch {
	if len(tokens) == 0 {
		return nil
	}
	for _, everyToken := range []bool{true, false} {
		var matches []previewMatch
		for _, turn := range turns {
			for _, window := range matchWindows(turn.lines, tokens, everyToken) {
				matches = append(matches, previewMatch{turn.role, window})
			}
		}
		if len(matches) > 0 {
			return matches
		}
	}
	return nil
}

// matchWindows returns each run of matching lines with its surrounding context,
// merging runs that would otherwise repeat the same lines.
func matchWindows(lines []string, tokens []string, everyToken bool) [][]string {
	var marked []int
	for index, line := range lines {
		lowered := strings.ToLower(line)
		matched := containsAny(lowered, tokens)
		if everyToken {
			matched = containsAll(lowered, tokens)
		}
		if matched {
			marked = append(marked, index)
		}
	}
	if len(marked) == 0 {
		return nil
	}
	var windows [][]string
	start, end := marked[0], marked[0]
	for _, index := range marked[1:] {
		if index-end <= 2*previewContext+1 {
			end = index
			continue
		}
		windows = append(windows, window(lines, start, end, tokens))
		start, end = index, index
	}
	return append(windows, window(lines, start, end, tokens))
}

// window cuts lines[first:last] widened by previewContext and marks the terms.
func window(lines []string, first, last int, tokens []string) []string {
	from, to := max(first-previewContext, 0), min(last+previewContext, len(lines)-1)
	result := make([]string, 0, to-from+1)
	for _, line := range lines[from : to+1] {
		for _, token := range tokens {
			line = highlight(line, token)
		}
		result = append(result, strings.TrimRight(line, " \t"))
	}
	return result
}

func writeMatches(out io.Writer, matches []previewMatch) {
	shown := matches
	if len(shown) > previewMaxHits {
		shown = shown[:previewMaxHits]
	}
	for index, match := range shown {
		fmt.Fprintf(out, "%s── %d/%d ── %s%s\n", previewRole, index+1, len(matches), match.role, previewReset)
		for _, line := range match.lines {
			fmt.Fprintln(out, line)
		}
		fmt.Fprintln(out)
	}
	if len(shown) < len(matches) {
		fmt.Fprintf(out, "%s%d more matches not shown%s\n", previewNote, len(matches)-len(shown), previewReset)
	}
}

func writeConversation(out io.Writer, turns []previewTurn) {
	written := 0
	for _, turn := range turns {
		if written >= previewMaxLines {
			fmt.Fprintf(out, "%sconversation continues beyond %d lines%s\n", previewNote, previewMaxLines, previewReset)
			return
		}
		fmt.Fprintf(out, "%s%s%s\n", previewRole, turn.role, previewReset)
		for _, line := range turn.lines {
			fmt.Fprintln(out, line)
		}
		fmt.Fprintln(out)
		written += len(turn.lines) + 2
	}
}

func containsAny(haystack string, tokens []string) bool {
	for _, token := range tokens {
		if strings.Contains(haystack, token) {
			return true
		}
	}
	return false
}

// highlight marks every case-insensitive occurrence of token. Matching on a
// lowered copy keeps the original casing in the output.
func highlight(text, token string) string {
	if token == "" {
		return text
	}
	lowered := strings.ToLower(text)
	var result strings.Builder
	for {
		index := strings.Index(lowered, token)
		if index < 0 {
			result.WriteString(text)
			return result.String()
		}
		result.WriteString(text[:index])
		result.WriteString(previewHit + text[index:index+len(token)] + previewReset)
		text, lowered = text[index+len(token):], lowered[index+len(token):]
	}
}

// transcriptTurns reports each user or assistant message in a transcript. Both
// providers are handled here rather than in their parsers, because a preview is
// given a path and no provider.
func transcriptTurns(path string, fn func(role, text string)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	internalCodex := false
	transcriptLines(file, func(line []byte) {
		if meta, internal := codexSessionMetadata(line); meta {
			internalCodex = internal
			return
		}
		if internalCodex {
			return
		}
		if role, text, ok := codexTurn(line); ok {
			fn(role, text)
			return
		}
		if role, text, ok := claudeTurn(line); ok {
			fn(role, text)
		}
	})
	return nil
}

func codexSessionMetadata(line []byte) (bool, bool) {
	var event codexEnvelope
	if json.Unmarshal(line, &event) != nil || event.Type != "session_meta" {
		return false, false
	}
	return true, internalCodexSession(event)
}

func codexTurn(line []byte) (string, string, bool) {
	var event codexEnvelope
	if json.Unmarshal(line, &event) != nil {
		return "", "", false
	}
	return visibleCodexTurn(event)
}

func claudeTurn(line []byte) (string, string, bool) {
	var event claudeEvent
	if json.Unmarshal(line, &event) != nil || len(event.Message) == 0 {
		return "", "", false
	}
	var message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(event.Message, &message) != nil {
		return "", "", false
	}
	text := contentText(message.Content)
	if strings.HasPrefix(strings.TrimSpace(text), commandCaveat) {
		return "", "", false
	}
	return speech(message.Role, text)
}

func speech(role, text string) (string, string, bool) {
	if role != "user" && role != "assistant" {
		return "", "", false
	}
	if strings.TrimSpace(text) == "" {
		return "", "", false
	}
	return role, text, true
}
