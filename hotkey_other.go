//go:build !windows

package main

func hotkeyLoop(want func() bool, fire func()) {}

func hostAlert(on bool) {}
