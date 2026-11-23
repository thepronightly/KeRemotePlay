//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Held for the whole run; Windows drops it when the process ends.
var instanceMutex windows.Handle

// singleInstance keeps one KeRemotePlay running. Starting the same file again
// brings the open window to the front and quits; starting another copy (e.g.
// KeRemotePlay-0.9.3.exe while 0.9.2 is open) closes the old one and takes over,
// so two copies never fight over the pairing port or run setups twice.
func singleInstance() bool {
	self, _ := os.Executable()
	name, _ := windows.UTF16PtrFromString(`Local\KeRemotePlay`)
	h, err := windows.CreateMutex(nil, false, name)
	instanceMutex = h
	running := err == windows.ERROR_ALREADY_EXISTS
	var same []uint32
	for _, p := range otherInstances() {
		if strings.EqualFold(filepath.Clean(p.path), filepath.Clean(self)) {
			same = append(same, p.pid)
			continue
		}
		logf("Başka bir KeRemotePlay kopyası kapatılıyor: %s (pid %d)", filepath.Base(p.path), p.pid)
		endProcess(p.pid)
	}
	if running && len(same) > 0 {
		logf("Zaten açık; açık pencere öne getiriliyor")
		for _, pid := range same {
			focusWindowOf(pid)
		}
		return false
	}
	return true
}

type procInfo struct {
	pid  uint32
	path string
}

func otherInstances() []procInfo {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	me := windows.GetCurrentProcessId()
	var out []procInfo
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		n := strings.ToLower(windows.UTF16ToString(e.ExeFile[:]))
		if e.ProcessID == me || !strings.HasPrefix(n, "keremoteplay") || !strings.HasSuffix(n, ".exe") {
			continue
		}
		if p := processPath(e.ProcessID); p != "" {
			out = append(out, procInfo{e.ProcessID, p})
		}
	}
	return out
}

func processPath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func endProcess(pid uint32) {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		logf("Kapatılamadı (pid %d): %v", pid, err)
		return
	}
	defer windows.CloseHandle(h)
	_ = windows.TerminateProcess(h, 0)
	_, _ = windows.WaitForSingleObject(h, 5000)
}

var (
	procEnumWindows         = user32.NewProc("EnumWindows") // user32: clip_windows.go
	procGetWindowThreadPID  = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procIsIconic            = user32.NewProc("IsIconic")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procFlashWindowEx       = user32.NewProc("FlashWindowEx")
	procMessageBeep         = user32.NewProc("MessageBeep")
)

// windowOf returns the visible top-level window of process pid.
func windowOf(pid uint32) uintptr {
	var found uintptr
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		var wpid uint32
		_, _, _ = procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&wpid)))
		if wpid == pid {
			if v, _, _ := procIsWindowVisible.Call(hwnd); v != 0 {
				found = hwnd
				return 0
			}
		}
		return 1
	})
	_, _, _ = procEnumWindows.Call(cb, 0)
	return found
}

// focusWindowOf brings the window of process pid to the front.
func focusWindowOf(pid uint32) {
	hwnd := windowOf(pid)
	if hwnd == 0 {
		return
	}
	if ic, _, _ := procIsIconic.Call(hwnd); ic != 0 {
		_, _, _ = procShowWindow.Call(hwnd, 9) // SW_RESTORE
	}
	_, _, _ = procSetForegroundWindow.Call(hwnd)
}

// hostAlert draws attention to a waiting connection request: the window stays on
// top of everything else, its taskbar button flashes and a sound plays. It does
// not take the focus, so a key pressed in a game cannot answer the question.
func hostAlert(on bool) {
	hwnd := windowOf(windows.GetCurrentProcessId())
	if hwnd == 0 {
		return
	}
	const swpNoSizeMoveActivate = 0x0001 | 0x0002 | 0x0010
	if !on {
		_, _, _ = procSetWindowPos.Call(hwnd, ^uintptr(1), 0, 0, 0, 0, swpNoSizeMoveActivate) // HWND_NOTOPMOST
		return
	}
	if ic, _, _ := procIsIconic.Call(hwnd); ic != 0 {
		_, _, _ = procShowWindow.Call(hwnd, 4) // SW_SHOWNOACTIVATE
	}
	_, _, _ = procSetWindowPos.Call(hwnd, ^uintptr(0), 0, 0, 0, 0, swpNoSizeMoveActivate) // HWND_TOPMOST
	fi := struct {
		size    uint32
		hwnd    uintptr
		flags   uint32
		count   uint32
		timeout uint32
	}{hwnd: hwnd, flags: 3, count: 5} // FLASHW_ALL
	fi.size = uint32(unsafe.Sizeof(fi))
	_, _, _ = procFlashWindowEx.Call(uintptr(unsafe.Pointer(&fi)))
	_, _, _ = procMessageBeep.Call(0x40) // MB_ICONASTERISK
}
