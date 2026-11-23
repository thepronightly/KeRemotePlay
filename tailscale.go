package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

type TSPeer struct {
	HostName string `json:"hostName"`
	IP       string `json:"ip"`
	Online   bool   `json:"online"`
	CurAddr  string `json:"curAddr"`
	Relay    string `json:"relay"`
}

type TSStatus struct {
	Installed    bool     `json:"installed"`
	ServiceUp    bool     `json:"serviceUp"`
	BackendState string   `json:"backendState"`
	SelfIP       string   `json:"selfIP"`
	SelfName     string   `json:"selfName"`
	Tailnet      string   `json:"tailnet"`
	Error        string   `json:"error,omitempty"`
	AuthURL      string   `json:"-"`
	Peers        []TSPeer `json:"-"`
}

func (s TSStatus) Running() bool { return s.BackendState == "Running" && s.SelfIP != "" }

func (s TSStatus) Peer(ip string) (TSPeer, bool) {
	for _, p := range s.Peers {
		if p.IP == ip {
			return p, true
		}
	}
	return TSPeer{}, false
}

type tsRaw struct {
	BackendState string `json:"BackendState"`
	AuthURL      string `json:"AuthURL"`
	Self         *struct {
		HostName     string   `json:"HostName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Self"`
	Peer map[string]struct {
		HostName     string   `json:"HostName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
		Online       bool     `json:"Online"`
		CurAddr      string   `json:"CurAddr"`
		Relay        string   `json:"Relay"`
	} `json:"Peer"`
	CurrentTailnet *struct {
		Name string `json:"Name"`
	} `json:"CurrentTailnet"`
}

func firstIPv4(ips []string) string {
	for _, s := range ips {
		if a, err := netip.ParseAddr(s); err == nil && a.Is4() {
			return s
		}
	}
	if len(ips) > 0 {
		return ips[0]
	}
	return ""
}

func runTS(ctx context.Context, args ...string) ([]byte, error) {
	exe := tailscaleExe()
	if exe == "" {
		return nil, errors.New("Tailscale kurulu değil")
	}
	c := exec.CommandContext(ctx, exe, args...)
	hideCmd(c)
	return c.CombinedOutput()
}

func parseTSStatus(b []byte) (TSStatus, error) {
	var raw tsRaw
	i := strings.IndexByte(string(b), '{')
	if i < 0 {
		return TSStatus{}, errors.New(strings.TrimSpace(string(b)))
	}
	if err := json.Unmarshal(b[i:], &raw); err != nil {
		return TSStatus{}, err
	}
	s := TSStatus{Installed: true, ServiceUp: true, BackendState: raw.BackendState, AuthURL: raw.AuthURL}
	if raw.Self != nil {
		s.SelfName = raw.Self.HostName
		s.SelfIP = firstIPv4(raw.Self.TailscaleIPs)
	}
	if raw.CurrentTailnet != nil {
		s.Tailnet = raw.CurrentTailnet.Name
	}
	for _, p := range raw.Peer {
		s.Peers = append(s.Peers, TSPeer{HostName: p.HostName, IP: firstIPv4(p.TailscaleIPs), Online: p.Online, CurAddr: p.CurAddr, Relay: p.Relay})
	}
	return s, nil
}

var (
	tsCacheMu sync.Mutex
	tsCache   TSStatus
	tsCacheAt time.Time
)

func tsStatus(maxAge time.Duration) TSStatus {
	tsCacheMu.Lock()
	defer tsCacheMu.Unlock()
	if time.Since(tsCacheAt) < maxAge {
		return tsCache
	}
	prev := tsCache.BackendState
	tsCache = tsStatusFresh()
	tsCacheAt = time.Now()
	if tsCache.BackendState != prev {
		logf("Tailscale durumu: %s", stateText(tsCache))
	}
	return tsCache
}

func tsInvalidate() {
	tsCacheMu.Lock()
	tsCacheAt = time.Time{}
	tsCacheMu.Unlock()
}

func tsStatusFresh() TSStatus {
	if tailscaleExe() == "" {
		return TSStatus{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	out, _ := runTS(ctx, "status", "--json")
	s, err := parseTSStatus(out)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return TSStatus{Installed: true, Error: msg}
	}
	return s
}

// ---- ping ----------------------------------------------------------------

type PingResult struct {
	OK     bool   `json:"ok"`
	Ms     int    `json:"ms"`
	Direct bool   `json:"direct"`
	Via    string `json:"via"`
	Kind   string `json:"kind"` // direct | peer-relay | derp
	Error  string `json:"error,omitempty"`
	At     int64  `json:"at"`
}

var pongRe = regexp.MustCompile(`pong from \S+ \(([^)]*)\) via (\S+) in (\S+)`)

func parsePing(out string) PingResult {
	var r PingResult
	for _, line := range strings.Split(out, "\n") {
		m := pongRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		d, err := time.ParseDuration(strings.TrimSpace(m[3]))
		if err != nil {
			continue
		}
		r.OK = true
		r.Via = m[2]
		r.Ms = int(d.Round(time.Millisecond) / time.Millisecond)
		switch {
		case strings.HasPrefix(r.Via, "DERP("):
			r.Kind, r.Direct = "derp", false
		case strings.HasPrefix(r.Via, "peer-relay("):
			r.Kind, r.Direct = "peer-relay", false
		default:
			r.Kind, r.Direct = "direct", true
		}
	}
	if !r.OK {
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = "yanıt yok"
		}
		if len(msg) > 160 {
			msg = msg[len(msg)-160:]
		}
		r.Error = msg
	}
	r.At = time.Now().Unix()
	return r
}

func tsPing(ip string) PingResult {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := runTS(ctx, "ping", "--c", "8", "--timeout", "2s", ip)
	r := parsePing(string(out))
	if !r.OK && err != nil && r.Error == "yanıt yok" {
		r.Error = err.Error()
	}
	return r
}

// ---- login ---------------------------------------------------------------

var authURLRe = regexp.MustCompile(`https://login\.tailscale\.com/\S+`)

// tsInteractiveLogin brings the host onto Tailscale. It looks at the current state
// first, opens the login page however Tailscale offers it (CLI output or the
// AuthURL in the status) and only reports success once Tailscale is Running.
func tsInteractiveLogin(j *Job) error {
	exe := tailscaleExe()
	if exe == "" {
		return errors.New("Tailscale kurulu değil")
	}
	st := tsEnsureStarted(j)
	j.Logf("Tailscale durumu: %s", stateText(st))
	if st.Running() {
		return nil
	}
	if st.BackendState == "NeedsMachineAuth" {
		_ = openURL("https://login.tailscale.com/admin/machines")
		j.Logf("Giriş yapılmış ama bu cihazın Tailscale panelinde onaylanması gerekiyor. Açılan sayfada bu bilgisayarı onayla.")
		return waitRunning(j, 5*time.Minute)
	}

	opened := ""
	open := func(u string) {
		if u != "" && u != opened {
			opened = u
			j.Logf("Tarayıcıda Tailscale giriş sayfası açılıyor. Giriş yapınca burası kendiliğinden devam eder.")
			_ = openURL(u)
		}
	}
	// A login may already be waiting for the browser.
	open(st.AuthURL)

	// "tailscale up" brings a logged-in but stopped client back up, and starts the
	// browser login otherwise. (Plain "login" would create an extra empty profile.)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	args := append([]string{"up", "--timeout=300s"}, tailscaleLoginExtraArgs()...)
	c := exec.CommandContext(ctx, exe, args...)
	hideCmd(c)
	pr, pw := io.Pipe()
	c.Stdout, c.Stderr = pw, pw
	if err := c.Start(); err != nil {
		return fmt.Errorf("tailscale başlatılamadı: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait(); pw.Close() }()
	lines := make(chan string, 32)
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			lines <- strings.TrimSpace(sc.Text())
		}
		close(lines)
	}()

	tick := time.NewTicker(1500 * time.Millisecond)
	defer tick.Stop()
	var cmdErr error
	cmdDone := false
	var outTail []string
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case l, ok := <-lines:
			if !ok {
				lines = nil
				continue
			}
			if l == "" {
				continue
			}
			j.Logf("tailscale: %s", l)
			outTail = append(outTail, l)
			open(authURLRe.FindString(l))
		case err := <-done:
			cmdDone, cmdErr = true, err
			done = nil
		case <-tick.C:
			s2 := tsStatusFresh()
			if s2.Running() {
				tsInvalidate()
				j.Logf("Tailscale bağlandı: %s", s2.SelfIP)
				return nil
			}
			open(s2.AuthURL)
			if cmdDone && opened == "" {
				// The command ended and no login page ever showed up: report why.
				msg := strings.Join(outTail, " | ")
				if cmdErr != nil {
					msg = strings.TrimSpace(cmdErr.Error() + " " + msg)
				}
				if msg == "" {
					msg = "tailscale komutu çıktı vermeden bitti"
				}
				logf("Tailscale girişi başarısız: durum=%s çıktı=%s", stateText(s2), msg)
				_ = openTailscaleApp()
				return fmt.Errorf("Tailscale girişi başlatılamadı (%s; %s). Tailscale uygulaması açıldı; saatin yanındaki Tailscale simgesinden \"Log in\" ile giriş yap, bu ekran kendiliğinden güncellenir", stateText(s2), msg)
			}
		}
	}
	return errors.New("Tailscale girişi 5 dakika içinde tamamlanmadı")
}

