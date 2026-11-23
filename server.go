package main

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed web/index.html
var indexHTML []byte

type actionReq struct {
	Action string            `json:"action"`
	Args   map[string]string `json:"args"`
}

var quitCh = make(chan struct{}, 1)

// startUI serves the app's interface on a random loopback port. Every API call
// must carry the per-launch token, so other local programs or web pages cannot
// drive the app.
func startUI(addr string) (string, error) {
	token := randHex(16)
	if v := os.Getenv("KRP_UI_TOKEN"); v != "" {
		token = v
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	hostHdr := ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(indexHTML)
	})
	api := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Host != hostHdr || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-KRP-Token")), []byte(token)) != 1 {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/api/state", api(func(w http.ResponseWriter, r *http.Request) {
		c := getConfig()
		st := map[string]any{
			"version": appVersion,
			"lang":    lang(),
			"mode":    c.Mode,
			"job":     currentJob(),
			"log":     logTail(60),
			"logPath": filepath.Join(dataDir(), "krp.log"),
		}
		switch c.Mode {
		case "host":
			st["host"] = hostView()
		case "client":
			touchClientPads()
			st["client"] = clientView()
		}
		// Offer an invite code found on the clipboard while one is still needed.
		if c.Mode == "" || c.Mode == "client" && c.Client.HostIP == "" {
			if inv := findInvite(clipReadText()); inv != "" {
				st["clipInvite"] = inv
			}
		}
		writeJSON(w, st)
	}))
	mux.HandleFunc("/api/action", api(func(w http.ResponseWriter, r *http.Request) {
		var req actionReq
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": L("geçersiz istek", "invalid request")})
			return
		}
		if err := doAction(req); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	}))

	go func() { _ = http.Serve(ln, mux) }()
	return fmt.Sprintf("http://%s/?t=%s", hostHdr, token), nil
}

func doAction(req actionReq) error {
	a := req.Args
	if a == nil {
		a = map[string]string{}
	}
	switch req.Action {
	case "set-lang":
		l := normLang(a["lang"])
		setLang(l)
		withConfig(func(c *Config) { c.Lang = l })
		return nil
	case "set-mode":
		m := a["mode"]
		if m != "host" && m != "client" && m != "" {
			return errors.New(L("geçersiz mod", "invalid mode"))
		}
		was := getConfig().Mode
		if was != "" {
			if m != "" {
				return errors.New(L("önce başa dön", "go back to the start first"))
			}
			return runJob(L("Kapatılıyor", "Closing"), func(j *Job) error {
				withConfig(func(c *Config) { c.Mode = "" })
				leaveRole(j, was)
				return nil
			})
		}
		withConfig(func(c *Config) { c.Mode = m })
		clientLeave("")
		if m == "host" {
			// A new code every time; the old one stops working here.
			withConfig(func(c *Config) { c.Host.Secret = randHex(16) })
			startHost()
			return startHostPrepare()
		}
		return nil
	case "dismiss":
		dismissJob()
		return nil
	case "open-url":
		return openURL(a["url"])
	case "open-log":
		return openURL(filepath.Join(dataDir(), "krp.log"))
	case "quit":
		select {
		case quitCh <- struct{}{}:
		default:
		}
		return nil

	// host
	case "conn-answer":
		id, _ := strconv.Atoi(a["id"])
		connAnswer(id, a["yes"] == "1")
		return nil
	case "end-session":
		hostEndSession(L("Oturum bitirildi", "Session ended"), true)
		return nil
	case "prepare":
		return startHostPrepare()
	case "legacy-cleanup":
		return runJob(L("Eski sürüm temizliği", "Old version cleanup"), func(j *Job) error {
			defer invalidateLegacy()
			return elevate(j, elevReq{Action: "setup", Legacy: true})
		})
	case "set-authkey":
		return hostSetAuthKey(a["key"])
	case "clear-authkey":
		withConfig(func(c *Config) { c.Host.AuthKey = "" })
		return nil
	case "copy-invite":
		inv := hostView().Invite
		if inv == "" {
			return errors.New(L("davet kodu henüz hazır değil", "the invite code is not ready yet"))
		}
		return clipWriteText(inv)
	case "relay":
		withConfig(func(c *Config) { c.Host.RelayOff = a["on"] != "1" })
		return nil
	case "relay-target":
		withConfig(func(c *Config) { c.Host.RelayTarget = a["id"] })
		return nil
	case "discord-ok":
		withConfig(func(c *Config) { c.Host.DiscordOK = a["v"] == "1" })
		return nil
	case "new-secret":
		withConfig(func(c *Config) { c.Host.Secret = randHex(16) })
		logf("%s", L("Yeni davet kodu üretildi; eski kodlarla yeni eşleştirme yapılamaz", "New invite code made; old codes cannot pair any more"))
		return nil
	case "open-keys":
		return openURL("https://login.tailscale.com/admin/settings/keys")
	case "open-panel":
		return openURL(hostView().SunPanel)

	// client
	case "join":
		code := findInvite(a["code"])
		if code == "" {
			if _, err := decodeInvite(a["code"]); err != nil {
				return err
			}
			code = a["code"]
		}
		withConfig(func(c *Config) { c.Mode = "client" })
		return runJob(L("Bağlanılıyor", "Connecting"), func(j *Job) error { return clientJoin(j, code) })
	case "pair":
		return runJob(L("Eşleştirme", "Pairing"), clientPair)
	case "rejoin":
		return runJob(L("Ağa yeniden bağlanma", "Rejoining the network"), clientRejoin)
	case "refresh-apps":
		return runJob(L("Uygulama listesi", "App list"), clientRefreshApps)
	case "health":
		kickHealth()
		return nil
	case "speed":
		return runJob(L("Hat ölçümü", "Line test"), func(j *Job) error { _, err := clientMeasure(j); return err })
	case "stream-settings":
		res := a["res"]
		if !validRes(res) {
			return errors.New(L("geçersiz çözünürlük", "invalid resolution"))
		}
		fps, _ := strconv.Atoi(a["fps"])
		if fps < 0 || fps > 240 {
			return errors.New(L("geçersiz fps", "invalid fps"))
		}
		kbps := 0
		if v := strings.TrimSpace(strings.ReplaceAll(a["mbps"], ",", ".")); v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0.5 || f > 500 {
				return errors.New(L("bitrate 0,5 ile 500 Mbps arasında olmalı", "bitrate must be between 0.5 and 500 Mbps"))
			}
			kbps = int(f * 1000)
		}
		withConfig(func(c *Config) { c.Client.Stream = StreamSettings{Res: res, FPS: fps, BitrateKbps: kbps} })
		return nil
	case "play":
		app := a["app"]
		if app == "" {
			app = "Desktop"
		}
		return runJob(L("Oyun başlatma", "Starting the game"), func(j *Job) error { return clientPlay(j, app) })
	case "cpad-wizard", "cpad-test":
		return clientPadCommand(strings.TrimPrefix(req.Action, "cpad-"), a["guid"])
	case "cpad-skip", "cpad-cancel", "cpad-save", "cpad-test-stop", "cpad-press":
		return clientPadCommand(strings.TrimPrefix(req.Action, "cpad-"), "")
	case "cpad-forget":
		g := a["guid"]
		withConfig(func(c *Config) { delete(c.Client.Mappings, g) })
		return nil
	}
	return fmt.Errorf(L("bilinmeyen işlem: %s", "unknown action: %s"), req.Action)
}
