//go:build windows

package main

// Re-enabling devices through the Configuration Manager API (cfgmgr32.dll), the
// layer Device Manager uses. Only needed to undo what the removed player-order
// panel of 0.8.x may have left switched off.

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

var (
	cfgmgr          = syscall.NewLazyDLL("cfgmgr32.dll")
	cmLocate        = cfgmgr.NewProc("CM_Locate_DevNodeW")
	cmStatus        = cfgmgr.NewProc("CM_Get_DevNode_Status")
	cmEnable        = cfgmgr.NewProc("CM_Enable_DevNode")
	cmSetup         = cfgmgr.NewProc("CM_Setup_DevNode")
	cmParent        = cfgmgr.NewProc("CM_Get_Parent")
	cmReenumerate   = cfgmgr.NewProc("CM_Reenumerate_DevNode")
	cmGetRegProp    = cfgmgr.NewProc("CM_Get_DevNode_Registry_PropertyW")
	cmSetRegProp    = cfgmgr.NewProc("CM_Set_DevNode_Registry_PropertyW")
	errNoSuchDevice = newErr("cihaz yok", "no device")
)

const (
	crSuccess          = 0x00
	crNoSuchDevnode    = 0x0D
	crNotDisableable   = 0x21
	crAccessDenied     = 0x33
	cmLocateNormal     = 0x0
	cmLocatePhantom    = 0x1
	cmSetupReady       = 0x0
	cmReenumSync       = 0x1
	cmDrpConfigFlags   = 0x0B
	configFlagDisabled = 0x1
	dnStarted          = 0x8
	dnHasProblem       = 0x400
	cmProbDisabled     = 22
	cmProbNeedRestart  = 14
)

type crError uint32

func (c crError) Error() string {
	switch uint32(c) {
	case crNotDisableable:
		return L("bu cihaz kapatılamıyor", "this device cannot be disabled")
	case crAccessDenied:
		return L("yönetici izni yok", "no admin permission")
	case crNoSuchDevnode:
		return L("cihaz bulunamadı", "device not found")
	}
	return fmt.Sprintf("CONFIGRET 0x%02X", uint32(c))
}

func cr(r uintptr) error {
	if uint32(r) == crSuccess {
		return nil
	}
	return crError(uint32(r))
}

func devLocate(id string, phantom bool) (uint32, error) {
	p, err := syscall.UTF16PtrFromString(id)
	if err != nil {
		return 0, err
	}
	var dn uint32
	flags := uintptr(cmLocateNormal)
	if phantom {
		flags = cmLocatePhantom
	}
	r, _, _ := cmLocate.Call(uintptr(unsafe.Pointer(&dn)), uintptr(unsafe.Pointer(p)), flags)
	if e := cr(r); e != nil {
		return 0, e
	}
	return dn, nil
}

// devStatus: ok=false when the device is not present.
func devStatus(dn uint32) (status, problem uint32, ok bool) {
	r, _, _ := cmStatus.Call(uintptr(unsafe.Pointer(&status)), uintptr(unsafe.Pointer(&problem)), uintptr(dn), 0)
	return status, problem, uint32(r) == crSuccess
}

func devConfigFlags(dn uint32) (uint32, bool) {
	var typ, v uint32
	n := uint32(4)
	r, _, _ := cmGetRegProp.Call(uintptr(dn), cmDrpConfigFlags, uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&v)), uintptr(unsafe.Pointer(&n)), 0)
	return v, uint32(r) == crSuccess
}

// devClearPersistentDisable removes the "disabled" flag that Device Manager,
// Disable-PnpDevice or a vetoed disable leave in the registry.
func devClearPersistentDisable(dn uint32) bool {
	v, ok := devConfigFlags(dn)
	if !ok || v&configFlagDisabled == 0 {
		return false
	}
	v &^= configFlagDisabled
	r, _, _ := cmSetRegProp.Call(uintptr(dn), cmDrpConfigFlags, uintptr(unsafe.Pointer(&v)), 4, 0)
	return uint32(r) == crSuccess
}

func devRunning(id string) bool {
	dn, err := devLocate(id, false)
	if err != nil {
		return false
	}
	st, prob, ok := devStatus(dn)
	return ok && st&dnStarted != 0 && st&dnHasProblem == 0 && prob == 0
}

func devNeedsRepair(id string) bool {
	dn, err := devLocate(id, true)
	if err != nil {
		return false
	}
	if v, ok := devConfigFlags(dn); ok && v&configFlagDisabled != 0 {
		return true
	}
	st, prob, ok := devStatus(dn)
	if !ok {
		return false // not present (controller off / unplugged)
	}
	return st&dnStarted == 0 || prob == cmProbDisabled || prob == cmProbNeedRestart
}

// devEnable brings a device back from a temporary or persistent disable, or
// from a removal.
func devEnable(id string) error {
	dn, err := devLocate(id, true)
	if err != nil {
		return err
	}
	if devClearPersistentDisable(dn) {
		logf(L("aç: %s kalıcı kapalı işareti temizlendi", "enable: cleared the disabled flag of %s"), id)
	}
	r, _, _ := cmEnable.Call(uintptr(dn), 0)
	enErr := cr(r)
	if waitFor(func() bool { return devRunning(id) }, 1500*time.Millisecond) {
		return nil
	}
	_, _, _ = cmSetup.Call(uintptr(dn), cmSetupReady)
	if waitFor(func() bool { return devRunning(id) }, 1500*time.Millisecond) {
		return nil
	}
	var parent uint32
	if r, _, _ := cmParent.Call(uintptr(unsafe.Pointer(&parent)), uintptr(dn), 0); uint32(r) == crSuccess {
		_, _, _ = cmReenumerate.Call(uintptr(parent), cmReenumSync)
	}
	if waitFor(func() bool { return devRunning(id) }, 2*time.Second) {
		return nil
	}
	if _, prob, ok := devStatus(dn); ok {
		return fmt.Errorf(L("başlamadı (sorun kodu %d)", "did not start (problem code %d)"), prob)
	} else if enErr != nil {
		return enErr
	}
	return errNoSuchDevice
}