// tsStarting: the service is up but has not loaded its state yet. On Windows this
// also happens after a reboot until the Tailscale tray app (which normally starts
// at sign-in) connects; without it the backend stays in NoState.
func tsStarting(s TSStatus) bool {
	return s.Installed && (s.BackendState == "" || s.BackendState == "NoState" || s.BackendState == "Starting")
}

// tsEnsureStarted opens the Tailscale app if needed and waits until Tailscale
// has settled (Running, NeedsLogin, Stopped...).
func tsEnsureStarted(j *Job) TSStatus {
	st := tsStatusFresh()
	if !tsStarting(st) {
		return st
	}
	j.Logf("Tailscale başlatılıyor...")
	_ = openTailscaleApp()
	waitFor(func() bool { st = tsStatusFresh(); return !tsStarting(st) }, 40*time.Second)
	tsInvalidate()
	return st
}

func waitRunning(j *Job, max time.Duration) error {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if st := tsStatusFresh(); st.Running() {
			tsInvalidate()
			j.Logf("Tailscale bağlandı: %s", st.SelfIP)
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return errors.New("Tailscale bağlanmadı")
}

func stateText(s TSStatus) string {
	switch {
	case !s.Installed:
		return "kurulu değil"
	case s.Error != "" && s.BackendState == "":
		return "servise ulaşılamadı (" + s.Error + ")"
	case s.BackendState == "":
		return "bilinmiyor"
	}
	return s.BackendState
}

// ---- profiles ------------------------------------------------------------

// A Tailscale profile is one logged-in tailnet; Tailscale keeps several and only
// one is active. Joining a host adds one, so a friend who also uses Tailscale for
// something else ends up switching between them.
type tsProfile struct {
	ID      string
	Tailnet string
	Current bool
}

// parseTSProfiles reads "tailscale switch --list":
//
//	ID    Tailnet              Account
//	9747  me@gmail.com         me@gmail.com
//	6458  friend@gmail.com     KeRemotePlay*
func parseTSProfiles(out string) []tsProfile {
	var ps []tsProfile
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		f := strings.Fields(line)
		if len(f) < 2 || f[0] == "ID" {
			continue
		}
		ps = append(ps, tsProfile{ID: f[0], Tailnet: strings.TrimSuffix(f[1], "*"), Current: strings.HasSuffix(line, "*")})
	}
	return ps
}

