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
	// previewMaxLines bounds a panel that is redrawn on every keystroke.
	previewMaxLines = 300
	previewHit      = "\x1b[7m"
	previewRole     = "\x1b[1;36m"
	previewReset    = "\x1b[0m"
)

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
	tokens := strings.Fields(strings.ToLower(query))
	var hits, opening []string
	err := transcriptTurns(path, func(role, text string) {
		header := previewRole + role + previewReset
		if len(opening) < previewMaxLines {
			opening = append(opening, header)
			opening = append(opening, strings.Split(strings.TrimRight(text, " \t\n"), "\n")...)
			opening = append(opening, "")
		}
		if len(tokens) == 0 || len(hits) >= previewMaxLines {
			return
		}
		if lines := matchingLines(text, tokens); len(lines) > 0 && containsAll(strings.ToLower(text), tokens) {
			hits = append(hits, header)
			hits = append(hits, lines...)
			hits = append(hits, "")
		}
	})
	if err != nil {
		fmt.Fprintf(errOut, "recall: preview %s: %v\n", path, err)
		return 1
	}
	// Matching lines are the point of the panel, but a query can match a title or
	// a directory and nothing that was said, which would leave it blank.
	lines := hits
	if len(lines) == 0 {
		lines = opening
	}
	if len(lines) > previewMaxLines {
		lines = lines[:previewMaxLines]
	}
	for _, line := range lines {
		fmt.Fprintln(out, line)
	}
	return 0
}

// matchingLines picks out the lines of a turn that mention a query token, so a
// long answer shows the part that matched rather than its opening paragraph.
func matchingLines(text string, tokens []string) []string {
	var result []string
	for _, line := range strings.Split(text, "\n") {
		lowered, matched := strings.ToLower(line), false
		for _, token := range tokens {
			if strings.Contains(lowered, token) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		for _, token := range tokens {
			line = highlight(line, token)
		}
		result = append(result, strings.TrimRight(line, " \t"))
	}
	return result
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
	transcriptLines(file, func(line []byte) {
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

func codexTurn(line []byte) (string, string, bool) {
	var event codexEnvelope
	if json.Unmarshal(line, &event) != nil || event.Payload.Type != "message" {
		return "", "", false
	}
	return speech(event.Payload.Role, contentText(event.Payload.Content))
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
