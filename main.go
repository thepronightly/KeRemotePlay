// KeRemotePlay: one small app that sets up Sunshine + Tailscale + Moonlight and
// pairs friends automatically, so remote play becomes "paste a code, press Play".
package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

const isWindows = runtime.GOOS == "windows"

func init() { lockMainThread() } // WebView2 needs the main OS thread

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "--elevated" {
		os.Exit(elevatedMain(os.Args[2]))
	}

	initLog()
	if !singleInstance() {
		return
	}
	loadConfig()
	// Sunshine only runs on the host screen; it may be left over from a crash.
	if s := sunServiceState(); s.Running && s.CanControl {
		logf("%s", L("Açık kalmış Sunshine kapatılıyor", "Stopping a Sunshine left running"))
		_ = sunServiceSet(false)
	}
	go healthLoop()
	kickHealth()

	addr := "127.0.0.1:0"
	if v := os.Getenv("KRP_UI_ADDR"); v != "" {
		addr = v
	}
	url, err := startUI(addr)
	if err != nil {
		logf(L("Arayüz başlatılamadı: %v", "Could not start the interface: %v"), err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	base, _, _ := strings.Cut(url, "?")
	logf(L("Arayüz: %s", "Interface: %s"), base)
	runWindow(url)
	leaveRole(&Job{Name: L("Çıkış", "Exit")}, getConfig().Mode)
}
