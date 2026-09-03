//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func detectPlatformActiveSessions(sessions []session, byID, byPath map[string]int, active map[int]bool) {
	psPath := firstNonEmpty(os.Getenv("RECALL_PS"), "/bin/ps")
	output, err := exec.Command(psPath, "-axo", "pid=,etime=,command=").Output()
	if err != nil {
		return
	}
	environments := darwinProcessEnvironments(psPath)
	fallbacks := map[string][]time.Time{}
	now := time.Now()
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 {
			continue
		}
		provider := commandProvider(strings.Join(fields[2:], " "))
		if provider == "" {
			continue
		}
		matched := false
		for _, key := range environments[fields[0]] {
			if index, ok := byID[key]; ok {
				active[index], matched = true, true
			}
		}
		paths, cwd := darwinProcessPaths(fields[0])
		for _, path := range paths {
			if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
				path = resolved
			}
			if index, ok := byPath[filepath.Clean(path)]; ok {
				active[index], matched = true, true
			}
		}
		if !matched && cwd != "" {
			key := provider + "\x00" + cwd
			fallbacks[key] = append(fallbacks[key], processStart(now, fields[1]))
		}
	}
	applyActiveFallbacks(sessions, fallbacks, active)
}

// darwinProcessEnvironments maps each pid to the byID keys implied by the
// session-id variables in its environment, mirroring the /proc/PID/environ scan
// on Linux. macOS exposes process environments only through ps -E, and only for
// processes the caller owns. The command column is read from a separate ps call
// so that variables like _=/usr/local/bin/claude cannot be mistaken for a
// running provider.
func darwinProcessEnvironments(psPath string) map[string][]string {
	output, err := exec.Command(psPath, "-Ewwo", "pid=,command=").Output()
	if err != nil {
		return nil
	}
	result := map[string][]string{}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		for _, field := range fields[1:] {
			name, value, found := strings.Cut(field, "=")
			if !found {
				continue
			}
			if provider := sessionIDVariable(name); provider != "" && value != "" {
				result[fields[0]] = append(result[fields[0]], provider+"\x00"+value)
			}
		}
	}
	return result
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

// processStart converts a ps etime value into a wall-clock start time. A zero
// time means the elapsed time was unparseable, which skips the transcript age
// check for that process rather than guessing.
func processStart(now time.Time, etime string) time.Time {
	elapsed, ok := parseElapsed(etime)
	if !ok {
		return time.Time{}
	}
	return now.Add(-elapsed)
}

// parseElapsed reads the [[dd-]hh:]mm:ss form that ps uses for etime.
func parseElapsed(value string) (time.Duration, bool) {
	total := time.Duration(0)
	if before, after, found := strings.Cut(value, "-"); found {
		days, err := strconv.Atoi(before)
		if err != nil || days < 0 {
			return 0, false
		}
		total, value = time.Duration(days)*24*time.Hour, after
	}
	parts := strings.Split(value, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	units := []time.Duration{time.Hour, time.Minute, time.Second}[3-len(parts):]
	for index, part := range parts {
		amount, err := strconv.Atoi(part)
		if err != nil || amount < 0 {
			return 0, false
		}
		total += time.Duration(amount) * units[index]
	}
	return total, true
}

func platformNotify(message string) error {
	script := `display notification ` + appleScriptQuote(message) + ` with title "Recall"`
	return exec.Command("/usr/bin/osascript", "-e", script).Run()
}

func appleScriptQuote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
