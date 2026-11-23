package main

// Who may connect to the host.
//
// A paired Moonlight talks to Sunshine directly and asks nobody, so approval only
// means something if nobody stays paired. Pairings are wiped when the host starts
// and whenever a session ends; a new pairing needs the host's "Evet" first.

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ConnReq is a friend waiting for the host's answer.
type ConnReq struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	IP   string `json:"ip"`
}

type connWait struct {
	ConnReq
	answer chan bool
}

const connBlockFor = 30 * time.Second

var (
	connMu      sync.Mutex
	connSeq     int
	connPending *connWait
	connAllowed = map[string]string{}    // client IP → name
	connBlocked = map[string]time.Time{} // client IP → asking again allowed from
)

// connAsk shows the request to the host and waits for the answer.
func connAsk(ctx context.Context, ip, name string, timeout time.Duration) error {
	connMu.Lock()
	if _, ok := connAllowed[ip]; ok {
		connMu.Unlock()
		return nil
	}
	if time.Now().Before(connBlocked[ip]) {
		connMu.Unlock()
		return errors.New("host bu bağlantıyı az önce reddetti")
	}
	if connPending != nil {
		connMu.Unlock()
		return errors.New("host şu an başka bir bağlantı isteğine bakıyor, birazdan tekrar dene")
	}
	connSeq++
	w := &connWait{ConnReq{connSeq, name, ip}, make(chan bool, 1)}
	connPending = w
	connMu.Unlock()

	logf("Bağlantı isteği: %s (%s)", name, ip)
	hostAlert(true)
	defer hostAlert(false)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	limit := time.After(timeout)
	yes, answered := false, false
	for waiting := true; waiting; {
		select {
		case yes = <-w.answer:
			answered, waiting = true, false
		case <-tick.C:
			hostAlert(true)
		case <-limit:
			waiting = false
		case <-ctx.Done():
			waiting = false
		}
	}
	connMu.Lock()
	defer connMu.Unlock()
	if connPending == w {
		connPending = nil
	}
	switch {
	case yes:
		connAllowed[ip] = name
		logf("Bağlantıya izin verildi: %s", name)
		return nil
	case answered:
		connBlocked[ip] = time.Now().Add(connBlockFor)
		logf("Bağlantı reddedildi: %s", name)
		return errors.New("host bağlantıyı reddetti")
	}
	logf("Bağlantı isteği cevapsız kaldı: %s", name)
	return errors.New("host cevap vermedi")
}

// connAnswer is the host's click on the question.
func connAnswer(id int, yes bool) {
	connMu.Lock()
	defer connMu.Unlock()
	if connPending != nil && connPending.ID == id {
		connPending.answer <- yes
		connPending = nil
	}
}

func connApproved(ip string) bool {
	connMu.Lock()
	defer connMu.Unlock()
	_, ok := connAllowed[ip]
	return ok
}

func connReset() {
	connMu.Lock()
	connAllowed = map[string]string{}
	connMu.Unlock()
}

// connView is what the host's window shows.
func connView() (req *ConnReq, clients []string) {
	connMu.Lock()
	defer connMu.Unlock()
	if connPending != nil {
		r := connPending.ConnReq
		req = &r
	}
	for _, n := range connAllowed {
		clients = append(clients, n)
	}
	return req, clients
}

// leaveRole closes everything the host or client screen opened: nothing stays
// running once the app is back at the role question or closed. The audio relay
// and the session watch follow the mode by themselves.
func leaveRole(j *Job, mode string) {
	switch mode {
	case "host":
		hostEndSession("Host'tan çıkıldı", false)
		stopAgent()
		hostStopSunshine(j)
		tsRestore(j)
	case "client":
		mlStop()
		tsRestore(j)
		clientLeave("")
	}
}

// ---- sessions ------------------------------------------------------------

var (
	sessMu      sync.Mutex
	sessOn      bool
	sessionOnce sync.Once
	hotkeyMu    sync.Mutex
	hotkeyErr   string
)

func sessionActive() bool {
	sessMu.Lock()
	defer sessMu.Unlock()
	return sessOn
}

func setHotkeyErr(s string) { hotkeyMu.Lock(); hotkeyErr = s; hotkeyMu.Unlock() }
func getHotkeyErr() string  { hotkeyMu.Lock(); defer hotkeyMu.Unlock(); return hotkeyErr }

// startSessionWatch follows the stream on the host: it notices a session that
// ended by itself and keeps Ctrl+F4 reserved only while one is running.
func startSessionWatch() {
	sessionOnce.Do(func() {
		go hotkeyLoop(sessionActive, func() { hostEndSession("Oturum Ctrl+F4 ile bitirildi", true) })
		go func() {
			for {
				time.Sleep(2 * time.Second)
				if getConfig().Mode == "host" {
					sessionTick()
				}
			}
		}()
	})
}

func sessionTick() {
	game, ok := sunCurrentGame()
	if !ok {
		return
	}
	sessMu.Lock()
	was := sessOn
	sessOn = game != 0
	sessMu.Unlock()
	if !was && game != 0 {
		logf("Yayın başladı")
	}
	if was && game == 0 {
		hostEndSession("Oturum bitti", false)
	}
}

// hostEndSession stops the stream and takes every approval back; the friend has
// to ask again. With newCode the old invite code stops working as well, so a
// kicked friend cannot even ask without a new one.
func hostEndSession(why string, newCode bool) {
	c := getConfig().Host
	if c.SunUser != "" && sunshineRunning() {
		if err := sunCloseApp(c.SunUser, c.SunPass); err != nil {
			logf("Yayın kapatılamadı: %v", err)
		}
		if err := sunUnpairAll(c.SunUser, c.SunPass); err != nil {
			logf("Eşleşmeler silinemedi: %v", err)
		}
		stopped := func() bool { g, ok := sunCurrentGame(); return !ok || g == 0 }
		if !waitFor(stopped, 3*time.Second) {
			logf("Yayın kapanmadı; Sunshine yeniden başlatılıyor")
			_, _, _ = sunRequest("POST", "/api/restart", c.SunUser, c.SunPass, map[string]string{})
		}
	}
	connReset()
	sessMu.Lock()
	sessOn = false
	sessMu.Unlock()
	if newCode {
		withConfig(func(c *Config) { c.Host.Secret = randHex(16) })
	}
	logf("%s", why)
	setAgent(func(s *AgentState) { s.LastEvent = why + " (" + time.Now().Format("15:04") + ")" })
}
