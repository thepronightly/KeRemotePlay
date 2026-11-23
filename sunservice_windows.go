//go:build windows

package main

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const sunServiceName = "SunshineService"

// sunServiceState reads SunshineService without admin rights. Sunshine runs only
// while the host screen is open, so the service must not start with Windows and
// this user must be allowed to start and stop it (set up once, see runSetup).
func sunServiceState() SunService {
	var s SunService
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return s
	}
	defer windows.CloseServiceHandle(scm)
	name, _ := windows.UTF16PtrFromString(sunServiceName)
	h, err := windows.OpenService(scm, name, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return s
	}
	defer windows.CloseServiceHandle(h)
	s.Exists = true
	var st windows.SERVICE_STATUS
	if windows.QueryServiceStatus(h, &st) == nil {
		s.Running = st.CurrentState == windows.SERVICE_RUNNING || st.CurrentState == windows.SERVICE_START_PENDING
	}
	var need uint32
	_ = windows.QueryServiceConfig(h, nil, 0, &need)
	if need > 0 {
		buf := make([]byte, need)
		cfg := (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&buf[0]))
		if windows.QueryServiceConfig(h, cfg, need, &need) == nil {
			s.Manual = cfg.StartType == windows.SERVICE_DEMAND_START
		}
	}
	if c, err := windows.OpenService(scm, name, windows.SERVICE_START|windows.SERVICE_STOP); err == nil {
		s.CanControl = true
		windows.CloseServiceHandle(c)
	}
	return s
}

// sunServiceSet starts or stops SunshineService; it does not wait for it.
func sunServiceSet(on bool) error {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(scm)
	name, _ := windows.UTF16PtrFromString(sunServiceName)
	h, err := windows.OpenService(scm, name, windows.SERVICE_START|windows.SERVICE_STOP|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(h)
	if !on {
		var st windows.SERVICE_STATUS
		if err := windows.ControlService(h, windows.SERVICE_CONTROL_STOP, &st); err != nil && err != windows.ERROR_SERVICE_NOT_ACTIVE {
			return err
		}
		return nil
	}
	// A stop still in progress would refuse the start.
	for i := 0; i < 75; i++ {
		var st windows.SERVICE_STATUS
		if windows.QueryServiceStatus(h, &st) != nil || st.CurrentState != windows.SERVICE_STOP_PENDING {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := windows.StartService(h, 0, nil); err != nil && err != windows.ERROR_SERVICE_ALREADY_RUNNING {
		return err
	}
	return nil
}

func currentUserSID() string {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return ""
	}
	return u.User.Sid.String()
}
