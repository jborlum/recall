//go:build !linux && !darwin

package main

import "io"

func isPlatformSetupCommand(string) bool { return false }

func runPlatformSetup(_ []string, _, _ io.Writer) int { return 2 }

func platformSetupUsage() string { return "" }
