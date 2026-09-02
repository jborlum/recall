package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

type bookmarkAction int

const (
	actionOpen bookmarkAction = iota
	actionDelete
)

func manageBookmarks(sessions []session, bookmarks map[string]bookmark, opts options, in io.Reader, out, errOut io.Writer) int {
	if !terminalIO(in, out) {
		printSessions(out, pinnedSessions(sessions, bookmarks), opts.limit)
		return 0
	}
	for {
		pinned := pinnedSessions(sessions, bookmarks)
		if len(pinned) == 0 {
			message := "No bookmarks"
			fmt.Fprintln(out, message)
			notify(message)
			return 0
		}
		selected, action, err := pickBookmark(pinned, opts, in, out, errOut)
		if err != nil {
			if errors.Is(err, errCancelled) {
				return 130
			}
			fmt.Fprintf(errOut, "recall: %v\n", err)
			return 1
		}
		if action == actionDelete {
			confirmed, err := confirmNo(in, out, fmt.Sprintf("Delete bookmark %q? [y/N] ", selected.Label))
			if err != nil {
				fmt.Fprintf(errOut, "recall: %v\n", err)
				return 1
			}
			if !confirmed {
				continue
			}
			delete(bookmarks, selected.Label)
			if err := saveBookmarks(bookmarks); err != nil {
				fmt.Fprintf(errOut, "recall: save bookmarks: %v\n", err)
				return 1
			}
			message := fmt.Sprintf("Deleted bookmark %s", selected.Label)
			fmt.Fprintln(out, message)
			notify(message)
			continue
		}
		if selected.Missing {
			fmt.Fprintf(errOut, "recall: bookmark %q points to a missing session; delete it with Ctrl+D\n", selected.Label)
			continue
		}
		if err := launch(selected, false, in, out, errOut); err != nil {
			if errors.Is(err, errCancelled) {
				return 130
			}
			fmt.Fprintf(errOut, "recall: %v\n", err)
			return 1
		}
		return 0
	}
}

func confirmNo(in io.Reader, out io.Writer, prompt string) (bool, error) {
	fmt.Fprint(out, prompt)
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	case "", "n", "no":
		return false, nil
	default:
		return false, errors.New("expected yes or no")
	}
}

func pickBookmark(items []session, opts options, in io.Reader, out, errOut io.Writer) (session, bookmarkAction, error) {
	limit := opts.limit
	if len(items) < limit {
		limit = len(items)
	}
	candidates := items[:limit]
	if !opts.noFZF {
		if path, err := exec.LookPath("fzf"); err == nil {
			var input strings.Builder
			for index, item := range candidates {
				fmt.Fprintf(&input, "%d\t%s\n", index, displayLine(item))
			}
			var selected bytes.Buffer
			cmd := exec.Command(
				path,
				"--delimiter=\\t",
				"--with-nth=2..",
				"--expect=enter,ctrl-d",
				"--header=Enter: open   Ctrl-D: delete   Esc: close",
				"--prompt=bookmarks> ",
				"--height=80%",
				"--reverse",
			)
			cmd.Stdin = strings.NewReader(input.String())
			cmd.Stdout = &selected
			cmd.Stderr = errOut
			if err := cmd.Run(); err != nil {
				return session{}, actionOpen, errCancelled
			}
			index, action, err := parseBookmarkSelection(selected.String())
			if err != nil || index < 0 || index >= len(candidates) {
				return session{}, actionOpen, errors.New("invalid picker result")
			}
			return candidates[index], action, nil
		}
	}

	for index, item := range candidates {
		fmt.Fprintf(out, "%2d  %s\n", index+1, displayLine(item))
	}
	fmt.Fprint(out, "Open NUMBER, d NUMBER to delete, or q: ")
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return session{}, actionOpen, err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" || strings.EqualFold(answer, "q") {
		return session{}, actionOpen, errCancelled
	}
	action := actionOpen
	if strings.HasPrefix(strings.ToLower(answer), "d ") {
		action = actionDelete
		answer = strings.TrimSpace(answer[2:])
	}
	index, err := strconv.Atoi(answer)
	if err != nil || index < 1 || index > len(candidates) {
		return session{}, actionOpen, errors.New("invalid selection")
	}
	return candidates[index-1], action, nil
}

func parseBookmarkSelection(value string) (int, bookmarkAction, error) {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return 0, actionOpen, errors.New("empty selection")
	}
	action := actionOpen
	row := lines[0]
	if len(lines) > 1 {
		if lines[0] == "ctrl-d" {
			action = actionDelete
		}
		row = lines[1]
	}
	field := strings.SplitN(row, "\t", 2)[0]
	index, err := strconv.Atoi(field)
	return index, action, err
}
