package main

import (
	"strings"
	"sync"
	"time"
)

type AudioDev struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Default bool   `json:"default"`
	Virtual bool   `json:"virtual"`
}

// RelayState is what the host UI shows about the couch co-op audio relay.
type RelayState struct {
	Running bool       `json:"running"`
	Source  string     `json:"source,omitempty"`
	Target  string     `json:"target,omitempty"`
	Error   string     `json:"error,omitempty"`
	Devices []AudioDev `json:"devices"`
}

var (
	relayMu   sync.Mutex
	relaySt   RelayState
	relayOnce sync.Once
)

func setRelay(fn func(s *RelayState)) {
	relayMu.Lock()
	fn(&relaySt)
	relayMu.Unlock()
}

func getRelay() RelayState {
	relayMu.Lock()
	defer relayMu.Unlock()
	s := relaySt
	s.Devices = append([]AudioDev(nil), relaySt.Devices...)
	return s
}

func startRelay() { relayOnce.Do(func() { go relayLoop() }) }

func isVirtualAudioName(n string) bool {
	l := strings.ToLower(n)
	return strings.Contains(l, "steam streaming") || strings.Contains(l, "cable input") || strings.Contains(l, "vb-audio")
}

// pickRelayDevices chooses the virtual speaker to copy from and the real
// headphones to play on. A user-chosen target wins; otherwise the current
// default (when it is real), else the first real device. -1 means none.
func pickRelayDevices(devs []AudioDev, wantTarget string) (src, dst int) {
	src, dst = -1, -1
	for i, d := range devs {
		if d.Virtual && strings.Contains(strings.ToLower(d.Name), "steam streaming") {
			src = i
		}
	}
	if src < 0 {
		for i, d := range devs {
			if d.Virtual {
				src = i
				break
			}
		}
	}
	if wantTarget != "" {
		for i, d := range devs {
			if d.ID == wantTarget && !d.Virtual {
				return src, i
			}
		}
	}
	for i, d := range devs {
		if d.Default && !d.Virtual {
			return src, i
		}
	}
	for i, d := range devs {
		if !d.Virtual {
			return src, i
		}
	}
	return src, -1
}

func sleepSeconds(n int) { time.Sleep(time.Duration(n) * time.Second) }
