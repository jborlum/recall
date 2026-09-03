//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func detectPlatformActiveSessions(sessions []session, byID, byPath map[string]int, active map[int]bool) {
	procRoot := os.Getenv("RECALL_PROC_ROOT")
	if procRoot == "" {
		procRoot = "/proc"
	}
	entries, _ := os.ReadDir(procRoot)
	fallbacks := map[string][]time.Time{}
	for _, entry := range entries {
		if !entry.IsDir() || !numeric(entry.Name()) {
			continue
		}
		processRoot := filepath.Join(procRoot, entry.Name())
		matched := false
		if data, err := os.ReadFile(filepath.Join(processRoot, "environ")); err == nil {
			for _, field := range strings.Split(string(data), "\x00") {
				name, value, found := strings.Cut(field, "=")
				if !found || value == "" {
					continue
				}
				provider := sessionIDVariable(name)
				if provider == "" {
					continue
				}
				if index, ok := byID[provider+"\x00"+value]; ok {
					active[index], matched = true, true
				}
			}
		}

		provider := linuxProcessProvider(processRoot)
		if provider == "" {
			continue
		}
		if descriptors, err := os.ReadDir(filepath.Join(processRoot, "fd")); err == nil {
			for _, descriptor := range descriptors {
				target, err := os.Readlink(filepath.Join(processRoot, "fd", descriptor.Name()))
				if err != nil {
					continue
				}
				if resolved, err := filepath.EvalSymlinks(target); err == nil {
					target = resolved
				}
				if index, ok := byPath[filepath.Clean(target)]; ok {
					active[index], matched = true, true
				}
			}
		}
		if matched {
			continue
		}
		cwd, err := os.Readlink(filepath.Join(processRoot, "cwd"))
		if err == nil {
			key := provider + "\x00" + cwd
			fallbacks[key] = append(fallbacks[key], linuxProcessStart(processRoot))
		}
	}
	applyActiveFallbacks(sessions, fallbacks, active)
}

// linuxProcessStart reads the process start time from the /proc entry's own
// modification time, which the kernel sets when the process is created. A zero
// time skips the transcript age check for that process.
func linuxProcessStart(processRoot string) time.Time {
	info, err := os.Stat(processRoot)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func numeric(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func linuxProcessProvider(root string) string {
	data, _ := os.ReadFile(filepath.Join(root, "comm"))
	if provider := commandProvider(string(data)); provider != "" {
		return provider
	}
	if command, err := os.ReadFile(filepath.Join(root, "cmdline")); err == nil {
		return commandProvider(strings.ReplaceAll(string(command), "\x00", " "))
	}
	return ""
}

func platformNotify(message string) error {
	path, err := exec.LookPath("notify-send")
	if err != nil {
		return err
	}
	return exec.Command(path, "Recall", message).Run()
}
