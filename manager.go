package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
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
		selected, action, err := pick(pinned, opts, true, true, "bookmarks> ", in, errOut)
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
		if err := launch(selected, action == actionFork, in, out, errOut); err != nil {
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

func parseSelection(value string) (int, selectionAction, error) {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return 0, actionOpen, errors.New("empty selection")
	}
	action := actionOpen
	row := lines[0]
	if len(lines) > 1 {
		switch lines[0] {
		case "ctrl-f":
			action = actionFork
		case "ctrl-d":
			action = actionDelete
		}
		row = lines[1]
	}
	field := strings.SplitN(row, "\t", 2)[0]
	index, err := strconv.Atoi(field)
	return index, action, err
}
