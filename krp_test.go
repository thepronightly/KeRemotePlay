package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInviteRoundTrip(t *testing.T) {
	in := Invite{Key: "tskey-auth-kAbc123-XYZ", IP: "100.101.102.103", Name: "HOST-PC", Secret: "abcd", Port: AgentPort}
	code := encodeInvite(in)
	if !strings.HasPrefix(code, "KRP1-") {
		t.Fatal(code)
	}
	// WhatsApp may wrap or add spaces/newlines.
	messy := " " + code[:20] + "\n" + code[20:] + " \r\n"
	out, err := decodeInvite(messy)
	if err != nil {
		t.Fatal(err)
	}
	if out.Key != in.Key || out.IP != in.IP || out.Secret != in.Secret || out.Port != AgentPort || out.Name != in.Name {
		t.Fatalf("%+v", out)
	}
	for _, bad := range []string{"", "hello", "KRP1-!!!", code[:len(code)-10]} {
		if _, err := decodeInvite(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestParsePing(t *testing.T) {
	derp := "pong from test-pc (100.64.1.2) via DERP(fra) in 61ms\n"
	direct := derp + "pong from test-pc (100.64.1.2, 41641) via 88.230.1.2:41641 in 14ms\n"
	r := parsePing(direct)
	if !r.OK || !r.Direct || r.Ms != 14 || r.Kind != "direct" {
		t.Fatalf("%+v", r)
	}
	r = parsePing(derp + "direct connection not established\n")
	if !r.OK || r.Direct || r.Kind != "derp" || r.Ms != 61 || r.Via != "DERP(fra)" {
		t.Fatalf("%+v", r)
	}
	r = parsePing("pong from x (100.64.1.2) via peer-relay(100.70.1.1:7777:vni:3) in 9.4ms\n")
	if r.Kind != "peer-relay" || r.Ms != 9 {
		t.Fatalf("%+v", r)
	}
	r = parsePing("timeout waiting for ping reply\n")
	if r.OK || r.Error == "" {
		t.Fatalf("%+v", r)
	}
}

func TestParseAppCSV(t *testing.T) {
	out := "Establishing connection to PC...\nLoading app list...\nName, ID, HDR Support, App Collection Game, Hidden, Direct Launch, Boxart URL\n" +
		"\"Desktop\",881448767,false,false,false,false,\"\"\n" +
		"\"Steam Big Picture\",1093255277,false,false,false,false,\"x\"\n" +
		"\"Secret\",1,false,false,true,false,\"\"\n"
	apps := parseAppCSV(out)
	if len(apps) != 2 || apps[0] != "Desktop" || apps[1] != "Steam Big Picture" {
		t.Fatal(apps)
	}
	if parseAppCSV("Computer X has not been paired") != nil {
		t.Fatal("expected nil")
	}
}

func TestParseTSStatus(t *testing.T) {
	js := `{"BackendState":"Running","Self":{"HostName":"test-pc","TailscaleIPs":["fd7a:115c:a1e0::1","100.64.0.1"]},
	"CurrentTailnet":{"Name":"host@example.com"},
	"Peer":{"nodekey:1":{"HostName":"berk-pc","TailscaleIPs":["100.64.0.2"],"Online":true,"CurAddr":"1.2.3.4:41641","Relay":"fra"}}}`
	s, err := parseTSStatus([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Running() || s.SelfIP != "100.64.0.1" || s.Tailnet != "host@example.com" {
		t.Fatalf("%+v", s)
	}
	p, ok := s.Peer("100.64.0.2")
	if !ok || !p.Online || p.HostName != "berk-pc" {
		t.Fatalf("%+v", p)
	}
	if _, err := parseTSStatus([]byte("failed to connect to local tailscaled")); err == nil {
		t.Fatal("expected error")
	}
}

func withSunshine(t *testing.T, h http.HandlerFunc) {
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	old := sunBaseURL
	sunBaseURL = func() string { return srv.URL }
	t.Cleanup(func() { sunBaseURL = old })
}

func checkAuth(w http.ResponseWriter, r *http.Request) bool {
	u, p, ok := r.BasicAuth()
	if !ok || u != "krp" || p != "pw" {
		w.WriteHeader(401)
		return false
	}
	if r.Header.Get("Origin") != "" || r.Header.Get("Referer") != "" {
		w.WriteHeader(400) // would trigger Sunshine's CSRF check
		return false
	}
	return true
}

func TestSubmitPinNewAPI(t *testing.T) {
	start := time.Now()
	var posted atomic.Value
	withSunshine(t, func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/pin":
			list := []map[string]string{{"id": "ffffffffffffffffffffffffffffffff", "name": "other", "address": "100.64.9.9"}}
			if time.Since(start) > 2*time.Second { // request shows up a bit later
				list = append(list, map[string]string{"id": "0123456789abcdef0123456789abcdef", "name": "berk", "address": "::ffff:100.64.0.2"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"pairings": list})
		case r.Method == "POST" && r.URL.Path == "/api/pin":
			if r.Header.Get("Content-Type") != "application/json" {
				w.WriteHeader(400)
				return
			}
			b, _ := io.ReadAll(r.Body)
			posted.Store(string(b))
			_, _ = w.Write([]byte(`{"status":true}`))
		default:
			w.WriteHeader(404)
		}
	})
	if err := sunSubmitPin("krp", "pw", "100.64.0.2", "1234", "berk", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	_ = json.Unmarshal([]byte(posted.Load().(string)), &body)
	if body["pairing_id"] != "0123456789abcdef0123456789abcdef" || body["pin"] != "1234" || body["name"] != "berk" {
		t.Fatal(body)
	}
}

func TestSubmitPinLegacyAPI(t *testing.T) {
	var tries atomic.Int32
	withSunshine(t, func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/pin" {
			if tries.Add(1) < 3 {
				_, _ = w.Write([]byte(`{"status":false}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"true"}`))
			return
		}
		w.WriteHeader(404)
	})
	if err := sunSubmitPin("krp", "pw", "100.64.0.2", "1234", "berk", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if tries.Load() != 3 {
		t.Fatal(tries.Load())
	}
	if err := sunSubmitPin("krp", "WRONG", "100.64.0.2", "1234", "berk", 3*time.Second); err == nil {
		t.Fatal("expected auth error")
	}
}

func TestCheckCreds(t *testing.T) {
	configured := false
	withSunshine(t, func(w http.ResponseWriter, r *http.Request) {
		if !configured {
			http.Redirect(w, r, "/welcome", http.StatusTemporaryRedirect)
			return
		}
		if checkAuth(w, r) {
			_, _ = w.Write([]byte(`{"apps":[]}`))
		}
	})
	if got := sunCheckCreds("", ""); got != "unset" {
		t.Fatal(got)
	}
	configured = true
	if got := sunCheckCreds("krp", "pw"); got != "ok" {
		t.Fatal(got)
	}
	if got := sunCheckCreds("krp", "nope"); got != "bad" {
		t.Fatal(got)
	}
}

func TestAgentAuth(t *testing.T) {
	t.Setenv("KRP_DATA_DIR", t.TempDir())
	loadConfig()
	withConfig(func(c *Config) { c.Mode = "host" })
	t.Cleanup(func() { withConfig(func(c *Config) { c.Mode = "" }) })
	secret := getConfig().Host.Secret
	h := agentHandler()
	do := func(remote, sec string) int {
		req := httptest.NewRequest("GET", "/krp/info", nil)
		req.RemoteAddr = remote
		req.Header.Set("X-KRP-Secret", sec)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := do("192.168.1.5:5000", secret); c != 403 {
		t.Fatalf("LAN address must be refused, got %d", c)
	}
	if c := do("100.64.0.2:5000", "wrong"); c != 401 {
		t.Fatalf("bad secret must be refused, got %d", c)
	}
	if c := do("100.64.0.2:5000", secret); c != 200 {
		t.Fatalf("tailscale peer with secret must pass, got %d", c)
	}
	if c := do("[fd7a:115c:a1e0::5]:5000", secret); c != 200 {
		t.Fatalf("tailscale v6 peer must pass, got %d", c)
	}
}

func TestMoonlightDefaultBitrate(t *testing.T) {
	cases := []struct{ w, h, fps, want int }{
		{1280, 720, 60, 10000}, {1920, 1080, 60, 20000}, {2560, 1440, 60, 40000},
		{3840, 2160, 60, 80000}, {1920, 1080, 120, 28000}, {1920, 1080, 30, 10000},
	}
	for _, c := range cases {
		if got := moonlightDefaultKbps(c.w, c.h, c.fps); got != c.want {
			t.Errorf("%dx%d@%d = %d, want %d", c.w, c.h, c.fps, got, c.want)
		}
	}
}

func TestMakePlan(t *testing.T) {
	auto := StreamSettings{Res: "auto"}
	sp := func(m float64) *SpeedResult { return &SpeedResult{Mbps: m, At: time.Now().Unix()} }
	cases := []struct {
		name     string
		s        StreamSettings
		speed    *SpeedResult
		w, h, hz int
		wantW    int
		wantFPS  int
		wantKbps int
		wantNote bool
	}{
		{"fast line, 144Hz 1440p screen", auto, sp(100), 2560, 1440, 144, 2560, 120, 57000, false},
		{"typical upload", auto, sp(14), 1920, 1080, 60, 1920, 60, 10500, false},
		{"weak upload drops to 720p60", auto, sp(6), 1920, 1080, 60, 1280, 60, 4500, false},
		{"very weak: last resort", auto, sp(2), 1920, 1080, 60, 1280, 30, 1500, true},
		{"4K screen, 40 Mbps", auto, sp(40), 3840, 2160, 60, 2560, 60, 30000, false},
		{"no measurement: conservative 1080p60", auto, nil, 2560, 1440, 144, 1920, 60, 20000, false},
		{"fixed res, auto fps/bitrate", StreamSettings{Res: "1440"}, sp(50), 1920, 1080, 144, 2560, 120, 37500, false},
		{"fixed fps, auto res", StreamSettings{Res: "auto", FPS: 120}, sp(20), 2560, 1440, 144, 1920, 120, 15000, false},
		{"fixed res too big for line", StreamSettings{Res: "2160", FPS: 60}, sp(10), 1920, 1080, 60, 3840, 60, 7500, true},
		{"manual bitrate kept", StreamSettings{Res: "1080", FPS: 60, BitrateKbps: 30000}, sp(100), 1920, 1080, 60, 1920, 60, 30000, false},
		{"manual bitrate above line warns", StreamSettings{Res: "1080", FPS: 60, BitrateKbps: 30000}, sp(10), 1920, 1080, 60, 1920, 60, 30000, true},
		{"native res", StreamSettings{Res: "native", FPS: 60}, sp(100), 1600, 1000, 60, 1600, 60, 0, false},
	}
	for _, c := range cases {
		p := makePlan(c.s, c.speed, c.w, c.h, c.hz)
		if p.Width != c.wantW || p.FPS != c.wantFPS || (c.wantKbps != 0 && p.BitrateKbps != c.wantKbps) || (p.Note != "") != c.wantNote {
			t.Errorf("%s: %+v", c.name, p)
		}
	}
	p := makePlan(StreamSettings{Res: "1440", FPS: 120, BitrateKbps: 57000}, nil, 1920, 1080, 60)
	if got := strings.Join(p.moonlightArgs(), " "); got != "--resolution 2560x1440 --fps 120 --bitrate 57000" {
		t.Error(got)
	}
	if !needsMeasurement(auto) || needsMeasurement(StreamSettings{Res: "1080", FPS: 60, BitrateKbps: 20000}) {
		t.Error("needsMeasurement")
	}
}

func TestSpeedMeasure(t *testing.T) {
	t.Setenv("KRP_DATA_DIR", t.TempDir())
	loadConfig()
	withConfig(func(c *Config) { c.Mode = "host" })
	t.Cleanup(func() { withConfig(func(c *Config) { c.Mode = "" }) })
	srv := httptest.NewServer(agentHandler())
	defer srv.Close()
	host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	var p int
	fmt.Sscan(port, &p)
	mbps, err := measureSpeed(host, p, getConfig().Host.Secret, 2*time.Second, 3)
	if err != nil {
		t.Fatal(err)
	}
	if mbps < 50 {
		t.Fatalf("loopback speed too low: %.1f", mbps)
	}
	t.Logf("loopback: %.0f Mbps", mbps)
	if _, err := measureSpeed(host, p, "wrong", 2*time.Second, 1); err == nil {
		t.Fatal("expected auth failure")
	}
}

func TestPickRelayDevices(t *testing.T) {
	devs := []AudioDev{
		{ID: "hdmi", Name: "LG HDR (NVIDIA High Definition Audio)"},
		{ID: "steam", Name: "Speakers (Steam Streaming Speakers)", Virtual: true, Default: true}, // during a stream
		{ID: "head", Name: "Headset (HyperX Cloud)"},
	}
	src, dst := pickRelayDevices(devs, "")
	if src != 1 || dst != 0 { // default is virtual → first real device
		t.Fatal(src, dst)
	}
	if _, dst = pickRelayDevices(devs, "head"); dst != 2 {
		t.Fatal(dst)
	}
	if _, dst = pickRelayDevices(devs, "steam"); dst == 1 { // never play into the virtual speaker
		t.Fatal("picked virtual as target")
	}
	devs[1].Default, devs[2].Default = false, true
	if _, dst = pickRelayDevices(devs, ""); dst != 2 {
		t.Fatal(dst)
	}
	if src, _ = pickRelayDevices(devs[:1], ""); src != -1 {
		t.Fatal("no virtual speaker expected")
	}
	if !isVirtualAudioName("CABLE Input (VB-Audio Virtual Cable)") || isVirtualAudioName("Realtek Speakers") {
		t.Fatal("isVirtualAudioName")
	}
}

func TestEffectiveDisplay(t *testing.T) {
	c := DisplayInfo{2560, 1440, 165}
	if d := effectiveDisplay(c, nil); d != c {
		t.Fatal(d)
	}
	if d := effectiveDisplay(c, &DisplayInfo{1920, 1080, 60}); d != (DisplayInfo{1920, 1080, 60}) {
		t.Fatal(d)
	}
	if d := effectiveDisplay(DisplayInfo{1920, 1080, 60}, &DisplayInfo{3840, 2160, 144}); d != (DisplayInfo{1920, 1080, 60}) {
		t.Fatal(d)
	}
}

func TestTuneSunshineConf(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/sunshine.conf"
	_ = os.WriteFile(p, []byte("port = 47989\r\n"), 0o644)
	changed, err := tuneSunshineConf(p)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	b, _ := os.ReadFile(p)
	if confValue(string(b), "gamepad_driver") != "vigembus" || confValue(string(b), "gamepad") != "x360" || confValue(string(b), "port") != "47989" {
		t.Fatal(string(b))
	}
	if changed, _ := tuneSunshineConf(p); changed {
		t.Fatal("second run must not change anything")
	}
	// User's own choices are kept; only missing keys are added.
	_ = os.WriteFile(p, []byte("gamepad_driver = all\ngamepad = ds5\n"), 0o644)
	if changed, _ := tuneSunshineConf(p); changed {
		t.Fatal("must respect user's choices")
	}
	if changed, err := tuneSunshineConf(dir + "/missing.conf"); err != nil || !changed {
		t.Fatal(changed, err)
	}
}

func TestConfEditing(t *testing.T) {
	pairs, ok := parseConf("# comment\r\nport = 47989\r\n\r\ngamepad = auto\r\nsunshine_name = Test PC\r\n")
	if !ok || len(pairs) != 3 {
		t.Fatal(pairs, ok)
	}
	if _, ok := parseConf("dd_mode_remapping = {\n  \"mixed\": []\n}\n"); ok {
		t.Fatal("multi-line values must be rejected")
	}
	out := setConfPairs(pairs, []confPair{{"gamepad", "x360"}, {"gamepad_driver", "vigembus"}, {"port", ""}})
	got := map[string]string{}
	for _, p := range out {
		got[p.Key] = p.Value
	}
	if got["gamepad"] != "x360" || got["gamepad_driver"] != "vigembus" || got["sunshine_name"] != "Test PC" || len(got) != 3 {
		t.Fatal(got)
	}
	// File fallback keeps comments and unknown lines.
	p := t.TempDir() + "/s.conf"
	_ = os.WriteFile(p, []byte("# keep me\nweird line\ngamepad = auto\n"), 0o644)
	if err := setConfFile(p, []confPair{{"gamepad", "x360"}, {"gamepad_driver", "vigembus"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "# keep me") || !strings.Contains(string(b), "weird line") || confValue(string(b), "gamepad") != "x360" || confValue(string(b), "gamepad_driver") != "vigembus" {
		t.Fatal(string(b))
	}
}

func TestSunSetConfAPI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KRP_SUNSHINE_CONF", dir+"/sunshine.conf")
	_ = os.WriteFile(dir+"/sunshine.conf", []byte("sunshine_name = Test PC\ngamepad = auto\n"), 0o644)
	var saved map[string]string
	restarted := false
	withSunshine(t, func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		switch r.URL.Path {
		case "/api/config":
			_ = json.NewDecoder(r.Body).Decode(&saved)
			_, _ = w.Write([]byte(`{"status":true}`))
		case "/api/restart":
			restarted = true
		case "/api/apps":
			_, _ = w.Write([]byte(`{"apps":[]}`))
		}
	})
	j := &Job{Name: "t"}
	if err := sunSetConf(j, "krp", "pw", []confPair{{"gamepad", "x360"}}); err != nil {
		t.Fatal(err)
	}
	if saved["gamepad"] != "x360" || saved["sunshine_name"] != "Test PC" || !restarted {
		t.Fatal(saved, restarted)
	}
}

func TestCleanPSErr(t *testing.T) {
	in := `#< CLIXML
<Objs Version="1.1.0.1" xmlns="http://schemas.microsoft.com/powershell/2004/04"><Obj S="progress" RefId="0"><TN RefId="0"><T>System.Management.Automation.PSCustomObject</T></TN><MS><PR N="Record"><AV>Preparing modules for first use.</AV></PR></MS></Obj><S S="Error">Disable-PnpDevice : Generic failure _x000D__x000A_</S><S S="Error">At line:1 char:1_x000D__x000A_</S><S S="Error">+ Disable-PnpDevice -InstanceId 'USB\VID_045E&amp;PID_028E\01' ..._x000D__x000A_</S><S S="Error">+ ~~~~~~~~_x000D__x000A_</S></Objs>`
	got := cleanPSErr(in)
	if !strings.HasPrefix(got, "Disable-PnpDevice : Generic failure") || strings.Contains(got, "Preparing") || !strings.Contains(got, "&PID_028E") {
		t.Fatal(got)
	}
	if cleanPSErr(" plain ") != "plain" {
		t.Fatal("plain")
	}
}

func TestDetectInput(t *testing.T) {
	base := snapshot{Buttons: make([]bool, 4), Axes: []int16{0, 0, -32768, 0}, Hats: []uint8{0}}
	used := map[string]bool{}
	cur := snapshot{Buttons: []bool{false, false, true, false}, Axes: []int16{0, 0, -32768, 0}, Hats: []uint8{0}}
	if b := detectInput(wizSteps[0], base, cur, used); b != "b2" {
		t.Fatal(b)
	}
	cur = snapshot{Buttons: make([]bool, 4), Axes: []int16{0, 0, -32768, 0}, Hats: []uint8{4}}
	if b := detectInput(wizSteps[14], base, cur, used); b != "h0.4" {
		t.Fatal(b)
	}
	stick := wizStep{Key: "leftx", Kind: "stickx"}
	cur = snapshot{Buttons: make([]bool, 4), Axes: []int16{-30000, 0, -32768, 0}, Hats: []uint8{0}}
	if b := detectInput(stick, base, cur, used); b != "a0~" { // pushed right but axis went negative → inverted
		t.Fatal(b)
	}
	trig := wizStep{Key: "lefttrigger", Kind: "trigger"}
	cur = snapshot{Buttons: make([]bool, 4), Axes: []int16{0, 0, 32767, 0}, Hats: []uint8{0}}
	if b := detectInput(trig, base, cur, used); b != "a2" { // rests at minimum → full-range trigger
		t.Fatal(b)
	}
	cur = snapshot{Buttons: make([]bool, 4), Axes: []int16{0, 0, -32768, -32000}, Hats: []uint8{0}}
	if b := detectInput(trig, base, cur, used); b != "-a3" { // shared Z axis, other half
		t.Fatal(b)
	}
	used["b2"] = true
	cur = snapshot{Buttons: []bool{false, false, true, false}, Axes: []int16{0, 0, -32768, 0}, Hats: []uint8{0}}
	if b := detectInput(wizSteps[1], base, cur, used); b != "" {
		t.Fatal("already used input must be ignored:", b)
	}
}

func TestWizardFlow(t *testing.T) {
	rest := func() snapshot {
		return snapshot{Buttons: make([]bool, 14), Axes: make([]int16, 4), Hats: make([]uint8, 1)}
	}
	w := newWizard("03000000790000000600000000000000", "USB, Gamepad", rest())
	press := func(f func(s *snapshot)) {
		s := rest()
		f(&s)
		w.feed(s)
		w.feed(s) // holding does not advance twice
		w.feed(rest())
	}
	for i := 0; i < 13; i++ { // a..rightstick as buttons, guide skipped
		if wizSteps[w.step].Key == "guide" {
			w.skip()
			continue
		}
		n := i
		press(func(s *snapshot) { s.Buttons[n] = true })
	}
	for _, v := range []uint8{1, 4, 8, 2} {
		vv := v
		press(func(s *snapshot) { s.Hats[0] = vv })
	}
	press(func(s *snapshot) { s.Axes[0] = 32767 })
	press(func(s *snapshot) { s.Axes[1] = 32767 })
	press(func(s *snapshot) { s.Axes[2] = 32767 })
	press(func(s *snapshot) { s.Axes[3] = 32767 })
	if !w.view().Done {
		t.Fatalf("not done at step %d (%s)", w.step, w.view().Prompt)
	}
	if err := w.complete(); err != nil {
		t.Fatal(err)
	}
	m := w.mapping()
	for _, want := range []string{"03000000790000000600000000000000,USB  Gamepad,", "a:b0,", "dpup:h0.1,", "dpright:h0.2,", "leftx:a0,", "righty:a3,", "platform:Windows,"} {
		if !strings.Contains(m, want) {
			t.Fatalf("mapping %q missing %q", m, want)
		}
	}
	if strings.Contains(m, "guide:") {
		t.Fatal("skipped guide must not be mapped")
	}
	if len(gameControllerDB) < 1000 || !strings.Contains(string(gameControllerDB), "platform:Windows") {
		t.Fatal("embedded controller database missing")
	}
}

func TestFindInviteAndKey(t *testing.T) {
	code := encodeInvite(Invite{Key: "tskey-auth-abc123456789", IP: "100.64.0.1", Name: "PC", Secret: "s", Port: AgentPort})
	msg := "Selam, kod bu: " + code + " hadi gir"
	if got := findInvite(msg); got != code {
		t.Fatalf("got %q", got)
	}
	if findInvite("KRP1-bozuk!") != "" || findInvite("merhaba") != "" {
		t.Fatal("garbage accepted")
	}
	if k := findAuthKey("  \"tskey-auth-kAbc123CNTRL-xyz987654321\"\n"); k != "tskey-auth-kAbc123CNTRL-xyz987654321" {
		t.Fatal(k)
	}
	if findAuthKey("tskey-api-abcdef0123456789") != "" || findAuthKey("") != "" {
		t.Fatal("non-auth key accepted")
	}
}

func TestLegacyTouchedPads(t *testing.T) {
	t.Setenv("KRP_DATA_DIR", t.TempDir())
	_ = os.MkdirAll(dataDir(), 0o700)
	bt := `HID\{00001812-0000-1000-8000-00805F9B34FB}&DEV&VID_045E&PID_0B13&IG_00\A&36A71BAB&0&0000`
	log := "2026-10-26 01:39:54  Disable-PnpDevice " + bt + " (HID uyumlu oyun denetleyicisi): HATA exit status 1\n" +
		"2026-10-26 01:40:01  Disable-PnpDevice USB\\VID_045E&PID_028E\\01 (Xbox 360): HATA\n" +
		"2026-10-26 01:41:00  kapat: " + bt + " (Xbox Wireless Controller)\n"
	_ = os.WriteFile(filepath.Join(dataDir(), "padorder.log"), []byte(log), 0o600)
	ids := legacyTouchedPads()
	if len(ids) != 2 || ids[0] != bt || ids[1] != `USB\VID_045E&PID_028E\01` {
		t.Fatalf("%q", ids)
	}
	legacyCleanup(func(string, ...any) {})
	if len(legacyTouchedPads()) != 0 {
		t.Fatal("log not removed after cleanup")
	}
}

func TestElevReqEmpty(t *testing.T) {
	if !(elevReq{Action: "setup"}).empty() || (elevReq{Install: []string{"moonlight"}}).empty() || (elevReq{Legacy: true}).empty() {
		t.Fatal("empty()")
	}
}

func TestParseTSProfiles(t *testing.T) {
	out := "ID    Tailnet                  Account\r\n9747  me@gmail.com             me@gmail.com\r\n6458  friend@gmail.com         KeRemotePlay*\r\n"
	ps := parseTSProfiles(out)
	if len(ps) != 2 || ps[0].ID != "9747" || ps[0].Current || ps[1].Tailnet != "friend@gmail.com" || !ps[1].Current {
		t.Fatalf("%+v", ps)
	}
	if parseTSProfiles("") != nil {
		t.Fatal("expected none")
	}
}

func TestInviteTailnet(t *testing.T) {
	code := encodeInvite(Invite{Key: "tskey-auth-abc123456789", IP: "100.64.0.1", Name: "PC", Secret: "s", Port: AgentPort, Tailnet: "friend@gmail.com"})
	inv, err := decodeInvite(code)
	if err != nil || inv.Tailnet != "friend@gmail.com" {
		t.Fatal(inv, err)
	}
}

// Moonlight prints the app list and then may keep running for a long time; the
// list must be taken as soon as it is complete.
func TestListAppsDoesNotWaitForMoonlight(t *testing.T) {
	if isWindows {
		t.Skip("uses a shell script as a stand-in for Moonlight")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$2\" = 100.64.0.9 ]; then echo 'Computer friend has not been paired. Please open Moonlight to pair before retrieving games list.' >&2; sleep 30; exit 1; fi\n" +
		"echo 'Name, ID, HDR Support, App Collection Game, Hidden, Direct Launch, Boxart URL'\n" +
		"echo '\"Desktop\",881448767,true,false,false,false,\"qrc:/res/no_app_image.png\"'\n" +
		"sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "moonlight"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	apps, err := mlListApps("100.64.0.1")
	if err != nil || len(apps) != 1 || apps[0] != "Desktop" {
		t.Fatal(apps, err)
	}
	if _, err := mlListApps("100.64.0.8"); !errors.Is(err, errNotPaired) {
		t.Fatal("Moonlight answers in the PC's language: ", err)
	}
	if _, err := mlListApps("100.64.0.9"); !errors.Is(err, errNotPaired) {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 8*time.Second {
		t.Fatalf("took %v", d)
	}
}

func hostMode(t *testing.T) {
	t.Helper()
	t.Setenv("KRP_DATA_DIR", t.TempDir())
	loadConfig()
	withConfig(func(c *Config) { c.Mode, c.Host.SunUser, c.Host.SunPass = "host", "krp", "pw" })
	connReset()
	connMu.Lock()
	connBlocked = map[string]time.Time{}
	connMu.Unlock()
	t.Cleanup(func() { withConfig(func(c *Config) { c.Mode = "" }) })
}

// The code and the role must not survive a restart.
func TestSessionNotStored(t *testing.T) {
	t.Setenv("KRP_DATA_DIR", t.TempDir())
	loadConfig()
	withConfig(func(c *Config) {
		c.Mode = "client"
		c.Host.AuthKey, c.Host.Secret = "tskey-auth-keep", "hostsecret"
		c.Client.HostIP, c.Client.Secret, c.Client.AuthKey = "100.64.0.1", "clientsecret", "tskey-auth-drop"
		c.Client.Paired = true
	})
	b, err := os.ReadFile(configPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"hostsecret", "clientsecret", "tskey-auth-drop", "100.64.0.1", "mode"} {
		if strings.Contains(string(b), s) {
			t.Errorf("%q must not be stored: %s", s, b)
		}
	}
	if !strings.Contains(string(b), "tskey-auth-keep") {
		t.Error("the host's auth key must be kept")
	}
	cfgMu.Lock()
	cfg = Config{}
	cfgMu.Unlock()
	loadConfig()
	if c := getConfig(); c.Mode != "" || c.Client.HostIP != "" || c.Host.Secret == "" || c.Host.Secret == "hostsecret" {
		t.Fatalf("%+v", c)
	}
}

func agentPost(h http.Handler, path, secret, body string) (*httptest.ResponseRecorder, map[string]any) {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.RemoteAddr = "100.64.0.2:5000"
	req.Header.Set("X-KRP-Secret", secret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func TestConnApproval(t *testing.T) {
	hostMode(t)
	secret := getConfig().Host.Secret
	h := agentHandler()
	if _, r := agentPost(h, "/krp/pair", secret, `{"pin":"1234","name":"x"}`); r["ok"] != false {
		t.Fatal("pairing without approval must be refused", r)
	}
	ask := func(yes bool) map[string]any {
		done := make(chan map[string]any, 1)
		go func() {
			_, r := agentPost(h, "/krp/request", secret, `{"name":"test-pc"}`)
			done <- r
		}()
		var req *ConnReq
		if !waitFor(func() bool { req, _ = connView(); return req != nil }, 2*time.Second) {
			t.Fatal("no request shown")
		}
		if req.Name != "test-pc" {
			t.Fatalf("%+v", req)
		}
		connAnswer(req.ID, yes)
		return <-done
	}
	if r := ask(false); r["ok"] != false || !strings.Contains(fmt.Sprint(r["error"]), "reddetti") {
		t.Fatal(r)
	}
	// A refused friend cannot keep popping the question up.
	if _, r := agentPost(h, "/krp/request", secret, `{"name":"test-pc"}`); r["ok"] != false {
		t.Fatal(r)
	}
	if req, _ := connView(); req != nil {
		t.Fatal("no question while blocked")
	}
	connMu.Lock()
	connBlocked = map[string]time.Time{}
	connMu.Unlock()
	if r := ask(true); r["ok"] != true {
		t.Fatal(r)
	}
	if !connApproved("100.64.0.2") {
		t.Fatal("not approved")
	}
	if _, r := agentPost(h, "/krp/request", secret, `{"name":"test-pc"}`); r["ok"] != true {
		t.Fatal("an approved friend is not asked about twice", r)
	}
}

func TestConnAskGivesUp(t *testing.T) {
	hostMode(t)
	if err := connAsk(context.Background(), "100.64.0.3", "x", 20*time.Millisecond); err == nil {
		t.Fatal("expected no answer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- connAsk(ctx, "100.64.0.3", "x", time.Minute) }()
	waitFor(func() bool { r, _ := connView(); return r != nil }, time.Second)
	cancel() // the friend closed the app: the question must disappear
	if err := <-done; err == nil {
		t.Fatal("expected an error")
	}
	if r, _ := connView(); r != nil {
		t.Fatal(r)
	}
}

func TestAgentOnlyWhileHosting(t *testing.T) {
	hostMode(t)
	secret := getConfig().Host.Secret
	h := agentHandler()
	withConfig(func(c *Config) { c.Mode = "" })
	if rec, _ := agentPost(h, "/krp/request", secret, `{}`); rec.Code != 503 {
		t.Fatal(rec.Code)
	}
}

func TestEndSession(t *testing.T) {
	hostMode(t)
	var closed, unpaired atomic.Int32
	var game atomic.Int32
	game.Store(881448767)
	withSunshine(t, func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		switch r.URL.Path {
		case "/api/apps/close":
			closed.Add(1)
			game.Store(0)
		case "/api/clients/unpair-all":
			unpaired.Add(1)
		}
		_, _ = w.Write([]byte(`{"status":true}`))
	})
	info := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<root status_code="200"><currentgame>%d</currentgame><state>x</state></root>`, game.Load())
	}))
	t.Cleanup(info.Close)
	old := sunInfoURL
	sunInfoURL = func() string { return info.URL }
	t.Cleanup(func() { sunInfoURL = old })

	sessionTick()
	if !sessionActive() {
		t.Fatal("session not noticed")
	}
	connMu.Lock()
	connAllowed["100.64.0.2"] = "test-pc"
	connMu.Unlock()
	code := getConfig().Host.Secret
	hostEndSession("Oturum Ctrl+F4 ile bitirildi", true)
	if closed.Load() != 1 || unpaired.Load() != 1 {
		t.Fatal(closed.Load(), unpaired.Load())
	}
	if sessionActive() || connApproved("100.64.0.2") {
		t.Fatal("still connected")
	}
	if getConfig().Host.Secret == code {
		t.Fatal("a kicked friend's code must stop working")
	}
	// A session that ends by itself takes the approval back, code stays.
	game.Store(5)
	sessionTick()
	connMu.Lock()
	connAllowed["100.64.0.2"] = "test-pc"
	connMu.Unlock()
	code = getConfig().Host.Secret
	game.Store(0)
	sessionTick()
	if unpaired.Load() != 2 || connApproved("100.64.0.2") || getConfig().Host.Secret != code {
		t.Fatal(unpaired.Load())
	}
}

func TestCleanName(t *testing.T) {
	if s := cleanName(" test\x00-pc\n"); s != "test -pc" {
		t.Fatalf("%q", s)
	}
	if cleanName("\n") != "KeRemotePlay" || len([]rune(cleanName(strings.Repeat("ş", 99)))) != 40 {
		t.Fatal("name")
	}
}

func TestAddServiceACE(t *testing.T) {
	const sid = "S-1-5-21-1-2-3-1001"
	base := "D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCLCSWLOCRRC;;;IU)"
	want := base + "(A;;CCLCSWRPWPLOCRRC;;;" + sid + ")"
	if got := addServiceACE(base+"\r\n", sid); got != want {
		t.Fatal(got)
	}
	if got := addServiceACE(want, sid); got != want {
		t.Fatal("added twice:", got)
	}
	audit := "S:(AU;FA;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;WD)"
	if got := addServiceACE(base+audit, sid); got != want+audit {
		t.Fatal("must stay in the permissions part:", got)
	}
	if got := addServiceACE(base, ""); got != base {
		t.Fatal(got)
	}
}

func TestServiceNeedsSetup(t *testing.T) {
	ready := SunService{Exists: true, Manual: true, CanControl: true}
	if ready.needsFix() || (SunService{}).needsFix() {
		t.Fatal("a stopped service is normal now")
	}
	if !(SunService{Exists: true, Running: true}).needsFix() || !(SunService{Exists: true, Manual: true}).needsFix() {
		t.Fatal("setup still needed")
	}
}

func TestAgentPortClosesAndReopens(t *testing.T) {
	t.Setenv("KRP_DATA_DIR", t.TempDir())
	loadConfig()
	open := func() bool { return tcpOpen("127.0.0.1", AgentPort) }
	if open() {
		t.Skip("port in use")
	}
	startAgent()
	if !waitFor(open, 3*time.Second) {
		t.Fatal("not listening")
	}
	stopAgent()
	if !waitFor(func() bool { return !open() }, 3*time.Second) {
		t.Fatal("still listening")
	}
	startAgent() // a second visit to the host screen
	if !waitFor(open, 3*time.Second) {
		t.Fatal("did not come back")
	}
	stopAgent()
}