func tsProfiles() []tsProfile {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := runTS(ctx, "switch", "--list")
	if err != nil {
		return nil
	}
	return parseTSProfiles(string(out))
}

func tsCurrentProfile() string {
	for _, p := range tsProfiles() {
		if p.Current {
			return p.ID
		}
	}
	return ""
}

// tsSwitch changes the active profile. The command's result says little: it
// fails with "Tailscale is stopped" for a profile that was switched off, although
// it did switch, and without the Tailscale tray app it hangs on a profile not in
// unattended mode. So it gets a short time and the outcome is checked.
func tsSwitch(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	out, err := runTS(ctx, "switch", id)
	tsInvalidate()
	if cur := tsCurrentProfile(); cur != id {
		return fmt.Errorf("%v %s", err, strings.TrimSpace(string(out)))
	}
	if tsStarting(tsStatusFresh()) {
		_ = openTailscaleApp()
		waitFor(func() bool { return !tsStarting(tsStatusFresh()) }, 20*time.Second)
	}
	return nil
}

// tsUp reconnects a Tailscale that was switched off. It has to be a plain "up":
// with any flag it refuses to run on a device with non-default settings.
func tsUp() {
	if tsStatusFresh().BackendState != "Stopped" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := runTS(ctx, "up"); err != nil {
		logf("tailscale up: %v %s", err, strings.TrimSpace(string(out)))
	}
	tsInvalidate()
}

func tsDown() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if out, err := runTS(ctx, "down"); err != nil {
		logf("tailscale down: %v %s", err, strings.TrimSpace(string(out)))
	}
	tsInvalidate()
}

