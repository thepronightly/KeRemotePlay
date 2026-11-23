//go:build windows

package main

import (
	"errors"
	"runtime"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	pOpenClipboard   = user32.NewProc("OpenClipboard")
	pCloseClipboard  = user32.NewProc("CloseClipboard")
	pEmptyClipboard  = user32.NewProc("EmptyClipboard")
	pGetClipboard    = user32.NewProc("GetClipboardData")
	pSetClipboard    = user32.NewProc("SetClipboardData")
	pIsFormatAvail   = user32.NewProc("IsClipboardFormatAvailable")
	pGlobalAlloc     = kernel32.NewProc("GlobalAlloc")
	pGlobalFree      = kernel32.NewProc("GlobalFree")
	pGlobalLock      = kernel32.NewProc("GlobalLock")
	pGlobalUnlock    = kernel32.NewProc("GlobalUnlock")
	pGlobalSize      = kernel32.NewProc("GlobalSize")
	errClipboardBusy = errors.New("pano meşgul")
)

const cfUnicodeText = 13

func openClipboard() bool {
	for i := 0; i < 10; i++ {
		if r, _, _ := pOpenClipboard.Call(0); r != 0 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// clipReadText returns the clipboard text ("" if none or not text).
func clipReadText() string {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if r, _, _ := pIsFormatAvail.Call(cfUnicodeText); r == 0 {
		return ""
	}
	if !openClipboard() {
		return ""
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipboard.Call(cfUnicodeText)
	if h == 0 {
		return ""
	}
	size, _, _ := pGlobalSize.Call(h)
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return ""
	}
	defer pGlobalUnlock.Call(h)
	n := int(size / 2)
	if n > 32768 {
		n = 32768
	}
	buf := unsafe.Slice((*uint16)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), n)
	for i, c := range buf {
		if c == 0 {
			buf = buf[:i]
			break
		}
	}
	return string(utf16.Decode(buf))
}

// clipWriteText puts text on the clipboard.
func clipWriteText(s string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	u := utf16.Encode([]rune(s + "\x00"))
	if !openClipboard() {
		return errClipboardBusy
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	const gmemMoveable = 0x2
	h, _, _ := pGlobalAlloc.Call(gmemMoveable, uintptr(len(u)*2))
	if h == 0 {
		return errors.New("bellek ayrılamadı")
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		pGlobalFree.Call(h)
		return errors.New("bellek kilitlenemedi")
	}
	copy(unsafe.Slice((*uint16)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), len(u)), u)
	pGlobalUnlock.Call(h)
	if r, _, _ := pSetClipboard.Call(cfUnicodeText, h); r == 0 {
		pGlobalFree.Call(h)
		return errors.New("panoya yazılamadı")
	}
	return nil
}
