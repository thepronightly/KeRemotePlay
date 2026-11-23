//go:build !windows

package main

// Non-Windows build exists only for development and testing: binaries are
// looked up on PATH so mocks can stand in for tailscale / Moonlight / sunshine.

import (
	"os"
	"os/exec"
)

func hideCmd(c *exec.Cmd) {}

func askToClose(c *exec.Cmd) { _ = c.Process.Signal(os.Interrupt) }

func openURL(u string) error {
	logf("Tarayıcıda aç: %s", u)
	if p, err := exec.LookPath("xdg-open"); err == nil {
		return exec.Command(p, u).Start()
	}
	return nil
}

func look(name string) string {
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

func tailscaleExe() string { return look("tailscale") }
func sunshineExe() string  { return look("sunshine") }
func moonlightExe() string { return look("moonlight") }

func sunshineConfFile() string { return os.Getenv("KRP_SUNSHINE_CONF") }

func tailscaleLoginExtraArgs() []string { return nil }

func wingetPath() string { return look("winget") }

func runElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	c := exec.Command(exe, args...)
	out, err := c.CombinedOutput()
	if err != nil {
		logf("elevated: %s", out)
	}
	return err
}

func detectNetwork() NetInfo { return NetInfo{} }

func primaryDisplay() (w, h, hz int) { return 1920, 1080, 60 }

func lockMainThread() {}

func vigemPath() string { return "/dev/null" }

func vigemInstalled() bool { return true }

func detectAudio() AudioInfo {
	return AudioInfo{Known: true, VirtualSink: "Steam Streaming Speakers", Steam: true}
}

func openTailscaleApp() error { return nil }

func isElevatedProcess() bool { return os.Geteuid() == 0 }
