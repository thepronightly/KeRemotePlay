//go:build windows

package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// sdl wraps the few SDL2 functions we need, loaded from Moonlight's own SDL2.dll so
// controllers are seen exactly as Moonlight sees them.
type sdl struct {
	dll   *syscall.LazyDLL
	procs map[string]*syscall.LazyProc
}

const (
	sdlInitJoystick       = 0x00000200
	sdlInitGameController = 0x00002000
	sdlInitEvents         = 0x00004000
)

func loadSDL() (*sdl, error) {
	exe := moonlightExe()
	if exe == "" {
		return nil, errors.New("Moonlight kurulu değil")
	}
	dir := filepath.Dir(exe)
	// Newer Moonlight builds ship sdl2-compat: its SDL2.dll loads "SDL3.dll" by bare
	// name, which Windows looks for next to *our* exe, not Moonlight's, and fails with
	// "Failed loading SDL3 library". Loading SDL3.dll from Moonlight's folder first
	// makes that bare-name lookup find the already-loaded module.
	if p := filepath.Join(dir, "SDL3.dll"); fileExists(p) {
		if _, err := syscall.LoadLibrary(p); err != nil {
			return nil, fmt.Errorf("Moonlight'ın SDL3.dll dosyası yüklenemedi: %v", err)
		}
	}
	d := syscall.NewLazyDLL(filepath.Join(dir, "SDL2.dll"))
	if err := d.Load(); err != nil {
		return nil, fmt.Errorf("Moonlight'ın SDL2.dll dosyası yüklenemedi: %v", err)
	}
	s := &sdl{dll: d, procs: map[string]*syscall.LazyProc{}}
	for _, n := range []string{"SDL_SetHint", "SDL_Init", "SDL_QuitSubSystem", "SDL_GetError", "SDL_PumpEvents",
		"SDL_NumJoysticks", "SDL_JoystickNameForIndex", "SDL_JoystickGetDeviceGUID", "SDL_IsGameController",
		"SDL_JoystickOpen", "SDL_JoystickClose", "SDL_JoystickUpdate", "SDL_JoystickNumButtons", "SDL_JoystickNumAxes",
		"SDL_JoystickNumHats", "SDL_JoystickGetButton", "SDL_JoystickGetAxis", "SDL_JoystickGetHat",
		"SDL_GameControllerOpen", "SDL_GameControllerClose", "SDL_GameControllerGetButton", "SDL_GameControllerGetAxis",
		"SDL_GameControllerAddMapping", "SDL_RWFromConstMem", "SDL_GameControllerAddMappingsFromRW"} {
		p := d.NewProc(n)
		if err := p.Find(); err != nil {
			return nil, fmt.Errorf("SDL2.dll beklenen fonksiyonu içermiyor: %s", n)
		}
		s.procs[n] = p
	}
	return s, nil
}

func (s *sdl) call(name string, args ...uintptr) uintptr {
	r, _, _ := s.procs[name].Call(args...)
	return r
}

func cstr(v string) uintptr {
	b, _ := syscall.BytePtrFromString(v)
	return uintptr(unsafe.Pointer(b))
}

