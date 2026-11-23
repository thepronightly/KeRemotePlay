package main

// Client-side controller support ("any controller works").
//
// Moonlight reads controllers through SDL. A controller is only sent to the host if
// SDL knows its layout (a "mapping"); unknown DirectInput pads are silently ignored.
// KeRemotePlay loads Moonlight's own SDL2.dll, so it sees controllers exactly like
// Moonlight does, shows which ones Moonlight will use, and for unknown ones runs a
// short "press each button" wizard. The resulting mapping is handed to Moonlight via
// SDL_GAMECONTROLLERCONFIG when the stream starts (Moonlight also saves it). On the
// host every friend's pad then appears as an Xbox (XInput) controller.

import (
	_ "embed"
	"fmt"
	"strings"
	"sync"
)

//go:embed assets/gamecontrollerdb_windows.txt
var gameControllerDB []byte

type ClientPad struct {
	Index      int    `json:"index"`
	Name       string `json:"name"`
	GUID       string `json:"guid"`
	Recognized bool   `json:"recognized"` // Moonlight will use it
	Custom     bool   `json:"custom"`     // recognized thanks to a mapping made here
	XInput     bool   `json:"xinput"`
}

// snapshot is the raw state of one joystick as SDL numbers it.
type snapshot struct {
	Buttons []bool
	Axes    []int16
	Hats    []uint8
}

type wizStep struct {
	Key    string `json:"key"`
	Prompt string `json:"prompt"`
	Kind   string `json:"kind"` // button | trigger | stickx | sticky
}

var wizSteps = []wizStep{
	{"a", "Alttaki tuş (Xbox A · PS Çarpı)", "button"},
	{"b", "Sağdaki tuş (Xbox B · PS Daire)", "button"},
	{"x", "Soldaki tuş (Xbox X · PS Kare)", "button"},
	{"y", "Üstteki tuş (Xbox Y · PS Üçgen)", "button"},
	{"leftshoulder", "Sol üst tuş (LB · L1)", "button"},
	{"rightshoulder", "Sağ üst tuş (RB · R1)", "button"},
	{"lefttrigger", "Sol tetik (LT · L2)", "trigger"},
	{"righttrigger", "Sağ tetik (RT · R2)", "trigger"},
	{"back", "Back / Select / Share", "button"},
	{"start", "Start / Options", "button"},
	{"guide", "Ortadaki ana tuş (Xbox / PS / Home) — yoksa Atla", "button"},
	{"leftstick", "Sol analoğa bastır (L3)", "button"},
	{"rightstick", "Sağ analoğa bastır (R3)", "button"},
	{"dpup", "Yön tuşu: YUKARI", "button"},
	{"dpdown", "Yön tuşu: AŞAĞI", "button"},
	{"dpleft", "Yön tuşu: SOL", "button"},
	{"dpright", "Yön tuşu: SAĞ", "button"},
	{"leftx", "Sol analoğu SAĞA it", "stickx"},
	{"lefty", "Sol analoğu AŞAĞI it", "sticky"},
	{"rightx", "Sağ analoğu SAĞA it", "stickx"},
	{"righty", "Sağ analoğu AŞAĞI it", "sticky"},
}

const axisThreshold = 16000

// detectInput compares the current state with the resting baseline and returns an
// SDL binding ("b3", "h0.4", "a2", "+a2", "a1~") for the first clearly moved input.
func detectInput(step wizStep, base, cur snapshot, used map[string]bool) string {
	for i := range cur.Buttons {
		if i < len(base.Buttons) && cur.Buttons[i] && !base.Buttons[i] {
			if b := fmt.Sprintf("b%d", i); !used[b] {
				return b
			}
		}
	}
	for i := range cur.Hats {
		if i < len(base.Hats) && cur.Hats[i] != base.Hats[i] && cur.Hats[i] != 0 {
			v := cur.Hats[i]
			if v == 1 || v == 2 || v == 4 || v == 8 { // ignore diagonals
				if b := fmt.Sprintf("h%d.%d", i, v); !used[b] {
					return b
				}
			}
		}
	}
	for i := range cur.Axes {
		if i >= len(base.Axes) {
			continue
		}
		b0, v := int(base.Axes[i]), int(cur.Axes[i])
		d := v - b0
		if d < axisThreshold && d > -axisThreshold {
			continue
		}
		var bind string
		switch step.Kind {
		case "stickx", "sticky":
			// SDL convention: right and down are positive.
			bind = fmt.Sprintf("a%d", i)
			if d < 0 {
				bind += "~"
			}
		default:
			switch {
			case b0 < -axisThreshold: // rests at minimum: a full-range trigger
				bind = fmt.Sprintf("a%d", i)
			case b0 > axisThreshold: // rests at maximum
				bind = fmt.Sprintf("a%d~", i)
			case d > 0:
				bind = fmt.Sprintf("+a%d", i)
			default:
				bind = fmt.Sprintf("-a%d", i)
			}
		}
		if !used[bind] && !used[axisKey(bind)] {
			return bind
		}
	}
	return ""
}

// axisKey reduces a full-axis binding to "a3" so a stick axis is not reused.
func axisKey(b string) string {
	b = strings.TrimSuffix(strings.TrimLeft(b, "+-"), "~")
	return b
}

