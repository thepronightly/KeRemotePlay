//go:build !windows

package main

import "os"

// Development stand-ins: KRP_FAKE_SUNSVC=stopped / locked simulates the service
// being off / still needing the one-time admin setup.
func sunServiceState() SunService {
	switch os.Getenv("KRP_FAKE_SUNSVC") {
	case "stopped":
		return SunService{Exists: true, Manual: true, CanControl: true}
	case "locked":
		return SunService{Exists: true, Running: true}
	}
	return SunService{Exists: true, Running: true, Manual: true, CanControl: true}
}

func sunServiceSet(on bool) error { return nil }

func currentUserSID() string { return "" }