func gostr(p uintptr) string {
	if p == 0 {
		return ""
	}
	// Reinterpret the C pointer without a uintptr→Pointer conversion (vet-safe).
	base := *(*unsafe.Pointer)(unsafe.Pointer(&p))
	var b []byte
	for i := 0; i < 512; i++ {
		c := *(*byte)(unsafe.Add(base, i))
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}

func (s *sdl) errStr() string { return gostr(s.call("SDL_GetError")) }

func (s *sdl) deviceGUID(i int) (string, [16]byte) {
	var g [16]byte
	// SDL_JoystickGUID (16 bytes) is returned through a hidden pointer on Win64.
	s.call("SDL_JoystickGetDeviceGUID", uintptr(unsafe.Pointer(&g)), uintptr(i))
	return hex.EncodeToString(g[:]), g
}

func (s *sdl) read(j uintptr) snapshot {
	nb := int(int32(s.call("SDL_JoystickNumButtons", j)))
	na := int(int32(s.call("SDL_JoystickNumAxes", j)))
	nh := int(int32(s.call("SDL_JoystickNumHats", j)))
	var sn snapshot
	for i := 0; i < nb && i < 64; i++ {
		sn.Buttons = append(sn.Buttons, s.call("SDL_JoystickGetButton", j, uintptr(i))&0xff != 0)
	}
	for i := 0; i < na && i < 16; i++ {
		sn.Axes = append(sn.Axes, int16(s.call("SDL_JoystickGetAxis", j, uintptr(i))))
	}
	for i := 0; i < nh && i < 4; i++ {
		sn.Hats = append(sn.Hats, uint8(s.call("SDL_JoystickGetHat", j, uintptr(i))))
	}
	return sn
}

// ---- service -------------------------------------------------------------

type padCmd struct {
	op   string // wizard | skip | cancel | save | test | test-stop
	guid string
	resp chan error
}

var (
	cpadOnce   sync.Once
	cpadCmds   = make(chan padCmd, 8)
	cpadTouch  = make(chan struct{}, 1)
	streamMu   sync.Mutex
	streamLive bool
)

func setStreaming(on bool) {
	streamMu.Lock()
	streamLive = on
	streamMu.Unlock()
}

func streaming() bool {
	streamMu.Lock()
	defer streamMu.Unlock()
	return streamLive
}

// touchClientPads keeps the controller scanner running while the client UI is open.
func touchClientPads() {
	cpadOnce.Do(func() { go clientPadLoop() })
	select {
	case cpadTouch <- struct{}{}:
	default:
	}
}

func clientPadCommand(op, guid string) error {
	touchClientPads()
	c := padCmd{op: op, guid: guid, resp: make(chan error, 1)}
	select {
	case cpadCmds <- c:
	case <-time.After(2 * time.Second):
		return errors.New("kol servisi meşgul")
	}
	select {
	case err := <-c.resp:
		return err
	case <-time.After(5 * time.Second):
		return errors.New("kol servisi yanıt vermedi")
	}
}

func clientPadLoop() {
	runtime.LockOSThread()
	var s *sdl
	inited := false
	lastTouch := time.Now()
	var wiz *wizard
	var wizJoy, testGC uintptr
	var testGUID, testName string

	shutdown := func() {
		if wizJoy != 0 {
			s.call("SDL_JoystickClose", wizJoy)
			wizJoy = 0
		}
		if testGC != 0 {
			s.call("SDL_GameControllerClose", testGC)
			testGC = 0
		}
		if inited {
			s.call("SDL_QuitSubSystem", sdlInitGameController|sdlInitJoystick)
			inited = false
		}
		wiz = nil
		setPadsView(func(v *PadsView) { v.Wizard, v.Test, v.Pads = nil, nil, nil })
	}
	start := func() error {
		if s == nil {
			var err error
			if s, err = loadSDL(); err != nil {
				return err
			}
		}
		s.call("SDL_SetHint", cstr("SDL_JOYSTICK_ALLOW_BACKGROUND_EVENTS"), cstr("1"))
		s.call("SDL_SetHint", cstr("SDL_JOYSTICK_THREAD"), cstr("1"))
		if r := int32(s.call("SDL_Init", sdlInitJoystick|sdlInitGameController|sdlInitEvents)); r != 0 {
			return fmt.Errorf("SDL başlatılamadı: %s", s.errStr())
		}
		rw := s.call("SDL_RWFromConstMem", uintptr(unsafe.Pointer(&gameControllerDB[0])), uintptr(len(gameControllerDB)))
		if rw != 0 {
			s.call("SDL_GameControllerAddMappingsFromRW", rw, 1)
		}
		for _, m := range getConfig().Client.Mappings {
			s.call("SDL_GameControllerAddMapping", cstr(m))
		}
		inited = true
		return nil
	}
	findIndex := func(guid string) (int, string) {
		n := int(int32(s.call("SDL_NumJoysticks")))
		for i := 0; i < n; i++ {
			if g, _ := s.deviceGUID(i); g == guid {
				return i, gostr(s.call("SDL_JoystickNameForIndex", uintptr(i)))
			}
		}
		return -1, ""
	}
	scan := func() {
		n := int(int32(s.call("SDL_NumJoysticks")))
		maps := getConfig().Client.Mappings
		var pads []ClientPad
		for i := 0; i < n; i++ {
			g, raw := s.deviceGUID(i)
			_, custom := maps[g]
			rec := s.call("SDL_IsGameController", uintptr(i))&0xff != 0
			pads = append(pads, ClientPad{Index: i, Name: gostr(s.call("SDL_JoystickNameForIndex", uintptr(i))), GUID: g,
				Recognized: rec, Custom: custom && rec, XInput: raw[14] == 'x'})
		}
		setPadsView(func(v *PadsView) { v.Pads = pads })
	}

	for {
		// Leave the controllers to Moonlight while it streams, and stop when nobody looks.
		idle := time.Since(lastTouch) > 15*time.Second
		if streaming() || idle {
			if inited {
				shutdown()
			}
			setPadsView(func(v *PadsView) { v.Paused = streaming() })
			select {
			case <-cpadTouch:
				lastTouch = time.Now()
			case c := <-cpadCmds:
				c.resp <- errors.New("yayın sürerken kollar Moonlight'ta; yayını kapatınca tekrar dene")
			case <-time.After(time.Second):
			}
			continue
		}
		if !inited {
			if err := start(); err != nil {
				setPadsView(func(v *PadsView) { v.Available, v.Error = false, err.Error() })
				select {
				case <-cpadTouch:
					lastTouch = time.Now()
				case c := <-cpadCmds:
					c.resp <- err
				case <-time.After(10 * time.Second):
				}
				continue
			}
			setPadsView(func(v *PadsView) { v.Available, v.Error, v.Paused = true, "", false })
			scan()
		}

		select {
		case <-cpadTouch:
			lastTouch = time.Now()
		case c := <-cpadCmds:
			var err error
			switch c.op {
			case "wizard":
				if wizJoy != 0 {
					s.call("SDL_JoystickClose", wizJoy)
					wizJoy = 0
				}
				idx, name := findIndex(c.guid)
				if idx < 0 {
					err = errors.New("kol bulunamadı (çıkarılmış olabilir)")
					break
				}
				wizJoy = s.call("SDL_JoystickOpen", uintptr(idx))
				if wizJoy == 0 {
					err = fmt.Errorf("kol açılamadı: %s", s.errStr())
					break
				}
				// Let the pad settle, then take its resting state as the baseline.
				for k := 0; k < 15; k++ {
					s.call("SDL_PumpEvents")
					s.call("SDL_JoystickUpdate")
					time.Sleep(20 * time.Millisecond)
				}
				wiz = newWizard(c.guid, name, s.read(wizJoy))
				setPadsView(func(v *PadsView) { v.Wizard = wiz.view() })
			case "skip":
				if wiz != nil {
					wiz.skip()
					setPadsView(func(v *PadsView) { v.Wizard = wiz.view() })
				}
			case "cancel":
				if wizJoy != 0 {
					s.call("SDL_JoystickClose", wizJoy)
					wizJoy = 0
				}
				wiz = nil
				setPadsView(func(v *PadsView) { v.Wizard = nil })
			case "save":
				if wiz == nil {
					err = errors.New("tanıtma açık değil")
					break
				}
				if err = wiz.complete(); err != nil {
					break
				}
				m := wiz.mapping()
				g := wiz.guid
				withConfig(func(cf *Config) {
					if cf.Client.Mappings == nil {
						cf.Client.Mappings = map[string]string{}
					}
					cf.Client.Mappings[g] = m
				})
				s.call("SDL_GameControllerAddMapping", cstr(m))
				logf("Kol tanıtıldı: %s", m)
				if wizJoy != 0 {
					s.call("SDL_JoystickClose", wizJoy)
					wizJoy = 0
				}
				wiz = nil
				setPadsView(func(v *PadsView) { v.Wizard = nil })
				scan()
			case "test":
				if testGC != 0 {
					s.call("SDL_GameControllerClose", testGC)
					testGC = 0
				}
				idx, name := findIndex(c.guid)
				if idx < 0 {
					err = errors.New("kol bulunamadı")
					break
				}
				testGC = s.call("SDL_GameControllerOpen", uintptr(idx))
				if testGC == 0 {
					err = errors.New("Moonlight bu kolu henüz tanımıyor; önce tanıt")
					break
				}
				testGUID, testName = c.guid, name
			case "test-stop":
				if testGC != 0 {
					s.call("SDL_GameControllerClose", testGC)
					testGC = 0
				}
				setPadsView(func(v *PadsView) { v.Test = nil })
			default:
				err = errors.New("bilinmeyen komut")
			}
			c.resp <- err
		case <-time.After(func() time.Duration {
			if wiz != nil || testGC != 0 {
				return 10 * time.Millisecond
			}
			return time.Second
		}()):
		}

		s.call("SDL_PumpEvents")
		s.call("SDL_JoystickUpdate")
		if wiz != nil && wizJoy != 0 {
			wiz.feed(s.read(wizJoy))
			setPadsView(func(v *PadsView) { v.Wizard = wiz.view() })
		} else if testGC != 0 {
			var pressed []string
			for b := 0; b < len(sdlButtonNames); b++ {
				if s.call("SDL_GameControllerGetButton", testGC, uintptr(b))&0xff != 0 {
					pressed = append(pressed, sdlButtonNames[b])
				}
			}
			ax := func(a int) int { return int(int16(s.call("SDL_GameControllerGetAxis", testGC, uintptr(a)))) }
			if ax(4) > 16000 {
				pressed = append(pressed, "LT")
			}
			if ax(5) > 16000 {
				pressed = append(pressed, "RT")
			}
			for _, st := range []struct {
				a   int
				neg string
				pos string
			}{{0, "Sol analog ←", "Sol analog →"}, {1, "Sol analog ↑", "Sol analog ↓"}, {2, "Sağ analog ←", "Sağ analog →"}, {3, "Sağ analog ↑", "Sağ analog ↓"}} {
				if v := ax(st.a); v < -16000 {
					pressed = append(pressed, st.neg)
				} else if v > 16000 {
					pressed = append(pressed, st.pos)
				}
			}
			tg, tn := testGUID, testName
			setPadsView(func(v *PadsView) { v.Test = &TestView{GUID: tg, Name: tn, Pressed: pressed} })
		} else {
			scan()
		}
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
