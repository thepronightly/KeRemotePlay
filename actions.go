package main

import (
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---- host ----------------------------------------------------------------

type HostView struct {
	Ready        bool       `json:"ready"`
	Phase        string     `json:"phase"`
	SunInstalled bool       `json:"sunInstalled"`
	SunRunning   bool       `json:"sunRunning"`
	SunCreds     string     `json:"sunCreds"` // ok | bad | unset | down | error
	SunUser      string     `json:"sunUser"`
	SunPass      string     `json:"sunPass"`
	SunPanel     string     `json:"sunPanel"`
	TS           TSStatus   `json:"ts"`
	AuthKeySet   bool       `json:"authKeySet"`
	Firewall     bool       `json:"firewall"`
	Invite       string     `json:"invite"`
	Agent        AgentState `json:"agent"`
	Net          NetInfo    `json:"net"`
	Audio        AudioInfo  `json:"audio"`
	DiscordOK    bool       `json:"discordOK"`
	RelayOn      bool       `json:"relayOn"`
	RelayTarget  string     `json:"relayTarget"`
	Relay        RelayState `json:"relay"`
	ViGEm        bool       `json:"vigem"`
	Legacy       bool       `json:"legacy"`
	SunService   SunService `json:"sunService"`
	Request      *ConnReq   `json:"request,omitempty"` // a friend waiting for an answer
	Clients      []string   `json:"clients"`           // approved friends
	Session      bool       `json:"session"`           // a stream is running
	HotkeyErr    string     `json:"hotkeyErr,omitempty"`
}

var (
	credCacheMu  sync.Mutex
	credCacheKey string
	credCacheVal string
	credCacheAt  time.Time
	fwCacheVal   bool
	fwCacheAt    time.Time
)

func cachedCreds(user, pass string) string {
	credCacheMu.Lock()
	defer credCacheMu.Unlock()
	key := user + "\x00" + pass
	if key == credCacheKey && time.Since(credCacheAt) < 8*time.Second {
		return credCacheVal
	}
	credCacheKey, credCacheVal, credCacheAt = key, sunCheckCreds(user, pass), time.Now()
	return credCacheVal
}

func cachedFirewall() bool {
	credCacheMu.Lock()
	defer credCacheMu.Unlock()
	if time.Since(fwCacheAt) > 20*time.Second {
		fwCacheVal, fwCacheAt = firewallRuleExists(), time.Now()
	}
	return fwCacheVal
}

func invalidateHostCaches() {
	credCacheMu.Lock()
	credCacheAt, fwCacheAt = time.Time{}, time.Time{}
	credCacheMu.Unlock()
	tsInvalidate()
}

func hostView() HostView {
	c := getConfig()
	v := HostView{
		Phase:        getHostPhase(),
		SunInstalled: sunshineExe() != "",
		SunUser:      c.Host.SunUser,
		SunPass:      c.Host.SunPass,
		SunPanel:     fmt.Sprintf("https://localhost:%d", sunshineUIPort()),
		TS:           tsStatus(2 * time.Second),
		AuthKeySet:   c.Host.AuthKey != "",
		Agent:        getAgent(),
		Net:          netInfoCached(),
		Audio:        audioInfoCached(),
		DiscordOK:    c.Host.DiscordOK,
		RelayOn:      !c.Host.RelayOff,
		RelayTarget:  c.Host.RelayTarget,
		Relay:        getRelay(),
		ViGEm:        vigemInstalled(),
		Legacy:       cachedLegacy(),
	}
	v.Request, v.Clients = connView()
	v.Session, v.HotkeyErr = sessionActive(), getHotkeyErr()
	v.SunRunning = sunshineRunning()
	v.SunService = sunServiceState()
	if v.SunRunning {
		v.SunCreds = cachedCreds(c.Host.SunUser, c.Host.SunPass)
	} else {
		v.SunCreds = "down"
	}
	v.Firewall = cachedFirewall()
	v.Ready = hostReady(v) && (c.Host.Tailnet == "" || v.TS.Tailnet == "" || v.TS.Tailnet == c.Host.Tailnet)
	if v.Ready {
		if c.Host.Tailnet == "" && v.TS.Tailnet != "" {
			withConfig(func(cf *Config) { cf.Host.Tailnet = v.TS.Tailnet }) // set up by an older version
		}
		name, _ := os.Hostname()
		v.Invite = encodeInvite(Invite{Key: c.Host.AuthKey, IP: v.TS.SelfIP, Name: name, Secret: c.Host.Secret, Port: AgentPort, Tailnet: v.TS.Tailnet})
	}
	return v
}

var (
	legacyMu  sync.Mutex
	legacyVal bool
	legacyAt  time.Time
)

func cachedLegacy() bool {
	legacyMu.Lock()
	defer legacyMu.Unlock()
	if time.Since(legacyAt) > 30*time.Second {
		legacyVal, legacyAt = legacyNeedsCleanup(), time.Now()
	}
	return legacyVal
}

func invalidateLegacy() { legacyMu.Lock(); legacyAt = time.Time{}; legacyMu.Unlock() }

func hostSetAuthKey(key string) error {
	key = strings.TrimSpace(key)
	if !strings.HasPrefix(key, "tskey-") {
		return errors.New(L("anahtar 'tskey-' ile başlamalı (Tailscale panelindeki Auth key)", "the key must start with 'tskey-' (the Auth key from the Tailscale admin page)"))
	}
	if strings.HasPrefix(key, "tskey-api-") || strings.HasPrefix(key, "tskey-client-") {
		return errors.New(L("bu bir API anahtarı. 'Generate auth key' ile üretilen tskey-auth-... anahtarı lazım", "this is an API key. A tskey-auth-... key made with 'Generate auth key' is needed"))
	}
	withConfig(func(c *Config) { c.Host.AuthKey = key })
	return nil
}

// ---- client --------------------------------------------------------------

type HostInfo struct {
	Name     string       `json:"name"`
	Version  string       `json:"version"`
	Sunshine bool         `json:"sunshine"`
	Net      NetInfo      `json:"net"`
	Display  *DisplayInfo `json:"display,omitempty"`
	SunPort  int          `json:"sunPort,omitempty"`
}

type Health struct {
	Ping     *PingResult `json:"ping,omitempty"`
	HostInfo *HostInfo   `json:"hostInfo,omitempty"`
	AgentErr string      `json:"agentErr,omitempty"`
	SunReach *bool       `json:"sunReach,omitempty"` // Sunshine's ports answer over Tailscale
	At       int64       `json:"at"`
	Busy     bool        `json:"busy"`
}

type ClientView struct {
	MLInstalled bool           `json:"mlInstalled"`
	TS          TSStatus       `json:"ts"`
	Joined      bool           `json:"joined"`
	HostIP      string         `json:"hostIP"`
	HostName    string         `json:"hostName"`
	HostOnline  bool           `json:"hostOnline"`
	OnTailnet   bool           `json:"onTailnet"`
	Paired      bool           `json:"paired"`
	Apps        []string       `json:"apps"`
	Stream      StreamSettings `json:"stream"`
	ResOptions  []string       `json:"resOptions"`
	FPSOptions  []int          `json:"fpsOptions"`
	Speed       *SpeedResult   `json:"speed,omitempty"`
	Plan        *Plan          `json:"plan,omitempty"`
	Display     string         `json:"display"`
	HostDisplay string         `json:"hostDisplay,omitempty"`
	Health      Health         `json:"health"`
	Pads        PadsView       `json:"pads"`
	Net         NetInfo        `json:"net"`
	Notice      string         `json:"notice,omitempty"`
}

var (
	healthMu   sync.Mutex
	health     Health
	healthKick = make(chan struct{}, 1)
)

func clientView() ClientView {
	c := getConfig()
	v := ClientView{
		MLInstalled: moonlightExe() != "",
		TS:          tsStatus(2 * time.Second),
		Joined:      c.Client.HostIP != "",
		HostIP:      c.Client.HostIP,
		HostName:    c.Client.HostName,
		Paired:      c.Client.Paired,
		Apps:        c.Client.Apps,
		Stream:      c.Client.Stream,
		ResOptions:  ResOptions,
		FPSOptions:  FPSOptions,
		Net:         netInfoCached(),
		Notice:      c.Client.Notice,
	}
	if p, ok := v.TS.Peer(c.Client.HostIP); ok {
		v.OnTailnet, v.HostOnline = true, p.Online
	}
	v.Speed = c.Client.Speed
	cd := clientDisplay()
	ed := effectiveDisplay(cd, c.Client.HostDisp)
	pl := makePlan(c.Client.Stream, c.Client.Speed, ed.W, ed.H, ed.Hz)
	v.Plan = &pl
	v.Display = fmt.Sprintf("%dx%d · %d Hz", cd.W, cd.H, cd.Hz)
	if hd := c.Client.HostDisp; hd != nil {
		v.HostDisplay = fmt.Sprintf("%dx%d · %d Hz", hd.W, hd.H, hd.Hz)
	}
	healthMu.Lock()
	v.Health = health
	healthMu.Unlock()
	v.Pads = getPadsView()
	return v
}

func kickHealth() {
	select {
	case healthKick <- struct{}{}:
	default:
	}
}

func healthLoop() {
	for {
		select {
		case <-healthKick:
		case <-time.After(20 * time.Second):
		}
		c := getConfig()
		if c.Mode != "client" || c.Client.HostIP == "" || tailscaleExe() == "" {
			continue
		}
		healthMu.Lock()
		health.Busy = true
		healthMu.Unlock()

		var info HostInfo
		agentErr := ""
		if err := agentCall(c.Client.HostIP, c.Client.AgentPort, c.Client.Secret, "GET", "/krp/info", nil, 4*time.Second, &info); err != nil {
			if errors.Is(err, errInviteGone) {
				clientLeave(hostLabel(c.Client) + L(" oturumu kapattı. Tekrar bağlanmak için yeni kod iste.", " ended the session. Ask for a new code to connect again."))
				continue
			}
			agentErr = err.Error()
		}
		ping := tsPing(c.Client.HostIP)
		var reach *bool
		if agentErr == "" && info.Sunshine {
			r := sunReachable(c.Client.HostIP, info.SunPort)
			reach = &r
		}

		if agentErr == "" && info.Display != nil && (c.Client.HostDisp == nil || *c.Client.HostDisp != *info.Display) {
			d := *info.Display
			withConfig(func(cf *Config) { cf.Client.HostDisp = &d })
		}
		healthMu.Lock()
		health.Ping, health.SunReach = &ping, reach
		if agentErr == "" {
			health.HostInfo, health.AgentErr = &info, ""
		} else {
			health.AgentErr = agentErr
		}
		health.At, health.Busy = time.Now().Unix(), false
		healthMu.Unlock()
	}
}

func clientJoin(j *Job, code string) error {
	inv, err := decodeInvite(code)
	if err != nil {
		return err
	}
	j.Logf(L("Davet kodu okundu: %s", "Invite code read: %s"), inv.Name)
	if err := clientInstall(j); err != nil {
		return err
	}

	if st := tsStatusFresh(); st.Running() && st.SelfIP == inv.IP {
		return errors.New(L("bu davet kodu bu bilgisayarın kendi kodu. Kodu arkadaşının bilgisayarında kullan", "this invite code belongs to this PC. Use it on your friend's PC"))
	}
	st, err := tsReach(j, inv.IP, inv.Tailnet, inv.Key)
	if err != nil {
		return err
	}
	j.Logf("%s", L("Host'un ağına bağlandın.", "You are on the host's network."))

	withConfig(func(c *Config) {
		c.Client.HostIP, c.Client.HostName, c.Client.Tailnet = inv.IP, inv.Name, st.Tailnet
		c.Client.Secret, c.Client.AgentPort, c.Client.AuthKey = inv.Secret, inv.Port, inv.Key
		c.Client.Paired, c.Client.Apps, c.Client.Notice = false, nil, ""
	})
	if err := clientPair(j); err != nil {
		// A refused or failed connection asks for a code again next time.
		if getConfig().Client.HostIP != "" {
			clientLeave("")
		}
		return err
	}
	return nil
}

// clientLeave forgets the host; the next connection needs a code again.
func clientLeave(notice string) {
	withConfig(func(c *Config) {
		cl := &c.Client
		cl.HostIP, cl.HostName, cl.Secret, cl.AuthKey, cl.Tailnet = "", "", "", "", ""
		cl.AgentPort, cl.Paired, cl.Apps, cl.HostDisp = 0, false, nil, nil
		cl.Notice = notice
	})
	healthMu.Lock()
	health = Health{}
	healthMu.Unlock()
}

// clientEnsureNet puts this PC back on the host's network if it left it (Tailscale
// not started yet, switched to another tailnet, logged out...).
func clientEnsureNet(j *Job) error {
	c := getConfig().Client
	if c.HostIP == "" {
		return errNoInvite
	}
	st, err := tsReach(j, c.HostIP, c.Tailnet, c.AuthKey)
	if err != nil {
		return err
	}
	if st.Tailnet != "" && st.Tailnet != c.Tailnet {
		withConfig(func(cf *Config) { cf.Client.Tailnet = st.Tailnet })
	}
	// Right after a profile switch peers show up as offline for a moment.
	online := func() bool { p, _ := tsStatusFresh().Peer(c.HostIP); return p.Online }
	if p, _ := st.Peer(c.HostIP); !p.Online && !waitFor(online, 10*time.Second) && !tcpOpen(c.HostIP, c.AgentPort) {
		return fmt.Errorf(L("%s şu an çevrim dışı. Arkadaşının bilgisayarı açık olmalı (KeRemotePlay de açık olsun)", "%s is offline. Your friend's PC must be on, with KeRemotePlay open"), hostLabel(c))
	}
	kickHealth()
	return nil
}

var errNoInvite = newErr("önce davet koduyla bağlan", "connect with an invite code first")

func hostLabel(c ClientConfig) string {
	if c.HostName != "" {
		return c.HostName
	}
	return "Host"
}

func clientPair(j *Job) error {
	c := getConfig().Client
	if c.HostIP == "" {
		return errNoInvite
	}
	j.Logf("%s", L("Host'taki KeRemotePlay'e ulaşılıyor...", "Reaching KeRemotePlay on the host..."))
	var info HostInfo
	var lastErr error
	for i := 0; i < 8; i++ {
		lastErr = agentCall(c.HostIP, c.AgentPort, c.Secret, "GET", "/krp/info", nil, 4*time.Second, &info)
		if lastErr == nil || errors.Is(lastErr, errInviteGone) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if errors.Is(lastErr, errInviteGone) {
		clientLeave(lastErr.Error())
		return lastErr
	}
	if errors.Is(lastErr, errNotHosting) {
		return lastErr
	}
	if lastErr != nil {
		logf(L("Host'a ulaşılamadı: %v", "Could not reach the host: %v"), lastErr)
		return errors.New(L("host'taki KeRemotePlay'e ulaşılamadı. Karşı tarafta program açık olmalı ve Host seçilmiş olmalı", "could not reach KeRemotePlay on the host. It must be open there, with Host picked"))
	}
	if !info.Sunshine {
		return errors.New(L("host'ta Sunshine çalışmıyor", "Sunshine is not running on the host"))
	}
	if !sunReachable(c.HostIP, info.SunPort) {
		logf(L("Sunshine portları yanıt vermiyor: %s:%d", "Sunshine's ports do not answer: %s:%d"), c.HostIP, sunPortOr(info.SunPort))
		return errors.New(sunUnreachableMsg())
	}

	j.Logf(L("%s'den izin isteniyor, cevabı bekleniyor...", "Asking %s for permission, waiting for the answer..."), hostLabel(c))
	name, _ := os.Hostname()
	var ask struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := agentCall(c.HostIP, c.AgentPort, c.Secret, "POST", "/krp/request", map[string]string{"name": name}, connAskTime+15*time.Second, &ask); err != nil {
		return fmt.Errorf(L("izin istenemedi: %v", "could not ask for permission: %v"), err)
	}
	if !ask.OK {
		return errors.New(ask.Error)
	}

	j.Logf("%s", L("Daha önce eşleşmiş mi kontrol ediliyor...", "Checking for an earlier pairing..."))
	apps, lerr := mlListApps(c.HostIP)
	if lerr == nil {
		clientSetPaired(apps)
		j.Logf("%s", L("Zaten eşleşmiş. Hazırsın.", "Already paired. You are ready."))
		return nil
	}
	logf(L("Moonlight listesi: %v", "Moonlight list: %v"), lerr)

	pin := randPIN()
	cmd, err := mlStartPair(c.HostIP, pin)
	if err != nil {
		return err
	}
	// Moonlight's pairing window never closes by itself (it ends with a dialog
	// that needs an OK click), so it is always closed for the user here.
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	defer func() {
		select {
		case <-exited:
		default:
			_ = cmd.Process.Kill()
		}
	}()
	j.Logf("%s", L("Moonlight eşleştirme penceresi açıldı, PIN host'a otomatik gönderiliyor...", "Moonlight's pairing window is open, sending the PIN to the host..."))
	time.Sleep(2 * time.Second)
	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	perr := agentCall(c.HostIP, c.AgentPort, c.Secret, "POST", "/krp/pair", map[string]string{"pin": pin, "name": name}, 70*time.Second, &res)
	if perr == nil && !res.OK {
		perr = errors.New(res.Error)
	}
	if perr != nil {
		// Moonlight may have been paired after all (then its window only says so).
		if apps, err := mlListApps(c.HostIP); err == nil {
			clientSetPaired(apps)
			j.Logf("%s", msgReady())
			return nil
		}
		return fmt.Errorf(L("host eşleştirmeyi onaylayamadı: %v", "the host could not approve the pairing: %v"), perr)
	}
	j.Logf("%s", L("Host onayladı, eşleştirme tamamlanıyor...", "The host approved, finishing the pairing..."))
	deadline := time.Now().Add(60 * time.Second)
	for {
		select {
		case <-exited:
			deadline = time.Now() // window closed by the user: check once more
		case <-time.After(1500 * time.Millisecond):
		}
		if apps, lerr = mlListApps(c.HostIP); lerr == nil || time.Now().After(deadline) {
			break
		}
	}
	if lerr != nil {
		return fmt.Errorf(L("eşleştirme tamamlanamadı: %v", "pairing did not finish: %v"), lerr)
	}
	clientSetPaired(apps)
	j.Logf("%s", msgReady())
	return nil
}

func msgReady() string {
	return L("Bağlantı hazır. Oyna'ya basabilirsin.", "Connection ready. You can press Play.")
}

func clientSetPaired(apps []string) {
	withConfig(func(cf *Config) { cf.Client.Paired, cf.Client.Apps = true, apps })
	kickHealth()
}

func waitExit(cmd *exec.Cmd, timeout time.Duration) {
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func clientRefreshApps(j *Job) error {
	c := getConfig().Client
	apps, err := mlListApps(c.HostIP)
	if err != nil {
		if errors.Is(err, errNotPaired) {
			withConfig(func(cf *Config) { cf.Client.Paired = false })
		}
		return err
	}
	clientSetPaired(apps)
	j.Logf(L("%d uygulama bulundu.", "%d apps found."), len(apps))
	return nil
}

// clientRejoin logs back into the host's network, e.g. after Tailscale switched
// to another tailnet, logged out or the device key expired.
func clientRejoin(j *Job) error {
	if err := clientEnsureNet(j); err != nil {
		return err
	}
	j.Logf("%s", L("Host'un ağına bağlısın.", "You are on the host's network."))
	return nil
}

// needsMeasurement: only auto choices depend on the line speed.
func needsMeasurement(s StreamSettings) bool {
	return s.Res == "" || s.Res == "auto" || s.FPS <= 0 || s.BitrateKbps <= 0
}

func clientMeasure(j *Job) (*SpeedResult, error) {
	c := getConfig().Client
	if c.HostIP == "" {
		return nil, errors.New(L("önce host'a bağlan", "connect to the host first"))
	}
	j.Logf("%s", L("Hat ölçülüyor (yaklaşık 5 saniye)...", "Testing the line (about 5 seconds)..."))
	mbps, err := measureSpeed(c.HostIP, c.AgentPort, c.Secret, 5*time.Second, 3)
	if err != nil {
		return nil, fmt.Errorf(L("hat ölçülemedi: %v (host'ta KeRemotePlay açık olmalı)", "could not test the line: %v (KeRemotePlay must be open on the host)"), err)
	}
	res := &SpeedResult{Mbps: math.Round(mbps*10) / 10, At: time.Now().Unix()}
	withConfig(func(cf *Config) { cf.Client.Speed = res })
	j.Logf(L("Host → bu bilgisayar: %.1f Mbps", "Host → this PC: %.1f Mbps"), res.Mbps)
	if res.Mbps < 8 {
		j.Logf("%s", L("Hat düşük. Genelde sebep host'un internet paketindeki upload hızıdır.", "The line is slow. Usually the host's upload speed is the reason."))
	}
	return res, nil
}

// clientPlay does whatever is still missing (install, network, pairing) and
// starts the stream, so "Play" works however the last session ended.
func clientPlay(j *Job, app string) error {
	if streaming() {
		return errors.New(L("yayın zaten açık; önce Moonlight penceresini kapat", "the stream is already open; close the Moonlight window first"))
	}
	if err := clientInstall(j); err != nil {
		return err
	}
	if err := clientEnsureNet(j); err != nil {
		return err
	}
	c := getConfig().Client
	// Takes a moment when paired; also notices a host that forgot this PC.
	j.Logf("%s", L("Host kontrol ediliyor...", "Checking the host..."))
	apps, lerr := mlListAppsWithin(c.HostIP, 12*time.Second)
	switch {
	case lerr == nil:
		clientSetPaired(apps)
	case !c.Paired || errors.Is(lerr, errNotPaired):
		if err := clientPair(j); err != nil {
			return err
		}
	default:
		logf(L("Moonlight listesi: %v", "Moonlight list: %v"), lerr)
	}
	c = getConfig().Client
	if len(c.Apps) > 0 && !slices.Contains(c.Apps, app) {
		app = c.Apps[0]
	}
	sp := c.Speed
	if needsMeasurement(c.Stream) && !speedFresh(sp, 10*time.Minute) {
		if res, err := clientMeasure(j); err == nil {
			sp = res
		} else {
			j.Logf("%v", err)
			if sp != nil {
				j.Logf(L("Son ölçüm kullanılıyor: %.1f Mbps", "Using the last test: %.1f Mbps"), sp.Mbps)
			} else {
				j.Logf("%s", L("Ölçüm yok; güvenli ayar seçiliyor.", "No test result; picking safe settings."))
			}
		}
	}
	ed := effectiveDisplay(clientDisplay(), c.HostDisp)
	plan := makePlan(c.Stream, sp, ed.W, ed.H, ed.Hz)
	j.Logf(L("Ayar: %s, %.1f Mbps", "Settings: %s, %.1f Mbps"), plan.Label, float64(plan.BitrateKbps)/1000)
	if plan.Note != "" {
		j.Logf("%s", plan.Note)
	}
	// Game audio is never played on the host: Sunshine then routes host audio to its
	// virtual speaker, which keeps the host's Discord (set to real headphones) out of the stream.
	// --quit-after: closing Moonlight ends the session on the host too, so the
	// host notices it and the next connection has to be approved again.
	args := append(plan.moonlightArgs(), "--no-audio-on-host", "--quit-after")
	logf(L("Yayın başlatılıyor: %s → %s %v", "Starting the stream: %s → %s %v"), c.HostIP, app, args)
	if err := mlStream(c.HostIP, app, args); err != nil {
		return err
	}
	j.Logf(L("Moonlight açılıyor: %s", "Opening Moonlight: %s"), app)
	return nil
}

func clientDisplay() DisplayInfo {
	w, h, hz := primaryDisplay()
	return DisplayInfo{W: w, H: h, Hz: hz}
}

func sunUnreachableMsg() string {
	return L("Host'taki KeRemotePlay'e ulaşılıyor ama Sunshine'a ulaşılamıyor; büyük ihtimalle host'un Windows güvenlik duvarı engelliyor. Host KeRemotePlay'i son sürüme güncelleyip \"Devam et\"e bassın (güvenlik duvarı izni eklenir), sonra tekrar dene",
		"KeRemotePlay on the host answers but Sunshine does not; most likely the host's Windows firewall blocks it. The host should update KeRemotePlay to the latest version and press \"Continue\" (this adds the firewall rule), then try again")
}

func sunPortOr(p int) int {
	if p <= 0 {
		return 47989
	}
	return p
}

// sunReachable: Moonlight's first contact (Sunshine's HTTP or HTTPS port)
// answers over Tailscale.
func sunReachable(ip string, port int) bool {
	port = sunPortOr(port)
	return tcpOpen(ip, port) || tcpOpen(ip, port-5)
}

func tcpOpen(ip string, port int) bool {
	if port <= 0 {
		port = AgentPort
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort(ip, strconv.Itoa(port)), 3*time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
