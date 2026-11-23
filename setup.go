package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

type component struct {
	Label     string
	WingetID  string
	ManualURL string
	Find      func() string
}

var components = map[string]component{
	"sunshine":  {"Sunshine", "LizardByte.Sunshine", "https://github.com/LizardByte/Sunshine/releases/latest", sunshineExe},
	"tailscale": {"Tailscale", "Tailscale.Tailscale", "https://tailscale.com/download/windows", tailscaleExe},
	"moonlight": {"Moonlight", "MoonlightGameStreamingProject.Moonlight", "https://moonlight-stream.org/", moonlightExe},
	"vigembus":  {"ViGEmBus", "ViGEm.ViGEmBus", "https://github.com/nefarius/ViGEmBus/releases/latest", vigemPath},
}

func (c component) label() string {
	if c.WingetID == "ViGEm.ViGEmBus" {
		return L("ViGEmBus (kol sürücüsü)", "ViGEmBus (controller driver)")
	}
	return c.Label
}

func isProgressLine(s string) bool {
	if s == "" || s == "-" || s == "\\" || s == "|" || s == "/" {
		return true
	}
	return strings.ContainsAny(s, "█▒") || strings.HasSuffix(s, "%") && len(s) < 8
}

// wingetInstall installs a component silently with winget. logl receives the
// meaningful output lines (progress bars are dropped).
func wingetInstall(key string, logl func(string, ...any)) error {
	comp := components[key]
	if comp.Find() != "" {
		return nil
	}
	winget := wingetPath()
	if winget == "" {
		return fmt.Errorf(L("winget bulunamadı; %s elle kurulmalı: %s (Microsoft Store'dan \"Uygulama Yükleyici\" güncellenirse winget de gelir)",
			"winget not found; install %s by hand: %s (updating \"App Installer\" from the Microsoft Store brings winget too)"), comp.label(), comp.ManualURL)
	}
	logl(L("%s indiriliyor ve kuruluyor...", "Downloading and installing %s..."), comp.label())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, winget, "install", "--id", comp.WingetID, "-e", "--silent",
		"--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity")
	hideCmd(c)
	pr, pw := io.Pipe()
	c.Stdout, c.Stderr = pw, pw
	if err := c.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait(); pw.Close() }()
	sc := bufio.NewScanner(pr)
	sc.Split(scanLinesCR)
	var tail []string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !isProgressLine(line) {
			tail = append(tail, line)
			if len(tail) > 6 {
				tail = tail[1:]
			}
		}
	}
	werr := <-done
	// Give installers a moment to finish writing files.
	for i := 0; i < 15 && comp.Find() == ""; i++ {
		time.Sleep(time.Second)
	}
	if comp.Find() == "" {
		logf("winget %s: %v | %s", comp.WingetID, werr, strings.Join(tail, " | "))
		if werr != nil {
			return fmt.Errorf(L("%s kurulamadı (%v). Elle kurmak için: %s", "could not install %s (%v). To install it by hand: %s"), comp.label(), werr, comp.ManualURL)
		}
		return fmt.Errorf(L("%s kuruldu görünüyor ama bulunamadı; bilgisayarı yeniden başlatmayı dene", "%s seems installed but cannot be found; try restarting the PC"), comp.label())
	}
	logl(L("%s kuruldu.", "%s installed."), comp.label())
	return nil
}

