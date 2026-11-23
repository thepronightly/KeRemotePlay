package main

// One-button flows.
//
// Host: "Make an invite code" → installs what is missing and sets Sunshine up with a
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

func startHostPrepare() error { return runJob(L("Hazırlanıyor", "Getting ready"), hostPrepare) }

var errTSNoStart = newErr("Tailscale servisi başlamadı. Bilgisayarı yeniden başlatıp tekrar dene", "the Tailscale service did not start. Restart the PC and try again")

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
				names[i] = components[k].label()
			}
			j.Logf(L("Kurulacak: %s", "To install: %s"), strings.Join(names, ", "))
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
	j.Logf("%s", L("Sunshine bekleniyor...", "Waiting for Sunshine..."))
	last := ""
	if !waitFor(func() bool {
		st := sunCheckCreds(c.SunUser, c.SunPass)
		if st != last {
			logf(L("Sunshine durumu: %s (servis %+v)", "Sunshine state: %s (service %+v)"), st, sunServiceState())
			last = st
		}
		return st == "ok"
	}, 90*time.Second) {
		if st := sunCheckCreds(c.SunUser, c.SunPass); st == "bad" || st == "unset" {
			return errors.New(L("Sunshine şifresi değişmiş; tekrar dene, yenisi ayarlanır", "Sunshine's password changed; try again and a new one is set"))
		}
		return errors.New(L("Sunshine yanıt vermedi. Bilgisayarı yeniden başlatıp tekrar dene", "Sunshine did not answer. Restart the PC and try again"))
	}
	// Nobody stays paired from an earlier session (see session.go).
	if !sessionActive() {
		if err := sunUnpairAll(c.SunUser, c.SunPass); err != nil {
			logf(L("Eski eşleşmeler silinemedi: %v", "Could not remove old pairings: %v"), err)
		}
		connReset()
	}

	setHostPhase("login")
	if !waitFor(func() bool { s := tsStatusFresh(); return s.Installed && s.BackendState != "" }, 60*time.Second) {
		return errTSNoStart
	}
	tsEnsureStarted(j)
	tsRemember()
	hostEnsureTailnet(j)
	tsUp()
	if !tsStatusFresh().Running() {
		j.Logf("%s", L("Tarayıcıda Tailscale açılıyor: Google, Microsoft ya da GitHub hesabınla giriş yap (sadece ilk seferde).", "Opening Tailscale in the browser: log in with your Google, Microsoft or GitHub account (first time only)."))
		if err := tsInteractiveLogin(j); err != nil {
			return err
		}
	}
	st := tsStatusFresh()
	if c := getConfig().Host; c.Tailnet != "" && st.Tailnet != "" && st.Tailnet != c.Tailnet {
		// The key belongs to the other network; invites need one from this one.
		logf(L("Tailscale ağı değişti (%s → %s); yeni anahtar gerekiyor", "Tailscale network changed (%s → %s); a new key is needed"), c.Tailnet, st.Tailnet)
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
			j.Logf("%s", L("Davet kodu panoya kopyalandı. Arkadaşına yapıştırıp gönder.", "Invite code copied to the clipboard. Paste it to your friend."))
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
		j.Logf("%s", L("Tailscale KeRemotePlay ağına geçiriliyor...", "Switching Tailscale to the KeRemotePlay network..."))
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
			logf(L("Sunshine başlatılamadı: %v", "Could not start Sunshine: %v"), err)
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
		logf("%s", L("Sunshine kapatılamadı: izin yok", "Could not stop Sunshine: no permission"))
		return
	}
	j.Logf("%s", L("Sunshine kapatılıyor...", "Stopping Sunshine..."))
	if err := sunServiceSet(false); err != nil {
		logf(L("Sunshine kapatılamadı: %v", "Could not stop Sunshine: %v"), err)
		return
	}
	waitFor(func() bool { return !sunServiceState().Running }, 15*time.Second)
}

// hostWaitKey opens Tailscale's key page and picks the new key up from the
// clipboard as soon as it is copied (it can also be pasted into the app).
func hostWaitKey(j *Job) error {
	_ = openURL("https://login.tailscale.com/admin/settings/keys")
	j.Logf("%s", L("Tarayıcıda Tailscale anahtar sayfası açıldı. Anahtarı oluşturup Kopyala'ya basman yeterli.", "The Tailscale keys page is open in the browser. Make the key and press Copy, that is all."))
	deadline := time.Now().Add(20 * time.Minute)
	for time.Now().Before(deadline) {
		if getConfig().Host.AuthKey != "" {
			return nil
		}
		if k := findAuthKey(clipReadText()); k != "" {
			if err := hostSetAuthKey(k); err == nil {
				j.Logf("%s", L("Anahtar panodan alındı.", "Key taken from the clipboard."))
				return nil
			}
		}
		time.Sleep(700 * time.Millisecond)
	}
	return errors.New(L("anahtar 20 dakika içinde gelmedi; tekrar dene", "no key arrived within 20 minutes; try again"))
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
	j.Logf("%s", L("Tailscale servisi bekleniyor...", "Waiting for the Tailscale service..."))
	if !waitFor(func() bool { s := tsStatusFresh(); return s.Installed && s.BackendState != "" }, 60*time.Second) {
		return errTSNoStart
	}
	return nil
}
