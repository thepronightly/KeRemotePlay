package main

// One-button flows.
//
// Host: "Davet kodu oluştur" → installs what is missing and sets Sunshine up with a
// single UAC prompt, logs into Tailscale in the browser (first time only), takes the
// invite key straight from the clipboard (first time only) and puts the invite code
// on the clipboard.
//
// Client: paste the code → installs what is missing (single UAC prompt), joins the
// host's network, pairs Moonlight and closes its pairing window by itself.

import (
	"errors"
	"strings"
	"sync"
	"time"
)

func waitFor(cond func() bool, max time.Duration) bool {
	deadline := time.Now().Add(max)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// ---- host ----------------------------------------------------------------

var (
	phaseMu   sync.Mutex
	hostPhase string // "" | setup | login | key | done
)

func setHostPhase(p string) { phaseMu.Lock(); hostPhase = p; phaseMu.Unlock() }
func getHostPhase() string  { phaseMu.Lock(); defer phaseMu.Unlock(); return hostPhase }

// hostSetupReq lists the admin-only work the host still needs.
func hostSetupReq(withLegacy bool) elevReq {
	r := elevReq{Action: "setup", SID: currentUserSID()}
	for _, k := range []string{"sunshine", "tailscale", "vigembus"} {
		if components[k].Find() == "" {
			r.Install = append(r.Install, k)
		}
	}
	// New Sunshine login only when there is none or it is refused; a Sunshine
	// that is still starting (e.g. right after boot) is waited for instead.
	c := getConfig().Host
	switch {
	case sunshineExe() == "" || c.SunUser == "":
		r.User, r.Pass = "keremoteplay", randPassword(20)
	default:
		if st := sunCheckCreds(c.SunUser, c.SunPass); st == "bad" || st == "unset" {
			r.User, r.Pass = "keremoteplay", randPassword(20)
		}
	}
	if sunshineExe() != "" && sunServiceState().needsFix() {
		r.SunSvc = true
	}
	r.Firewall = !firewallRuleExists()
	r.Legacy = withLegacy && legacyNeedsCleanup()
	return r
}

func hostReady(v HostView) bool {
	return v.SunCreds == "ok" && v.TS.Running() && v.AuthKeySet && v.Firewall
}

func startHost() {
	startAgent()
	startRelay()
	startSessionWatch()
}

func startHostPrepare() error { return runJob("Hazırlanıyor", hostPrepare) }

// hostPrepare takes the host from nothing to a ready invite code.
func hostPrepare(j *Job) (err error) {
	defer func() {
		if err != nil {
			setHostPhase("")
		}
		invalidateHostCaches()
	}()
	setHostPhase("setup")
	hostStartSunshine()
	req := hostSetupReq(true)
	if !req.empty() {
		// Piggybacks on the prompt that is shown anyway.
		req.TSUnatt = true
		if len(req.Install) > 0 {
			names := make([]string, len(req.Install))
			for i, k := range req.Install {
				names[i] = components[k].Label
			}
			j.Logf("Kurulacak: %s", strings.Join(names, ", "))
		}
		if err := elevate(j, req); err != nil {
			return err
		}
		invalidateHostCaches()
	}
	if req.User != "" {
		withConfig(func(c *Config) { c.Host.SunUser, c.Host.SunPass = req.User, req.Pass })
	}
	c := getConfig().Host
	j.Logf("Sunshine bekleniyor...")
	last := ""
	if !waitFor(func() bool {
		st := sunCheckCreds(c.SunUser, c.SunPass)
		if st != last {
			logf("Sunshine durumu: %s (servis %+v)", st, sunServiceState())
			last = st
		}
		return st == "ok"
	}, 90*time.Second) {
		if st := sunCheckCreds(c.SunUser, c.SunPass); st == "bad" || st == "unset" {
			return errors.New("Sunshine şifresi değişmiş; tekrar dene, yenisi ayarlanır")
		}
		return errors.New("Sunshine yanıt vermedi. Bilgisayarı yeniden başlatıp tekrar dene")
	}
	// Nobody stays paired from an earlier session (see session.go).
	if !sessionActive() {
		if err := sunUnpairAll(c.SunUser, c.SunPass); err != nil {
			logf("Eski eşleşmeler silinemedi: %v", err)
		}
		connReset()
	}

	setHostPhase("login")
	if !waitFor(func() bool { s := tsStatusFresh(); return s.Installed && s.BackendState != "" }, 60*time.Second) {
		return errors.New("Tailscale servisi başlamadı. Bilgisayarı yeniden başlatıp tekrar dene")
	}
	tsEnsureStarted(j)
	tsRemember()
	hostEnsureTailnet(j)
	tsUp()
	if !tsStatusFresh().Running() {
		j.Logf("Tarayıcıda Tailscale açılıyor: Google, Microsoft ya da GitHub hesabınla giriş yap (sadece ilk seferde).")
		if err := tsInteractiveLogin(j); err != nil {
			return err
		}
	}
	st := tsStatusFresh()
	if c := getConfig().Host; c.Tailnet != "" && st.Tailnet != "" && st.Tailnet != c.Tailnet {
		// The key belongs to the other network; invites need one from this one.
		logf("Tailscale ağı değişti (%s → %s); yeni anahtar gerekiyor", c.Tailnet, st.Tailnet)
		withConfig(func(c *Config) { c.Host.AuthKey = "" })
	}
	if st.Tailnet != "" {
		withConfig(func(c *Config) { c.Host.Tailnet = st.Tailnet })
	}

	if getConfig().Host.AuthKey == "" {
		setHostPhase("key")
		if err := hostWaitKey(j); err != nil {
			return err
		}
	}
	setHostPhase("done")
	if inv := hostView().Invite; inv != "" {
		if clipWriteText(inv) == nil {
			j.Logf("Davet kodu panoya kopyalandı. Arkadaşına yapıştırıp gönder.")
		}
	}
	return nil
}

// hostEnsureTailnet switches Tailscale back to the host's network when another
// profile became active (invites would otherwise carry the wrong address).
func hostEnsureTailnet(j *Job) {
	want := getConfig().Host.Tailnet
	if want == "" || tsStatusFresh().Tailnet == want {
		return
	}
	for _, p := range tsProfiles() {
		if p.Tailnet != want || p.Current {
			continue
		}
		j.Logf("Tailscale KeRemotePlay ağına geçiriliyor...")
		if err := tsSwitch(p.ID); err != nil {
			logf("tailscale switch %s: %v", p.ID, err)
			return
		}
		tsUp()
		waitFor(func() bool { return tsStatusFresh().Running() }, 15*time.Second)
		return
	}
}

// hostStartSunshine starts Sunshine's service if the app is allowed to; if not,
// the one-time setup does it.
func hostStartSunshine() {
	s := sunServiceState()
	if !s.Exists || !s.CanControl {
		return
	}
	if !s.Running {
		if err := sunServiceSet(true); err != nil {
			logf("Sunshine başlatılamadı: %v", err)
			return
		}
	}
	waitFor(sunshineRunning, 20*time.Second)
}

func hostStopSunshine(j *Job) {
	s := sunServiceState()
	if !s.Running {
		return
	}
	if !s.CanControl {
		logf("Sunshine kapatılamadı: izin yok")
		return
	}
	j.Logf("Sunshine kapatılıyor...")
	if err := sunServiceSet(false); err != nil {
		logf("Sunshine kapatılamadı: %v", err)
		return
	}
	waitFor(func() bool { return !sunServiceState().Running }, 15*time.Second)
}

// hostWaitKey opens Tailscale's key page and picks the new key up from the
// clipboard as soon as it is copied (it can also be pasted into the app).
func hostWaitKey(j *Job) error {
	_ = openURL("https://login.tailscale.com/admin/settings/keys")
	j.Logf("Tarayıcıda Tailscale anahtar sayfası açıldı. Anahtarı oluşturup Kopyala'ya basman yeterli.")
	deadline := time.Now().Add(20 * time.Minute)
	for time.Now().Before(deadline) {
		if getConfig().Host.AuthKey != "" {
			return nil
		}
		if k := findAuthKey(clipReadText()); k != "" {
			if err := hostSetAuthKey(k); err == nil {
				j.Logf("Anahtar panodan alındı.")
				return nil
			}
		}
		time.Sleep(700 * time.Millisecond)
	}
	return errors.New("anahtar 20 dakika içinde gelmedi; tekrar dene")
}

// findAuthKey extracts a Tailscale auth key from arbitrary text.
func findAuthKey(s string) string {
	for _, f := range strings.Fields(s) {
		f = strings.Trim(f, "\"'`.,;")
		if strings.HasPrefix(f, "tskey-auth-") && len(f) > 20 {
			return f
		}
	}
	return ""
}

// findInvite extracts an invite code from arbitrary text (e.g. a chat message).
func findInvite(s string) string {
	i := strings.Index(s, invitePrefix)
	if i < 0 {
		return ""
	}
	rest := s[i:]
	end := strings.IndexFunc(rest, func(r rune) bool {
		return !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	})
	if end > 0 {
		rest = rest[:end]
	}
	if _, err := decodeInvite(rest); err != nil {
		return ""
	}
	return rest
}

// ---- client --------------------------------------------------------------

// clientInstall installs Moonlight and Tailscale if missing (one UAC prompt).
func clientInstall(j *Job) error {
	req := elevReq{Action: "setup"}
	for _, k := range []string{"moonlight", "tailscale"} {
		if components[k].Find() == "" {
			req.Install = append(req.Install, k)
		}
	}
	if req.empty() {
		return nil
	}
	if err := elevate(j, req); err != nil {
		return err
	}
	tsInvalidate()
	j.Logf("Tailscale servisi bekleniyor...")
	if !waitFor(func() bool { s := tsStatusFresh(); return s.Installed && s.BackendState != "" }, 60*time.Second) {
		return errors.New("Tailscale servisi başlamadı. Bilgisayarı yeniden başlatıp tekrar dene")
	}
	return nil
}