var (
	tsSavedMu sync.Mutex
	tsSaved   *string // the profile that was active before the app touched Tailscale
)

// tsRemember notes how Tailscale was found, once per host or client visit.
func tsRemember() {
	tsSavedMu.Lock()
	defer tsSavedMu.Unlock()
	if tsSaved == nil {
		id := tsCurrentProfile()
		tsSaved = &id
	}
}

// tsRestore takes back what the app did to Tailscale: it returns to the network
// that was active before, or disconnects when that was the app's own network.
func tsRestore(j *Job) {
	tsSavedMu.Lock()
	saved := tsSaved
	tsSaved = nil
	tsSavedMu.Unlock()
	if saved == nil || tailscaleExe() == "" {
		return
	}
	if *saved != "" && *saved != tsCurrentProfile() {
		j.Logf("Tailscale önceki ağına döndürülüyor...")
		if err := tsSwitch(*saved); err != nil {
			logf("tailscale switch %s: %v", *saved, err)
		}
		return
	}
	j.Logf("Tailscale kapatılıyor...")
	tsDown()
}

// tsWaitPeer waits until Tailscale runs and ip is one of its peers.
func tsWaitPeer(ip string, max time.Duration) (TSStatus, bool) {
	var st TSStatus
	ok := waitFor(func() bool {
		st = tsStatusFresh()
		_, found := st.Peer(ip)
		return st.Running() && found
	}, max)
	tsInvalidate()
	return st, ok
}

// tsSwitchToPeer looks for an already logged-in profile that can see ip (the
// preferred tailnet first) and switches to it. When none can, the profile that
// was active before is restored.
func tsSwitchToPeer(j *Job, ip, prefer string) (TSStatus, bool) {
	var orig string
	var order []tsProfile
	for _, p := range tsProfiles() {
		if p.Current {
			orig = p.ID
		}
		if prefer == "" || p.Tailnet == prefer {
			order = append(order, p)
		}
	}
	if len(order) == 0 {
		return TSStatus{}, false
	}
	j.Logf("Tailscale host'un ağına bağlanıyor...")
	for _, p := range order {
		if !p.Current {
			if err := tsSwitch(p.ID); err != nil {
				logf("tailscale switch %s: %v", p.ID, err)
				continue
			}
		}
		tsUp()
		if st, ok := tsWaitPeer(ip, 12*time.Second); ok {
			logf("Tailscale profili: %s", p.Tailnet)
			return st, true
		}
	}
	if orig != "" && orig != tsCurrentProfile() {
		_ = tsSwitch(orig)
	}
	return TSStatus{}, false
}

// tsReach puts this PC on the network where ip is: as it is, by switching to a
// saved profile, or by logging in with key (adds a profile).
func tsReach(j *Job, ip, tailnet, key string) (TSStatus, error) {
	st := tsEnsureStarted(j)
	tsRemember()
	if _, ok := st.Peer(ip); ok && st.Running() {
		return st, nil
	}
	if st2, ok := tsSwitchToPeer(j, ip, tailnet); ok {
		return st2, nil
	}
	if key == "" {
		return st, errors.New("host'un ağına bağlı değilsin; host'tan yeni davet kodu iste")
	}
	if st = tsStatusFresh(); st.Running() {
		j.Logf("Tailscale başka bir ağa bağlı; KeRemotePlay ağı ayrı bir profil olarak ekleniyor.")
	}
	j.Logf("Host'un ağına katılınıyor...")
	if err := tsJoinWithKey(j, key); err != nil {
		return st, fmt.Errorf("%v. Anahtarın süresi dolmuş olabilir; host'tan yeni davet kodu iste", err)
	}
	if st, ok := tsWaitPeer(ip, 30*time.Second); ok {
		return st, nil
	}
	return st, errors.New("ağa katılındı ama host görünmüyor. Davet kodu güncel mi? Host'ta Tailscale açık mı?")
}

// tsJoinWithKey logs this device into the host's tailnet with a pre-auth key.
func tsJoinWithKey(j *Job, key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	args := []string{"login", "--auth-key=" + key, "--nickname=KeRemotePlay", "--timeout=90s"}
	args = append(args, tailscaleLoginExtraArgs()...)
	out, err := runTS(ctx, args...)
	if s := strings.TrimSpace(string(out)); s != "" {
		j.Logf("tailscale: %s", s)
	}
	tsInvalidate()
	if err != nil {
		return fmt.Errorf("Tailscale ağına katılınamadı: %v", err)
	}
	return nil
}
