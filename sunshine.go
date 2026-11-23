package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Sunshine talks HTTPS with a self-signed certificate on 127.0.0.1, so we skip
// verification for this loopback-only client. Redirects are not followed so we
// can detect the "/welcome" redirect of an unconfigured Sunshine.
var sunHTTP = &http.Client{
	Timeout: 8 * time.Second,
	Transport: &http.Transport{
		Proxy:           nil,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // loopback only
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// sunBaseURL may be overridden in tests.
var sunBaseURL = func() string { return fmt.Sprintf("https://127.0.0.1:%d", sunshineUIPort()) }

func sunshineUIPort() int {
	base := 47989
	if p := sunshineConfFile(); p != "" {
		if f, err := os.Open(p); err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if strings.HasPrefix(line, "#") {
					continue
				}
				k, v, ok := strings.Cut(line, "=")
				if ok && strings.TrimSpace(k) == "port" {
					if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 1024 {
						base = n
					}
				}
			}
			f.Close()
		}
	}
	return base + 1
}

// SunService is the state of Sunshine's Windows service.
type SunService struct {
	Exists     bool `json:"exists"`
	Running    bool `json:"running"`
	Manual     bool `json:"manual"`  // does not start with Windows
	CanControl bool `json:"control"` // this user may start and stop it
}

// needsFix: the one-time admin setup of the service is still missing.
func (s SunService) needsFix() bool { return s.Exists && (!s.Manual || !s.CanControl) }

// addServiceACE allows sid to start and stop a service, given the service's
// security descriptor as "sc sdshow" prints it.
func addServiceACE(sddl, sid string) string {
	sddl = strings.TrimSpace(sddl)
	if sid == "" || strings.Contains(sddl, ";"+sid+")") {
		return sddl
	}
	ace := "(A;;CCLCSWRPWPLOCRRC;;;" + sid + ")"
	if i := strings.Index(sddl, "S:"); i >= 0 { // the audit part comes after the permissions
		return sddl[:i] + ace + sddl[i:]
	}
	return sddl + ace
}

