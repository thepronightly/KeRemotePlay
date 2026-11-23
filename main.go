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
		logf("Açık kalmış Sunshine kapatılıyor")
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
		logf("Arayüz başlatılamadı: %v", err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	base, _, _ := strings.Cut(url, "?")
	logf("Arayüz: %s", base)
	runWindow(url)
	leaveRole(&Job{Name: "Çıkış"}, getConfig().Mode)
}
