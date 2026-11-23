//go:build windows

package main

import (
	"path/filepath"
	"syscall"

	webview2 "github.com/jchv/go-webview2"
)

// systemScale returns the display scale factor (1.0 = 100%) so the window has the
// same visual size on high-DPI screens (the manifest makes the app DPI-aware).
func systemScale() float64 {
	proc := syscall.NewLazyDLL("user32.dll").NewProc("GetDpiForSystem")
	if proc.Find() != nil {
		return 1
	}
	dpi, _, _ := proc.Call()
	if dpi < 96 || dpi > 480 {
		return 1
	}
	return float64(dpi) / 96
}

func runWindow(url string) {
	s := systemScale()
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		DataPath:  filepath.Join(dataDir(), "webview"),
		WindowOptions: webview2.WindowOptions{
			Title:  "KeRemotePlay",
			Width:  uint(1040 * s),
			Height: uint(800 * s),
			IconId: 2, // rsrc: manifest=1, icon group=2
			Center: true,
		},
	})
	if w == nil {
		// WebView2 runtime missing: fall back to the default browser.
		logf("%s", L("WebView2 açılamadı, arayüz tarayıcıda açılıyor", "WebView2 did not open, opening the interface in the browser"))
		_ = openURL(url)
		<-quitCh
		return
	}
	defer w.Destroy()
	go func() {
		<-quitCh
		w.Dispatch(func() { w.Terminate() })
	}()
	w.Navigate(url)
	w.Run()
}
