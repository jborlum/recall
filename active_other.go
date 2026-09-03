//go:build !linux && !darwin

package main

func detectPlatformActiveSessions(_ []session, _, _ map[string]int, _ map[int]bool) {}

func platformNotify(string) error { return nil }
