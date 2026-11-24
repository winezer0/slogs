//go:build !windows && !linux

package main

// Soak execution is limited to Windows/Linux; other targets are compile-only.
func acceptanceProcessMemory() (rss, private uint64, err error) { return 0, 0, nil }
