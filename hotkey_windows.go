//go:build windows

package main

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"
)

const (
	hotkeyID    = 0x4B52 // "KR"
	modControl  = 0x0002
	modNoRepeat = 0x4000
	vkF4        = 0x73
	wmHotkey    = 0x0312
	pmRemove    = 1
)

var (
	procRegisterHotKey   = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey = user32.NewProc("UnregisterHotKey")
	procPeekMessage      = user32.NewProc("PeekMessageW")
)

// hotkeyLoop keeps Ctrl+F4 reserved while want() is true, so the key still works
// normally in other programs outside a session. A hotkey belongs to the thread
// that registered it, hence the locked thread.
func hotkeyLoop(want func() bool, fire func()) {
	runtime.LockOSThread()
	on := false
	var msg [48]byte // MSG
	for {
		if w := want(); w != on {
			if w {
				r, _, err := procRegisterHotKey.Call(0, hotkeyID, modControl|modNoRepeat, vkF4)
				if r == 0 {
					logf(L("Ctrl+F4 alınamadı: %v", "Could not reserve Ctrl+F4: %v"), err)
					setHotkeyErr(fmt.Sprint(err))
				} else {
					on = true
					setHotkeyErr("")
				}
			} else {
				_, _, _ = procUnregisterHotKey.Call(0, hotkeyID)
				on = false
			}
		}
		for {
			r, _, _ := procPeekMessage.Call(uintptr(unsafe.Pointer(&msg[0])), 0, wmHotkey, wmHotkey, pmRemove)
			if r == 0 {
				break
			}
			fire()
		}
		time.Sleep(100 * time.Millisecond)
	}
}
