//go:build !darwin && !linux

package main

func terminalColumns() int { return 0 }