func sunshineRunning() bool {
	addr := strings.TrimPrefix(sunBaseURL(), "https://")
	c, err := net.DialTimeout("tcp", addr, 800*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func sunRequest(method, path, user, pass string, body any) (*http.Response, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, sunBaseURL()+path, rd)
	if err != nil {
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetBasicAuth(user, pass)
	resp, err := sunHTTP.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp, b, nil
}

// sunCheckCreds returns "ok", "bad", "unset", "down" or "error".
func sunCheckCreds(user, pass string) string {
	if !sunshineRunning() {
		return "down"
	}
	if user == "" {
		// Probe without creds to learn whether Sunshine has credentials at all.
		resp, _, err := sunRequest("GET", "/api/apps", "", "", nil)
		if err != nil {
			return "error"
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			return "unset"
		}
		return "bad"
	}
	resp, _, err := sunRequest("GET", "/api/apps", user, pass, nil)
	if err != nil {
		return "error"
	}
	switch {
	case resp.StatusCode == 200:
		return "ok"
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return "unset"
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return "bad"
	}
	return "error"
}

func jsonStatusTrue(b []byte) (bool, bool) {
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return false, false
	}
	switch v := m["status"].(type) {
	case bool:
		return v, true
	case string:
		return v == "true", true
	}
	return false, false
}

func addrMatches(pairingAddr, clientIP string) bool {
	a := strings.TrimPrefix(strings.ToLower(pairingAddr), "::ffff:")
	return a == strings.ToLower(clientIP)
}

// sunSubmitPin approves a waiting Moonlight pairing request coming from clientIP.
// Supports both Sunshine APIs:
//   - newer (2026.10+): GET /api/pin lists pending requests, POST needs pairing_id
//   - older: POST /api/pin {pin, name} succeeds once a request is pending
func sunSubmitPin(user, pass, clientIP, pin, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	legacy := false
	for {
		if !legacy {
			resp, b, err := sunRequest("GET", "/api/pin", user, pass, nil)
			if err != nil {
				return fmt.Errorf(msgSunUnreachable(), err)
			}
			switch {
			case resp.StatusCode == 401 || resp.StatusCode == 403:
				return errSunPass
			case resp.StatusCode == 200 && bytes.Contains(b, []byte(`"pairings"`)):
				var lst struct {
					Pairings []struct {
						ID      string `json:"id"`
						Name    string `json:"name"`
						Address string `json:"address"`
					} `json:"pairings"`
				}
				if err := json.Unmarshal(b, &lst); err != nil {
					return fmt.Errorf(L("Sunshine yanıtı okunamadı: %v", "could not read Sunshine's reply: %v"), err)
				}
				// Moonlight may reach Sunshine from another address than the agent
				// saw (e.g. Tailscale IPv6); a lone request is taken after a while.
				lone := len(lst.Pairings) == 1 && time.Until(deadline) < timeout-10*time.Second
				for _, p := range lst.Pairings {
					if !addrMatches(p.Address, clientIP) && !lone {
						continue
					}
					_, pb, err := sunRequest("POST", "/api/pin", user, pass, map[string]string{"pairing_id": p.ID, "pin": pin, "name": name})
					if err != nil {
						return err
					}
					if ok, _ := jsonStatusTrue(pb); ok {
						return nil
					}
					return fmt.Errorf(L("Sunshine PIN'i kabul etmedi: %s", "Sunshine did not accept the PIN: %s"), strings.TrimSpace(string(pb)))
				}
			default:
				legacy = true
				logf("%s", L("Sunshine eski eşleştirme API'si kullanılıyor", "Sunshine uses the old pairing API"))
				continue
			}
		} else {
			resp, pb, err := sunRequest("POST", "/api/pin", user, pass, map[string]string{"pin": pin, "name": name})
			if err != nil {
				return fmt.Errorf(msgSunUnreachable(), err)
			}
			if resp.StatusCode == 401 || resp.StatusCode == 403 {
				return errSunPass
			}
			if ok, _ := jsonStatusTrue(pb); ok {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return newErr("Moonlight'tan eşleştirme isteği gelmedi (zaman aşımı)", "no pairing request came from Moonlight (timed out)")
		}
		time.Sleep(1500 * time.Millisecond)
	}
}

var errSunPass = newErr("Sunshine şifresi geçersiz (host uygulamasında Sunshine bağlantısını yeniden ayarla)", "Sunshine's password is wrong (set the Sunshine connection up again in the host app)")

func msgSunUnreachable() string {
	return L("Sunshine'a ulaşılamadı: %v", "could not reach Sunshine: %v")
}

// sunInfoURL may be overridden in tests.
var sunInfoURL = func() string {
	return fmt.Sprintf("http://127.0.0.1:%d/serverinfo?uniqueid=0123456789ABCDEF", sunshineUIPort()-1)
}

var currentGameRe = regexp.MustCompile(`<currentgame>(\d+)</currentgame>`)

// sunCurrentGame returns the running app's id, 0 if nothing is being streamed.
// ok is false when Sunshine cannot be asked (it may be restarting).
func sunCurrentGame() (game int, ok bool) {
	resp, err := sunHTTP.Get(sunInfoURL())
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	m := currentGameRe.FindSubmatch(b)
	if m == nil {
		return 0, false
	}
	game, err = strconv.Atoi(string(m[1]))
	return game, err == nil
}

// sunCloseApp ends the running app, and with it the stream.
func sunCloseApp(user, pass string) error {
	return sunPost("/api/apps/close", user, pass)
}

// sunUnpairAll forgets every paired Moonlight; each must pair again to connect.
func sunUnpairAll(user, pass string) error {
	return sunPost("/api/clients/unpair-all", user, pass)
}

func sunPost(path, user, pass string) error {
	resp, b, err := sunRequest("POST", path, user, pass, map[string]string{})
	if err != nil {
		return err
	}
	if ok, _ := jsonStatusTrue(b); resp.StatusCode != 200 || !ok {
		return fmt.Errorf("%s: %s %s", path, resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

var errConfUnparsable = newErr("Sunshine ayar dosyası beklenmedik biçimde", "Sunshine's config file has an unexpected format")

// sunSetConf changes Sunshine settings through its own web API (Sunshine runs as
// SYSTEM, so no UAC prompt), then restarts it. Sunshine's POST /api/config replaces
// the whole file, so the current file is read, changed and sent back complete.
func sunSetConf(j *Job, user, pass string, set []confPair) error {
	path := sunshineConfFile()
	if path == "" {
		return errors.New(L("Sunshine ayar dosyası bulunamadı", "Sunshine's config file not found"))
	}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	pairs, ok := parseConf(string(b))
	if !ok {
		return errConfUnparsable
	}
	pairs = setConfPairs(pairs, set)
	body := map[string]string{}
	for _, p := range pairs {
		body[p.Key] = p.Value
	}
	resp, rb, err := sunRequest("POST", "/api/config", user, pass, body)
	if err != nil {
		return fmt.Errorf(msgSunUnreachable(), err)
	}
	if ok, _ := jsonStatusTrue(rb); resp.StatusCode != 200 || !ok {
		return fmt.Errorf(L("Sunshine ayarı kaydetmedi: %s %s", "Sunshine did not save the setting: %s %s"), resp.Status, strings.TrimSpace(string(rb)))
	}
	j.Logf("%s", L("Sunshine ayarı kaydedildi, yeniden başlatılıyor...", "Sunshine saved the setting, restarting..."))
	_, _, _ = sunRequest("POST", "/api/restart", user, pass, map[string]string{}) // may not answer
	time.Sleep(3 * time.Second)
	for i := 0; i < 30; i++ {
		if sunCheckCreds(user, pass) == "ok" {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New(L("Sunshine yeniden başladıktan sonra yanıt vermedi", "Sunshine did not answer after restarting"))
}

func sunGamepadMode() string {
	p := sunshineConfFile()
	if p == "" {
		return ""
	}
	b, _ := os.ReadFile(p)
	return confValue(string(b), "gamepad")
}
