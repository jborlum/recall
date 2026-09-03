package main

import (
	"os"
	"path/filepath"
	"time"
)

func setupWord(configured bool) string {
	if configured {
		return "configured"
	}
	return "missing"
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