// scanLinesCR splits on \n and \r so winget's progress redraws become separate lines.
func scanLinesCR(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// ---- elevated helper -----------------------------------------------------

// elevReq is everything that needs admin rights, done in one go so the user
// sees a single UAC prompt.
type elevReq struct {
	Action   string   `json:"action"` // "setup"
	Install  []string `json:"install,omitempty"`
	User     string   `json:"user,omitempty"`    // Sunshine credentials to set (empty = keep)
	SunSvc   bool     `json:"sunSvc,omitempty"`  // one-time setup of Sunshine's service
	SID      string   `json:"sid,omitempty"`     // the user who may start and stop it
	TSUnatt  bool     `json:"tsUnatt,omitempty"` // host: keep Tailscale running without its tray app
	Pass     string   `json:"pass,omitempty"`
	Firewall bool     `json:"firewall,omitempty"`
	Legacy   bool     `json:"legacy,omitempty"`
	Lang     string   `json:"lang,omitempty"` // the elevated copy reports in the app's language
}

func (r elevReq) empty() bool {
	return len(r.Install) == 0 && r.User == "" && !r.Firewall && !r.Legacy && !r.SunSvc
}

type elevResp struct {
	OK  bool     `json:"ok"`
	Log []string `json:"log"`
	Err string   `json:"error,omitempty"`
}

// elevate writes the request to a private temp file, runs it with admin rights
// and streams its progress into the job.
func elevate(j *Job, req elevReq) error {
	dir, err := os.MkdirTemp("", "krp-elev-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	reqPath := filepath.Join(dir, "req.json")
	req.Lang = lang()
	b, _ := json.Marshal(req)
	if err := os.WriteFile(reqPath, b, 0o600); err != nil {
		return err
	}
	j.Logf("%s", L("Windows bir kez izin soracak: Evet'e bas.", "Windows will ask for permission once: press Yes."))
	done := make(chan error, 1)
	go func() { done <- runElevated([]string{"--elevated", reqPath}) }()
	shown := 0
	tail := func() {
		lb, _ := os.ReadFile(reqPath + ".log")
		lines := strings.Split(strings.TrimRight(string(lb), "\n"), "\n")
		for ; shown < len(lines); shown++ {
			if l := strings.TrimSpace(lines[shown]); l != "" {
				j.Logf("%s", l)
			}
		}
	}
	var runErr error
	for waiting := true; waiting; {
		select {
		case runErr = <-done:
			waiting = false
		case <-time.After(400 * time.Millisecond):
		}
		tail()
	}
	var resp elevResp
	if rb, err := os.ReadFile(reqPath + ".out"); err == nil {
		_ = json.Unmarshal(rb, &resp)
	}
	if runErr != nil {
		if resp.Err != "" {
			return errors.New(resp.Err)
		}
		return runErr
	}
	if !resp.OK {
		if resp.Err == "" {
			resp.Err = L("yönetici işlemi sonuç vermedi", "the admin step gave no result")
		}
		return errors.New(resp.Err)
	}
	return nil
}

// elevatedMain runs inside the elevated copy of the program.
func elevatedMain(reqPath string) int {
	var resp elevResp
	logl := func(f string, a ...any) {
		l := fmt.Sprintf(f, a...)
		resp.Log = append(resp.Log, l)
		if lf, err := os.OpenFile(reqPath+".log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			_, _ = lf.WriteString(strings.ReplaceAll(l, "\n", " ") + "\n")
			lf.Close()
		}
	}
	write := func() {
		b, _ := json.Marshal(resp)
		_ = os.WriteFile(reqPath+".out", b, 0o600)
	}
	defer write()
	b, err := os.ReadFile(reqPath)
	if err != nil {
		resp.Err = err.Error()
		return 1
	}
	_ = os.Remove(reqPath) // contains a password; delete as soon as read
	var req elevReq
	if err := json.Unmarshal(b, &req); err != nil || req.Action != "setup" {
		resp.Err = "invalid request"
		return 1
	}
	setLang(req.Lang)
	if err := runSetup(req, logl); err != nil {
		resp.Err = err.Error()
		return 1
	}
	resp.OK = true
	return 0
}

// runSetup does the admin-only part of the setup: installs, Sunshine login and
// controller settings, firewall rule, cleanup of the old player-order task.
func runSetup(req elevReq, logl func(string, ...any)) error {
	run := func(timeout time.Duration, name string, args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		c := exec.CommandContext(ctx, name, args...)
		hideCmd(c)
		out, err := c.CombinedOutput()
		if err != nil {
			logf("%s %v: %v %s", name, args, err, strings.TrimSpace(string(out)))
		}
		return err
	}
	var failed []string
	for _, key := range req.Install {
		if _, ok := components[key]; !ok {
			continue
		}
		if err := wingetInstall(key, logl); err != nil {
			logl("%v", err)
			failed = append(failed, components[key].label())
		}
	}
	restartSun := false
	if req.User != "" && req.Pass != "" {
		exe := sunshineExe()
		if exe == "" {
			return errors.New(L("Sunshine kurulamadığı için ayarlanamadı", "Sunshine could not be set up because it is not installed"))
		}
		logl("%s", L("Sunshine ayarlanıyor...", "Setting up Sunshine..."))
		if err := run(30*time.Second, exe, "--creds", req.User, req.Pass); err != nil {
			return fmt.Errorf(L("Sunshine ayarlanamadı: %v", "could not set up Sunshine: %v"), err)
		}
		restartSun = true
	}
	if conf := sunshineConfFile(); conf != "" {
		if changed, err := tuneSunshineConf(conf); err != nil {
			logf(L("Sunshine ayar dosyası güncellenemedi: %v", "Could not update Sunshine's config file: %v"), err)
		} else if changed {
			restartSun = true
		}
	}
	if req.SunSvc || slices.Contains(req.Install, "sunshine") || req.User != "" {
		// Sunshine runs only while the host screen is open: it does not start with
		// Windows, and the app may start and stop it without asking every time.
		if err := run(30*time.Second, "sc", "config", "SunshineService", "start=", "demand"); err != nil {
			logl(L("Sunshine ayarlanamadı: %v", "Could not set up Sunshine: %v"), err)
		}
		if err := allowServiceControl(req.SID); err != nil {
			logf(L("Sunshine servis izni verilemedi: %v", "Could not grant the Sunshine service permission: %v"), err)
			logl("%s", L("Sunshine'ı kapatma izni verilemedi; Başa dön'de açık kalabilir.", "Could not get permission to stop Sunshine; it may stay on after Back to start."))
		}
		restartSun = true
	}
	if restartSun {
		logl("%s", L("Sunshine başlatılıyor...", "Starting Sunshine..."))
		_ = run(60*time.Second, "net", "stop", "SunshineService")
		if err := run(60*time.Second, "net", "start", "SunshineService"); err != nil {
			logl("%s", L("Sunshine servisi başlatılamadı; bilgisayarı yeniden başlatmak gerekebilir.", "Could not start the Sunshine service; a restart of the PC may be needed."))
		}
	}
	if req.TSUnatt && tailscaleExe() != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		if _, err := runTS(ctx, "set", "--unattended=true"); err != nil {
			logf("tailscale set --unattended: %v", err)
		}
		cancel()
	}
	if req.Firewall {
		if err := addFirewallRule(run); err != nil {
			logl(L("Güvenlik duvarı izni eklenemedi: %v", "Could not add the firewall rule: %v"), err)
		}
	}
	if req.Legacy {
		legacyCleanup(logl)
	}
	if len(failed) > 0 {
		return fmt.Errorf(L("kurulamadı: %s", "could not install: %s"), strings.Join(failed, ", "))
	}
	return nil
}

// allowServiceControl lets this user start and stop Sunshine's service.
func allowServiceControl(sid string) error {
	if !isWindows || sid == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	show := exec.CommandContext(ctx, "sc", "sdshow", "SunshineService")
	hideCmd(show)
	out, err := show.Output()
	if err != nil {
		return err
	}
	sddl := strings.TrimSpace(string(out))
	updated := addServiceACE(sddl, sid)
	if updated == sddl {
		return nil
	}
	set := exec.CommandContext(ctx, "sc", "sdset", "SunshineService", updated)
	hideCmd(set)
	if b, err := set.CombinedOutput(); err != nil {
		return fmt.Errorf("%v %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

const (
	fwRuleName = "KeRemotePlay-Agent"
	fwSunRule  = "KeRemotePlay-Sunshine"
	tsRanges   = "100.64.0.0/10,fd7a:115c:a1e0::/48"
)

// addFirewallRule opens, for Tailscale addresses only, the pairing agent's port
// and Sunshine's ports. Sunshine's own rule exists only if its installer's
// firewall option was picked, and an "allow" given at Windows' prompt usually
// covers private networks only, which the Tailscale adapter may not be.
func addFirewallRule(run func(time.Duration, string, ...string) error) error {
	if !isWindows {
		return nil
	}
	base := sunshineUIPort() - 1
	for _, n := range []string{fwRuleName, fwSunRule} {
		_ = run(30*time.Second, "netsh", "advfirewall", "firewall", "delete", "rule", "name="+n)
	}
	add := func(name, proto, ports string) error {
		return run(30*time.Second, "netsh", "advfirewall", "firewall", "add", "rule", "name="+name,
			"dir=in", "action=allow", "protocol="+proto, "localport="+ports, "remoteip="+tsRanges, "profile=any")
	}
	if err := add(fwRuleName, "TCP", fmt.Sprint(AgentPort)); err != nil {
		return err
	}
	if err := add(fwSunRule, "TCP", fmt.Sprintf("%d,%d,%d,%d", base-5, base, base+1, base+21)); err != nil {
		return err
	}
	return add(fwSunRule, "UDP", fmt.Sprintf("%d-%d", base+9, base+21))
}

func firewallRuleExists() bool {
	if !isWindows {
		return true
	}
	for _, n := range []string{fwRuleName, fwSunRule} {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		c := exec.CommandContext(ctx, "netsh", "advfirewall", "firewall", "show", "rule", "name="+n)
		hideCmd(c)
		err := c.Run()
		cancel()
		if err != nil {
			return false
		}
	}
	return true
}

// defaultGamepadConf: friends' controllers go through ViGEmBus and always appear
// as Xbox 360 pads (PlayStation pads included), which every game understands.
var defaultGamepadConf = []confPair{{"gamepad_driver", "vigembus"}, {"gamepad", "x360"}}

type confPair struct{ Key, Value string }

// tuneSunshineConf adds the default gamepad settings that are not set yet; it never
// overrides a choice the user already made.
func tuneSunshineConf(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	var add []confPair
	for _, p := range defaultGamepadConf {
		if confValue(string(b), p.Key) == "" {
			add = append(add, p)
		}
	}
	if len(add) == 0 {
		return false, nil
	}
	text := strings.TrimRight(string(b), "\r\n")
	for _, p := range add {
		if text != "" {
			text += "\r\n"
		}
		text += p.Key + " = " + p.Value
	}
	return true, os.WriteFile(path, []byte(text+"\r\n"), 0o644)
}

// parseConf reads Sunshine's "key = value" file. ok is false if it contains lines
// we do not understand (then it must not be rewritten through the API).
func parseConf(text string) (pairs []confPair, ok bool) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !found || k == "" || strings.ContainsAny(k, " \t[]{}") {
			return nil, false
		}
		pairs = append(pairs, confPair{k, strings.TrimSpace(v)})
	}
	return pairs, true
}

// setConfPairs returns the config with the given keys set (added or replaced);
// an empty value removes the key.
func setConfPairs(pairs []confPair, set []confPair) []confPair {
	out := append([]confPair(nil), pairs...)
	for _, s := range set {
		found := false
		for i := 0; i < len(out); i++ {
			if out[i].Key == s.Key {
				if s.Value == "" {
					out = append(out[:i], out[i+1:]...)
					i--
				} else {
					out[i].Value = s.Value
				}
				found = true
			}
		}
		if !found && s.Value != "" {
			out = append(out, s)
		}
	}
	return out
}

func confValue(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// setConfFile edits Sunshine's config file line by line (elevated fallback that keeps
// every other line untouched).
func setConfFile(path string, set []confPair) error {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var out []string
	done := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		k, _, found := strings.Cut(t, "=")
		k = strings.TrimSpace(k)
		replaced := false
		if found && !strings.HasPrefix(t, "#") {
			for _, s := range set {
				if s.Key == k {
					if s.Value != "" && !done[k] {
						out = append(out, k+" = "+s.Value)
					}
					done[k], replaced = true, true
				}
			}
		}
		if !replaced {
			out = append(out, line)
		}
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	for _, s := range set {
		if !done[s.Key] && s.Value != "" {
			out = append(out, s.Key+" = "+s.Value)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\r\n")+"\r\n"), 0o644)
}

var psErrRe = regexp.MustCompile(`(?s)<S S="Error">(.*?)</S>`)

// cleanPSErr turns PowerShell's CLIXML error stream into plain text.
func cleanPSErr(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "#< CLIXML") {
		return s
	}
	var b strings.Builder
	for _, m := range psErrRe.FindAllStringSubmatch(s, -1) {
		b.WriteString(html.UnescapeString(strings.ReplaceAll(m[1], "_x000D__x000A_", "\n")))
	}
	var lines []string
	for _, l := range strings.Split(b.String(), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "+ ~") && !strings.HasPrefix(l, "At line:") {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, " ")
}
