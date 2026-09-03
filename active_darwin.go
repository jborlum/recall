//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func detectPlatformActiveSessions(sessions []session, _ map[string]int, byPath map[string]int, active map[int]bool) {
	psPath := firstNonEmpty(os.Getenv("RECALL_PS"), "/bin/ps")
	output, err := exec.Command(psPath, "-axo", "pid=,command=").Output()
	if err != nil {
		return
	}
	fallbacks := map[string]int{}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		provider := commandProvider(strings.Join(fields[1:], " "))
		if provider == "" {
			continue
		}
		paths, cwd := darwinProcessPaths(fields[0])
		matched := false
		for _, path := range paths {
			if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
				path = resolved
			}
			if index, ok := byPath[filepath.Clean(path)]; ok {
				active[index], matched = true, true
			}
		}
		if !matched && cwd != "" {
			fallbacks[provider+"\x00"+cwd]++
		}
	}
	applyActiveFallbacks(sessions, fallbacks, active)
}

func darwinProcessPaths(pid string) ([]string, string) {
	if _, err := strconv.Atoi(pid); err != nil {
		return nil, ""
	}
	lsofPath := firstNonEmpty(os.Getenv("RECALL_LSOF"), "/usr/sbin/lsof")
	output, _ := exec.Command(lsofPath, "-p", pid, "-Fn").Output()
	return parseLsofOutput(output)
}

func parseLsofOutput(output []byte) ([]string, string) {
	var paths []string
	cwd := ""
	cwdRecord := false
	for _, line := range strings.Split(string(output), "\n") {
		switch {
		case strings.HasPrefix(line, "f"):
			cwdRecord = strings.TrimPrefix(line, "f") == "cwd"
		case strings.HasPrefix(line, "n"):
			path := strings.TrimPrefix(line, "n")
			if filepath.IsAbs(path) {
				paths = append(paths, path)
				if cwdRecord {
					cwd = path
				}
			}
		}
	}
	return paths, cwd
}

func platformNotify(message string) {
	script := `display notification ` + appleScriptQuote(message) + ` with title "Recall"`
	_ = exec.Command("/usr/bin/osascript", "-e", script).Run()
}

func appleScriptQuote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
