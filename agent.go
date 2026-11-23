package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The agent runs on the host and is reachable only over Tailscale. It lets a
// friend's KeRemotePlay approve its own Moonlight pairing request, so nobody has
// to type a PIN into Sunshine's web panel.

type AgentState struct {
	Running   bool   `json:"running"`
	Error     string `json:"error,omitempty"`
	LastEvent string `json:"lastEvent,omitempty"`
}

var (
	agentMu   sync.Mutex
	agentSt   AgentState
	pairMu    sync.Mutex
	agentRun  sync.Mutex
	agentStop chan struct{} // non-nil while the agent is meant to run
)

var allowedNets = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fd7a:115c:a1e0::/48"),
}

func remoteAllowed(remote string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return netip.Addr{}, false
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	a = a.Unmap()
	if a.IsLoopback() {
		return a, true
	}
	for _, p := range allowedNets {
		if p.Contains(a) {
			return a, true
		}
	}
	return a, false
}

func setAgent(fn func(s *AgentState)) {
	agentMu.Lock()
	fn(&agentSt)
	agentMu.Unlock()
}

func getAgent() AgentState {
	agentMu.Lock()
	defer agentMu.Unlock()
	return agentSt
}

var pinRe = regexp.MustCompile(`^[0-9]{4}$`)

func agentHandler() http.Handler {
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) (netip.Addr, bool) {
		ip, ok := remoteAllowed(r.RemoteAddr)
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return ip, false
		}
		secret := getConfig().Host.Secret
		got := r.Header.Get("X-KRP-Secret")
		if secret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
			time.Sleep(time.Second)
			http.Error(w, "bad secret", http.StatusUnauthorized)
			return ip, false
		}
		if getConfig().Mode != "host" { // left the host screen
			http.Error(w, "not hosting", http.StatusServiceUnavailable)
			return ip, false
		}
		return ip, true
	}
	mux.HandleFunc("/krp/info", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth(w, r); !ok {
			return
		}
		name, _ := os.Hostname()
		writeJSON(w, map[string]any{
			"app":      "KeRemotePlay",
			"version":  appVersion,
			"name":     name,
			"sunshine": sunshineRunning(),
			"net":      netInfoCached(),
			"display":  hostDisplay(),
			"sunPort":  sunshineUIPort() - 1,
		})
	})
	mux.HandleFunc("/krp/speed", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth(w, r); !ok {
			return
		}
		speedHandler(w, r)
	})
	// The host's window asks "X bağlanmak istiyor"; the answer ends this call.
	mux.HandleFunc("/krp/request", func(w http.ResponseWriter, r *http.Request) {
		ip, ok := auth(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
		if err := connAsk(r.Context(), ip.String(), cleanName(req.Name), connAskTime); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/krp/pair", func(w http.ResponseWriter, r *http.Request) {
		ip, ok := auth(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			PIN  string `json:"pin"`
			Name string `json:"name"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil || !pinRe.MatchString(req.PIN) {
			writeJSON(w, map[string]any{"ok": false, "error": "geçersiz istek"})
			return
		}
		if !connApproved(ip.String()) {
			writeJSON(w, map[string]any{"ok": false, "error": "host bu bağlantıya izin vermedi"})
			return
		}
		name := cleanName(req.Name)
		if !pairMu.TryLock() {
			writeJSON(w, map[string]any{"ok": false, "error": "host şu an başka bir eşleştirmeyle meşgul, birazdan tekrar dene"})
			return
		}
		defer pairMu.Unlock()
		c := getConfig()
		logf("Eşleştirme isteği: %s (%s)", name, ip)
		err := sunSubmitPin(c.Host.SunUser, c.Host.SunPass, ip.String(), req.PIN, name, 45*time.Second)
		if err != nil {
			logf("Eşleştirme başarısız: %v", err)
			setAgent(func(s *AgentState) { s.LastEvent = fmt.Sprintf("%s eşleştirilemedi: %v", name, err) })
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		logf("Eşleştirildi: %s", name)
		setAgent(func(s *AgentState) {
			s.LastEvent = fmt.Sprintf("%s eşleştirildi (%s)", name, time.Now().Format("15:04"))
		})
		writeJSON(w, map[string]any{"ok": true})
	})
	return mux
}

// connAskTime: how long a friend waits for the host's answer.
const connAskTime = 60 * time.Second

// cleanName makes a client's self-chosen name safe to show.
func cleanName(s string) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r < 32 || r == 127 }), " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return "KeRemotePlay"
	}
	if r := []rune(s); len(r) > 40 {
		s = string(r[:40])
	}
	return s
}

func startAgent() {
	agentRun.Lock()
	defer agentRun.Unlock()
	if agentStop != nil {
		return
	}
	stop := make(chan struct{})
	agentStop = stop
	go func() {
		srv := &http.Server{Handler: agentHandler(), ReadHeaderTimeout: 10 * time.Second}
		go func() { <-stop; _ = srv.Close() }()
		for {
			ln, err := net.Listen("tcp", fmt.Sprintf(":%d", AgentPort))
			if err != nil {
				setAgent(func(s *AgentState) {
					s.Running = false
					s.Error = fmt.Sprintf("port %d açılamadı: %v", AgentPort, err)
				})
			} else {
				setAgent(func(s *AgentState) { s.Running = true; s.Error = "" })
				logf("Eşleştirme servisi açık (port %d)", AgentPort)
				err = srv.Serve(ln)
				if !errors.Is(err, http.ErrServerClosed) {
					setAgent(func(s *AgentState) { s.Running = false; s.Error = fmt.Sprint(err) })
				}
			}
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
}

// stopAgent closes the port; a friend's waiting request ends with it.
func stopAgent() {
	agentRun.Lock()
	defer agentRun.Unlock()
	if agentStop == nil {
		return
	}
	close(agentStop)
	agentStop = nil
	setAgent(func(s *AgentState) { s.Running, s.Error = false, "" })
	logf("Eşleştirme servisi kapatıldı")
}

// ---- client side ---------------------------------------------------------

var agentHTTP = &http.Client{Transport: &http.Transport{Proxy: nil}}

var (
	errInviteGone = errors.New("bu kod artık geçerli değil; host'tan yeni kod iste")
	errNotHosting = errors.New("karşı taraf şu an Host değil; KeRemotePlay'de Host'u seçsin")
)

func agentCall(ip string, port int, secret, method, path string, body any, timeout time.Duration, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, fmt.Sprintf("http://%s%s", net.JoinHostPort(ip, fmt.Sprint(port)), path), rd)
	if err != nil {
		return err
	}
	req.Header.Set("X-KRP-Secret", secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	cl := *agentHTTP
	cl.Timeout = timeout
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 401:
		return errInviteGone
	case 503:
		return errNotHosting
	case 404:
		return errors.New("karşı taraftaki KeRemotePlay eski; ikiniz de aynı sürümü kullanın")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("host yanıtı: %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func hostDisplay() DisplayInfo {
	w, h, hz := primaryDisplay()
	return DisplayInfo{W: w, H: h, Hz: hz}
}
