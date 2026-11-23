//go:build !windows

package main

import "os"

// systemLang follows the usual locale variables (KRP_LANG first, for tests).
func systemLang() string {
	for _, k := range []string{"KRP_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "en"
}
