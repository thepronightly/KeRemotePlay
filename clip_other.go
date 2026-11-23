//go:build !windows

package main

import (
	"os"
	"path/filepath"
)

// Development stand-in: the "clipboard" is a file in the data folder.
func clipFile() string { return filepath.Join(dataDir(), "clipboard.txt") }

func clipReadText() string {
	b, _ := os.ReadFile(clipFile())
	return string(b)
}

func clipWriteText(s string) error { return os.WriteFile(clipFile(), []byte(s), 0o600) }
