//go:build !windows

package main

import (
	"errors"
	"sync"
)

// Development stub: one recognized Xbox pad and one unknown DirectInput pad. The
// wizard is driven by scripted snapshots so the UI flow can be tried on Linux.

var (
	stubMu  sync.Mutex
	stubWiz *wizard
	stubN   int
)

func setStreaming(bool) {}
func streaming() bool   { return false }

func stubPads() []ClientPad {
	maps := getConfig().Client.Mappings
	unk := "03000000790000000600000000000000"
	_, custom := maps[unk]
	return []ClientPad{
		{Index: 0, Name: "Xbox Controller", GUID: "030000005e040000120b000000007801", Recognized: true, XInput: true},
		{Index: 1, Name: "USB Gamepad", GUID: unk, Recognized: custom, Custom: custom},
	}
}

func touchClientPads() {
	setPadsView(func(v *PadsView) { v.Available, v.Pads = true, stubPads() })
}

func clientPadCommand(op, guid string) error {
	stubMu.Lock()
	defer stubMu.Unlock()
	switch op {
	case "wizard":
		stubWiz = newWizard(guid, "USB Gamepad", snapshot{Buttons: make([]bool, 16), Axes: make([]int16, 4), Hats: make([]uint8, 1)})
		stubN = 0
	case "skip":
		if stubWiz != nil {
			stubWiz.skip()
		}
	case "press": // test helper: simulate pressing the requested input
		if stubWiz != nil {
			stubN++
			cur := snapshot{Buttons: make([]bool, 16), Axes: make([]int16, 4), Hats: make([]uint8, 1)}
			switch {
			case stubWiz.step < 13:
				cur.Buttons[stubWiz.step] = true
			case stubWiz.step < 17:
				cur.Hats[0] = []uint8{1, 4, 8, 2}[stubWiz.step-13]
			default:
				cur.Axes[stubWiz.step-17] = 32767
			}
			stubWiz.feed(cur)
			stubWiz.feed(snapshot{Buttons: make([]bool, 16), Axes: make([]int16, 4), Hats: make([]uint8, 1)})
		}
	case "cancel":
		stubWiz = nil
	case "save":
		if stubWiz == nil {
			return errors.New("tanıtma açık değil")
		}
		if err := stubWiz.complete(); err != nil {
			return err
		}
		m, g := stubWiz.mapping(), stubWiz.guid
		withConfig(func(cf *Config) {
			if cf.Client.Mappings == nil {
				cf.Client.Mappings = map[string]string{}
			}
			cf.Client.Mappings[g] = m
		})
		stubWiz = nil
	case "test":
		setPadsView(func(v *PadsView) {
			v.Test = &TestView{GUID: guid, Name: "Kol", Pressed: []string{"A", "Sol analog →"}}
		})
		return nil
	case "test-stop":
		setPadsView(func(v *PadsView) { v.Test = nil })
		return nil
	}
	setPadsView(func(v *PadsView) {
		v.Pads = stubPads()
		v.Wizard = nil
		if stubWiz != nil {
			v.Wizard = stubWiz.view()
		}
	})
	return nil
}
