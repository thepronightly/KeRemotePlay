//go:build !windows

package main

import "os"

// Development stand-ins: KRP_FAKE_LEGACY=1 pretends the old task is installed.
func legacyTaskPresent() bool       { return os.Getenv("KRP_FAKE_LEGACY") == "1" }
func removeLegacyTask()             {}
func padSwitchedOff(id string) bool { return false }
func padSwitchOn(id string) error   { return nil }
