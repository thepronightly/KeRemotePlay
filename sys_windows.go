//go:build windows

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const createNoWindow = 0x08000000

// hideCmd keeps console tools (tailscale, winget, powershell, netsh) from flashing a window.
// Do NOT use it for GUI programs such as Moonlight.
func hideCmd(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// askToClose closes a program's window as the user would (no /F).
func askToClose(c *exec.Cmd) {
	k := exec.Command("taskkill", "/PID", strconv.Itoa(c.Process.Pid))
	hideCmd(k)
	_ = k.Run()
}

func openURL(u string) error {
	c := exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	hideCmd(c)
	return c.Start()
}

func programDirs() []string {
	var out []string
	seen := map[string]bool{}
	for _, k := range []string{"ProgramW6432", "ProgramFiles", "ProgramFiles(x86)"} {
		if v := os.Getenv(k); v != "" && !seen[strings.ToLower(v)] {
			seen[strings.ToLower(v)] = true
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		out = []string{`C:\Program Files`}
	}
	return out
}

func findExe(rel []string, pathName string) string {
	for _, d := range programDirs() {
		for _, r := range rel {
			p := filepath.Join(d, r)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	if pathName != "" {
		if p, err := exec.LookPath(pathName); err == nil {
			return p
		}
	}
	return ""
}

func tailscaleExe() string { return findExe([]string{`Tailscale\tailscale.exe`}, "tailscale.exe") }
func sunshineExe() string  { return findExe([]string{`Sunshine\sunshine.exe`}, "") }
func moonlightExe() string {
	return findExe([]string{`Moonlight Game Streaming\Moonlight.exe`}, "Moonlight.exe")
}

func sunshineConfFile() string {
	if p := os.Getenv("KRP_SUNSHINE_CONF"); p != "" { // tests
		return p
	}
	if exe := sunshineExe(); exe != "" {
		return filepath.Join(filepath.Dir(exe), "config", "sunshine.conf")
	}
	return ""
}

// Unattended mode needs admin rights on Windows; normal mode is enough for playing.
func tailscaleLoginExtraArgs() []string { return nil }

// wingetPath finds winget, including the App Execution Alias that LookPath may miss.
func wingetPath() string {
	if p, err := exec.LookPath("winget"); err == nil {
		return p
	}
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		p := filepath.Join(la, "Microsoft", "WindowsApps", "winget.exe")
		if _, err := os.Lstat(p); err == nil {
			return p
		}
	}
	return ""
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func psEncode(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		b[i*2] = byte(v)
		b[i*2+1] = byte(v >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func runPowerShell(script string) ([]byte, error) {
	// Progress records ("Preparing modules for first use") would otherwise end up
	// in stderr as CLIXML noise.
	c := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", psEncode("$ProgressPreference='SilentlyContinue'\n"+script))
	hideCmd(c)
	var so, se bytes.Buffer
	c.Stdout, c.Stderr = &so, &se
	err := c.Run()
	out := so.Bytes()
	if e := cleanPSErr(se.String()); err != nil && e != "" {
		out = append(append(bytes.TrimRight(out, "\r\n "), '\n'), e...)
	}
	return out, err
}

// runElevated re-launches this executable with admin rights (UAC prompt) and waits for it.
func runElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
	}
	script := fmt.Sprintf(
		"try { $p = Start-Process -FilePath %s -ArgumentList %s -Verb RunAs -WindowStyle Hidden -Wait -PassThru -ErrorAction Stop; exit $p.ExitCode } catch { Write-Output $_.Exception.Message; exit 1223 }",
		psQuote(exe), psQuote(strings.Join(quoted, " ")))
	out, err := runPowerShell(script)
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1223 {
			return fmt.Errorf("yönetici izni verilmedi (UAC penceresinde Evet'e basmak gerekiyor)")
		}
		return fmt.Errorf("yönetici işlemi başarısız: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// detectNetwork reports which adapter carries the default route and whether it is Wi-Fi.
func detectNetwork() NetInfo {
	script := `$r = Get-NetRoute -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | Sort-Object { $_.RouteMetric + (Get-NetIPInterface -InterfaceIndex $_.InterfaceIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue).InterfaceMetric } | Select-Object -First 1
if ($r) { $a = Get-NetAdapter -InterfaceIndex $r.InterfaceIndex -ErrorAction SilentlyContinue; if ($a) { Write-Output ("{0}|{1}|{2}|{3}" -f $a.Name, $a.NdisPhysicalMedium, $a.LinkSpeed, $a.InterfaceDescription) } }`
	out, err := runPowerShell(script)
	if err != nil {
		return NetInfo{}
	}
	line := strings.TrimSpace(string(out))
	parts := strings.Split(line, "|")
	if len(parts) < 4 {
		return NetInfo{}
	}
	desc := strings.ToLower(parts[3])
	medium := strings.TrimSpace(parts[1])
	wifi := medium == "9" || medium == "1" || medium == "Native802_11" || medium == "WirelessLan" ||
		strings.Contains(desc, "wi-fi") || strings.Contains(desc, "wireless") || strings.Contains(desc, "802.11")
	return NetInfo{Known: true, Adapter: parts[0], WiFi: wifi, Speed: parts[2]}
}

func lockMainThread() { runtime.LockOSThread() }

func vigemPath() string {
	p := filepath.Join(os.Getenv("SystemRoot"), "System32", "drivers", "ViGEmBus.sys")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

func vigemInstalled() bool { return vigemPath() != "" }

// detectAudio looks for a virtual speaker Sunshine can capture instead of the real
// output (Steam Streaming Speakers, or VB-Cable), and whether Steam is installed
// (Sunshine installs Steam's speaker driver itself when Steam is present).
func detectAudio() AudioInfo {
	script := `$v = Get-CimInstance Win32_SoundDevice -ErrorAction SilentlyContinue | Where-Object { $_.Name -match 'Steam Streaming Speakers|VB-Audio|CABLE' } | Select-Object -First 1 -ExpandProperty Name
$s = (Test-Path (Join-Path ${env:ProgramFiles(x86)} 'Steam\steam.exe')) -or [bool](Get-ItemProperty 'HKCU:\Software\Valve\Steam' -Name SteamPath -ErrorAction SilentlyContinue)
Write-Output ("{0}|{1}" -f $v, $s)`
	out, err := runPowerShell(script)
	if err != nil {
		return AudioInfo{}
	}
	name, steam, ok := strings.Cut(strings.TrimSpace(string(out)), "|")
	if !ok {
		return AudioInfo{}
	}
	return AudioInfo{Known: true, VirtualSink: strings.TrimSpace(name), Steam: strings.EqualFold(strings.TrimSpace(steam), "True")}
}

// primaryDisplay returns the current mode of the primary monitor via
// EnumDisplaySettingsW (DEVMODEW: dmSize@68, dmPelsWidth@172, dmPelsHeight@176, dmDisplayFrequency@184).
func primaryDisplay() (w, h, hz int) {
	var dm [220]byte
	binary.LittleEndian.PutUint16(dm[68:], 220)
	proc := syscall.NewLazyDLL("user32.dll").NewProc("EnumDisplaySettingsW")
	if proc.Find() != nil {
		return 1920, 1080, 60
	}
	const enumCurrentSettings = 0xFFFFFFFF
	r, _, _ := proc.Call(0, uintptr(enumCurrentSettings), uintptr(unsafe.Pointer(&dm[0])))
	if r == 0 {
		return 1920, 1080, 60
	}
	w = int(binary.LittleEndian.Uint32(dm[172:]))
	h = int(binary.LittleEndian.Uint32(dm[176:]))
	hz = int(binary.LittleEndian.Uint32(dm[184:]))
	if w < 640 || h < 360 {
		return 1920, 1080, 60
	}
	if hz < 24 {
		hz = 60
	}
	return w, h, hz
}

// openTailscaleApp starts the Tailscale tray app so the user can log in from there.
func openTailscaleApp() error {
	p := findExe([]string{`Tailscale\tailscale-ipn.exe`}, "")
	if p == "" {
		return errors.New("Tailscale uygulaması bulunamadı")
	}
	return exec.Command(p).Start()
}

// isElevatedProcess reports whether this process runs with admin rights.
func isElevatedProcess() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}
