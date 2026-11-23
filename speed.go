package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Speed test: the host's agent streams incompressible data to the client over
// Tailscale, so we measure the real path the video will take (host upload,
// relay or direct, client download). The result picks resolution/fps/bitrate.

type SpeedResult struct {
	Mbps float64 `json:"mbps"`
	At   int64   `json:"at"`
}

// ---- host side -----------------------------------------------------------

var (
	speedBufOnce sync.Once
	speedBuf     []byte
	speedSlots   = make(chan struct{}, 6)
)

func speedHandler(w http.ResponseWriter, r *http.Request) {
	sec, _ := strconv.Atoi(r.URL.Query().Get("sec"))
	if sec < 1 || sec > 8 {
		sec = 5
	}
	select {
	case speedSlots <- struct{}{}:
		defer func() { <-speedSlots }()
	default:
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	speedBufOnce.Do(func() {
		speedBuf = make([]byte, 256<<10)
		_, _ = rand.Read(speedBuf)
	})
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	deadline := time.Now().Add(time.Duration(sec) * time.Second)
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(deadline.Add(3 * time.Second))
	for time.Now().Before(deadline) {
		if _, err := w.Write(speedBuf[:64<<10]); err != nil {
			return
		}
	}
}

// ---- client side ---------------------------------------------------------

// measureSpeed downloads from the host with a few parallel streams and returns
// Mbps, ignoring the first second (TCP ramp-up).
func measureSpeed(ip string, port int, secret string, dur time.Duration, streams int) (float64, error) {
	var total atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, streams)
	sec := int(math.Ceil(dur.Seconds()))
	start := time.Now()
	for i := 0; i < streams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s/krp/speed?sec=%d", net.JoinHostPort(ip, fmt.Sprint(port)), sec), nil)
			req.Header.Set("X-KRP-Secret", secret)
			cl := &http.Client{Transport: &http.Transport{Proxy: nil, DisableCompression: true}, Timeout: dur + 10*time.Second}
			resp, err := cl.Do(req)
			if err != nil {
				errs <- err
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				errs <- fmt.Errorf(L("host yanıtı: %s", "host replied: %s"), resp.Status)
				return
			}
			buf := make([]byte, 64<<10)
			for {
				n, err := resp.Body.Read(buf)
				total.Add(int64(n))
				if err == io.EOF {
					return
				}
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	warm := time.Second
	if dur <= 2*time.Second {
		warm = dur / 4
	}
	time.Sleep(warm)
	b0, t0 := total.Load(), time.Now()
	wg.Wait()
	b1, t1 := total.Load(), time.Now()
	close(errs)
	elapsed := t1.Sub(t0).Seconds()
	if b1-b0 <= 0 || elapsed <= 0 {
		if err := <-errs; err != nil {
			return 0, err
		}
		return 0, errors.New(L("veri gelmedi", "no data arrived"))
	}
	_ = start
	return float64(b1-b0) * 8 / elapsed / 1e6, nil
}

// ---- quality planning ----------------------------------------------------

// StreamSettings is what the client chose. Zero / "auto" means "decide for me".
type StreamSettings struct {
	Res         string `json:"res"`         // auto | native | 720 | 900 | 1080 | 1440 | 2160
	FPS         int    `json:"fps"`         // 0 = auto
	BitrateKbps int    `json:"bitrateKbps"` // 0 = auto
}

type Plan struct {
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         int    `json:"fps"`
	BitrateKbps int    `json:"bitrateKbps"`
	Label       string `json:"label"`
	AutoRes     bool   `json:"autoRes"`
	AutoFPS     bool   `json:"autoFps"`
	AutoBitrate bool   `json:"autoBitrate"`
	Note        string `json:"note,omitempty"`
}

// moonlightDefaultKbps mirrors Moonlight's StreamingPreferences::getDefaultBitrate.
func moonlightDefaultKbps(w, h, fps int) int {
	ff := float64(fps)
	if fps > 60 {
		ff = math.Sqrt(float64(fps)/60) * 60
	}
	ff /= 30
	table := []struct{ px, f float64 }{
		{640 * 360, 1}, {854 * 480, 2}, {1280 * 720, 5}, {1920 * 1080, 10}, {2560 * 1440, 20}, {3840 * 2160, 40},
	}
	px := float64(w * h)
	rf := table[len(table)-1].f
	for i, e := range table {
		if px <= e.px {
			if i == 0 {
				rf = e.f
			} else {
				p := table[i-1]
				rf = (px-p.px)/(e.px-p.px)*(e.f-p.f) + p.f
			}
			break
		}
	}
	return int(math.Round(rf*ff)) * 1000
}

const (
	budgetShare = 0.75 // leave room for FEC (~20%) and other traffic
	minShare    = 0.45 // below this share of Moonlight's default the picture gets mushy
)

var standardRes = []struct {
	Key  string
	W, H int
}{
	{"2160", 3840, 2160}, {"1440", 2560, 1440}, {"1080", 1920, 1080}, {"900", 1600, 900}, {"720", 1280, 720},
}

var ResOptions = []string{"auto", "native", "720", "900", "1080", "1440", "2160"}
var FPSOptions = []int{0, 30, 60, 90, 120, 144}

func validRes(r string) bool {
	for _, o := range ResOptions {
		if o == r {
			return true
		}
	}
	return false
}

func resLabel(w, h int) string {
	switch {
	case w == 3840 && h == 2160:
		return "4K"
	case w == 1920 && h == 1080, w == 2560 && h == 1440, w == 1280 && h == 720, w == 1600 && h == 900:
		return fmt.Sprintf("%dp", h)
	}
	return fmt.Sprintf("%dx%d", w, h)
}

func roundKbps(k float64) int {
	v := int(k/500) * 500
	if v < 1500 {
		v = 1500
	}
	if v > 150000 {
		v = 150000
	}
	return v
}

// makePlan turns the client's choices, the measured line and the client's display
// into concrete Moonlight settings. Whatever the user fixed is kept as-is; only the
// "auto" parts are chosen, preferring native resolution, then 60 fps, then lower
// resolutions until the line can carry the stream.
func makePlan(s StreamSettings, sp *SpeedResult, dispW, dispH, dispHz int) Plan {
	if dispW <= 0 || dispH <= 0 {
		dispW, dispH, dispHz = 1920, 1080, 60
	}
	if dispW > 3840 || dispH > 2160 {
		dispW, dispH = 3840, 2160
	}
	hasBudget := sp != nil && sp.Mbps > 0
	budget := math.Inf(1)
	if hasBudget {
		budget = sp.Mbps * 1000 * budgetShare
	}

	type wh struct{ w, h int }
	var resC []wh
	autoRes := s.Res == "" || s.Res == "auto"
	switch {
	case s.Res == "native":
		resC = []wh{{dispW, dispH}}
	case !autoRes:
		for _, r := range standardRes {
			if r.Key == s.Res {
				resC = []wh{{r.W, r.H}}
			}
		}
		if resC == nil {
			resC, autoRes = []wh{{dispW, dispH}}, true
		}
	default:
		resC = append(resC, wh{dispW, dispH})
		for _, r := range standardRes {
			if r.H < dispH {
				resC = append(resC, wh{r.W, r.H})
			}
		}
		if !hasBudget && dispH > 1080 {
			// Without a measurement be conservative: no more than 1080p.
			resC = []wh{{1920, 1080}}
		}
	}

	autoFPS := s.FPS <= 0
	var fpsC []int
	if autoFPS {
		if dispHz >= 115 && hasBudget {
			fpsC = append(fpsC, 120)
		}
		fpsC = append(fpsC, 60)
	} else {
		fpsC = []int{s.FPS}
	}

	p := Plan{AutoRes: autoRes, AutoFPS: autoFPS, AutoBitrate: s.BitrateKbps <= 0}
	found := false
	for _, r := range resC {
		for _, f := range fpsC {
			if budget >= float64(moonlightDefaultKbps(r.w, r.h, f))*minShare {
				p.Width, p.Height, p.FPS, found = r.w, r.h, f, true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		last := resC[len(resC)-1]
		p.Width, p.Height, p.FPS = last.w, last.h, fpsC[len(fpsC)-1]
		if autoFPS && autoRes {
			p.FPS = 30
		}
		if autoRes && autoFPS {
			p.Note = L("Hat çok zayıf; görüntü düşük kalitede olacak.", "The line is very weak; the picture will be low quality.")
		} else {
			p.Note = L("Seçtiğin çözünürlük/fps bu hat için fazla; Otomatik daha akıcı olur.", "The resolution/fps you picked is too much for this line; Auto will be smoother.")
		}
	}

	def := float64(moonlightDefaultKbps(p.Width, p.Height, p.FPS))
	if p.AutoBitrate {
		p.BitrateKbps = roundKbps(math.Min(budget, def))
	} else {
		p.BitrateKbps = s.BitrateKbps
		if p.BitrateKbps < 500 {
			p.BitrateKbps = 500
		}
		if p.BitrateKbps > 500000 {
			p.BitrateKbps = 500000
		}
		if hasBudget && float64(p.BitrateKbps) > budget*1.05 {
			p.Note = fmt.Sprintf(L("Seçtiğin bitrate ölçülen hattın (%.1f Mbps) kaldıracağından yüksek; takılma olabilir.", "The bitrate you picked is more than the tested line (%.1f Mbps) can carry; it may stutter."), sp.Mbps)
		}
	}
	p.Label = fmt.Sprintf("%s · %d fps", resLabel(p.Width, p.Height), p.FPS)
	return p
}

func (p Plan) moonlightArgs() []string {
	return []string{"--resolution", fmt.Sprintf("%dx%d", p.Width, p.Height), "--fps", strconv.Itoa(p.FPS), "--bitrate", strconv.Itoa(p.BitrateKbps)}
}

// speedFresh reports whether a measurement is recent enough to reuse.
func speedFresh(s *SpeedResult, maxAge time.Duration) bool {
	return s != nil && s.Mbps > 0 && time.Since(time.Unix(s.At, 0)) < maxAge
}

type DisplayInfo struct {
	W  int `json:"w"`
	H  int `json:"h"`
	Hz int `json:"hz"`
}

// effectiveDisplay is the client's screen, capped to the host's screen: Sunshine
// captures the host monitor as-is (it does not change the host's resolution), so
// streaming above it only wastes bitrate.
func effectiveDisplay(client DisplayInfo, host *DisplayInfo) DisplayInfo {
	d := client
	if host != nil && host.W > 0 && host.H > 0 {
		if host.H < d.H {
			d.W, d.H = host.W, host.H
		}
		if host.Hz > 0 && host.Hz < d.Hz {
			d.Hz = host.Hz
		}
	}
	return d
}
