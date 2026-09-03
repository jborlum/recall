package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

func setupWord(configured bool) string {
	if configured {
		return "configured"
	}
	return "missing"
}

// removeMarkedBlock cuts the region between two markers, inclusive, leaving a
// single newline where it stood. Both platforms fence their generated bindings
// this way so that a hand-edited config keeps everything outside the fence.
func removeMarkedBlock(data []byte, beginMarker, endMarker string) []byte {
	contents := string(data)
	begin := strings.Index(contents, beginMarker)
	if begin < 0 {
		return data
	}
	endRelative := strings.Index(contents[begin:], endMarker)
	if endRelative < 0 {
		return data
	}
	end := begin + endRelative + len(endMarker)
	if end < len(contents) && contents[end] == '\n' {
		end++
	}
	return []byte(strings.TrimRight(contents[:begin], "\n") + "\n" + strings.TrimLeft(contents[end:], "\n"))
}

func replaceBindings(path string, previous, updated []byte, mode os.FileMode) (string, error) {
	backup := path + ".bak.recall-" + time.Now().Format("20060102-150405.000000000")
	file, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return "", err
	}
	if _, err := file.Write(previous); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := atomicWrite(path, updated, mode); err != nil {
		return "", err
	}
	return backup, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".recall-bindings-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