func atRest(base, cur snapshot) bool {
	for i := range cur.Buttons {
		if i < len(base.Buttons) && cur.Buttons[i] != base.Buttons[i] {
			return false
		}
	}
	for i := range cur.Hats {
		if i < len(base.Hats) && cur.Hats[i] != base.Hats[i] {
			return false
		}
	}
	for i := range cur.Axes {
		if i < len(base.Axes) {
			d := int(cur.Axes[i]) - int(base.Axes[i])
			if d > axisThreshold/2 || d < -axisThreshold/2 {
				return false
			}
		}
	}
	return true
}

func sanitizeMapName(n string) string {
	n = strings.NewReplacer(",", " ", "\n", " ", "\r", " ").Replace(strings.TrimSpace(n))
	if n == "" {
		n = "Kol"
	}
	return n
}

func buildMapping(guid, name string, binds map[string]string) string {
	var b strings.Builder
	b.WriteString(guid + "," + sanitizeMapName(name) + ",")
	for _, s := range wizSteps {
		if v := binds[s.Key]; v != "" {
			b.WriteString(s.Key + ":" + v + ",")
		}
	}
	b.WriteString("platform:Windows,")
	return b.String()
}

// ---- wizard state (shared by the Windows implementation and the tests) -----

type WizardView struct {
	Active  bool              `json:"active"`
	GUID    string            `json:"guid"`
	Name    string            `json:"name"`
	Step    int               `json:"step"`
	Total   int               `json:"total"`
	Prompt  string            `json:"prompt"`
	Waiting bool              `json:"waiting"` // waiting for the user to let go
	Binds   map[string]string `json:"binds"`
	Done    bool              `json:"done"`
	Error   string            `json:"error,omitempty"`
}

type TestView struct {
	GUID    string   `json:"guid"`
	Name    string   `json:"name"`
	Pressed []string `json:"pressed"`
}

type PadsView struct {
	Available bool        `json:"available"` // SDL from Moonlight could be loaded
	Error     string      `json:"error,omitempty"`
	Pads      []ClientPad `json:"pads"`
	Wizard    *WizardView `json:"wizard,omitempty"`
	Test      *TestView   `json:"test,omitempty"`
	Paused    bool        `json:"paused"` // during a stream KeRemotePlay leaves the pads to Moonlight
}

var (
	cpadMu sync.Mutex
	cpadV  PadsView
)

func setPadsView(fn func(v *PadsView)) {
	cpadMu.Lock()
	fn(&cpadV)
	cpadMu.Unlock()
}

func getPadsView() PadsView {
	cpadMu.Lock()
	defer cpadMu.Unlock()
	v := cpadV
	v.Pads = append([]ClientPad(nil), cpadV.Pads...)
	if cpadV.Wizard != nil {
		w := *cpadV.Wizard
		w.Binds = map[string]string{}
		for k, b := range cpadV.Wizard.Binds {
			w.Binds[k] = b
		}
		v.Wizard = &w
	}
	if cpadV.Test != nil {
		t := *cpadV.Test
		t.Pressed = append([]string(nil), cpadV.Test.Pressed...)
		v.Test = &t
	}
	return v
}

// wizard runs the press-each-button flow over a stream of snapshots.
type wizard struct {
	guid, name string
	base       snapshot
	step       int
	binds      map[string]string
	used       map[string]bool
	waitRest   bool
}

func newWizard(guid, name string, base snapshot) *wizard {
	return &wizard{guid: guid, name: name, base: base, binds: map[string]string{}, used: map[string]bool{}}
}

// feed processes one snapshot; returns true when all steps are done.
func (w *wizard) feed(cur snapshot) bool {
	if w.step >= len(wizSteps) {
		return true
	}
	if w.waitRest {
		if atRest(w.base, cur) {
			w.waitRest = false
		}
		return false
	}
	st := wizSteps[w.step]
	if b := detectInput(st, w.base, cur, w.used); b != "" {
		w.binds[st.Key] = b
		w.used[b] = true
		if strings.HasPrefix(b, "a") { // a full-axis binding reserves the whole axis
			w.used[axisKey(b)] = true
		}
		w.step++
		w.waitRest = true
	}
	return w.step >= len(wizSteps)
}

func (w *wizard) skip() bool {
	if w.step < len(wizSteps) {
		w.step++
	}
	return w.step >= len(wizSteps)
}

func (w *wizard) view() *WizardView {
	v := &WizardView{Active: true, GUID: w.guid, Name: w.name, Step: w.step, Total: len(wizSteps), Waiting: w.waitRest, Binds: w.binds}
	if w.step < len(wizSteps) {
		v.Prompt = wizSteps[w.step].Prompt
	} else {
		v.Done = true
	}
	return v
}

func (w *wizard) mapping() string { return buildMapping(w.guid, w.name, w.binds) }

// wizardComplete: a usable pad needs at least the face buttons and the left stick.
func (w *wizard) complete() error {
	for _, k := range []string{"a", "b", "leftx", "lefty"} {
		if w.binds[k] == "" {
			return fmt.Errorf("en azından A, B ve sol analog tanıtılmalı")
		}
	}
	return nil
}

// moonlightMappingEnv returns the SDL_GAMECONTROLLERCONFIG value for Moonlight.
func moonlightMappingEnv() string {
	m := getConfig().Client.Mappings
	var lines []string
	for _, v := range m {
		lines = append(lines, v)
	}
	return strings.Join(lines, "\n")
}

var sdlButtonNames = []string{"A", "B", "X", "Y", "Back", "Guide", "Start", "L3", "R3", "LB", "RB", "↑", "↓", "←", "→"}
