//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func legacyTaskDir() string {
	pf := os.Getenv("ProgramW6432")
	if pf == "" {
		pf = os.Getenv("ProgramFiles")
	}
	return filepath.Join(pf, "KeRemotePlay")
}

func legacyTaskPresent() bool {
	if _, err := os.Stat(legacyTaskDir()); err == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "schtasks", "/query", "/tn", `\KeRemotePlay\PadOrder`)
	hideCmd(c)
	return c.Run() == nil
}

func removeLegacyTask() {
	_, _ = runPowerShell(`Unregister-ScheduledTask -TaskName 'PadOrder' -TaskPath '\KeRemotePlay\' -Confirm:$false -ErrorAction SilentlyContinue`)
	_ = os.RemoveAll(legacyTaskDir())
}

func padSwitchedOff(id string) bool { return devNeedsRepair(id) }
func padSwitchOn(id string) error   { return devEnable(id) }
