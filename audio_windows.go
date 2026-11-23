//go:build windows

package main

// Couch co-op audio relay.
//
// While a friend streams, Sunshine moves Windows' default output to a virtual
// speaker (Steam Streaming Speakers) and sends whatever plays there to the friend.
// Discord is pinned to the host's real headphones, so it never reaches the stream.
// The problem: the host player no longer hears the game. This relay fixes that by
// loopback-capturing the virtual speaker and playing it on the host's headphones.
// Nothing is ever captured from the headphones, so Discord cannot leak back.

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

type mmDev struct {
	dev  *wca.IMMDevice
	info AudioDev
}

func activateAudioClient(d *wca.IMMDevice) (*wca.IAudioClient, error) {
	var ac *wca.IAudioClient
	hr, _, _ := syscall.SyscallN(d.VTable().Activate,
		uintptr(unsafe.Pointer(d)),
		uintptr(unsafe.Pointer(wca.IID_IAudioClient)),
		uintptr(wca.CLSCTX_ALL),
		0,
		uintptr(unsafe.Pointer(&ac)))
	if hr != 0 {
		return nil, ole.NewError(hr)
	}
	return ac, nil
}

func deviceName(d *wca.IMMDevice) string {
	var ps *wca.IPropertyStore
	if err := d.OpenPropertyStore(wca.STGM_READ, &ps); err != nil {
		return "?"
	}
	defer ps.Release()
	var pv wca.PROPVARIANT
	if err := ps.GetValue(&wca.PKEY_Device_FriendlyName, &pv); err != nil {
		return "?"
	}
	return pv.String()
}

// enumRender lists active output devices. Caller must release the returned devices.
func enumRender(de *wca.IMMDeviceEnumerator) ([]mmDev, error) {
	defID := ""
	var def *wca.IMMDevice
	if err := de.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &def); err == nil {
		_ = def.GetId(&defID)
		def.Release()
	}
	var col *wca.IMMDeviceCollection
	if err := de.EnumAudioEndpoints(wca.ERender, wca.DEVICE_STATE_ACTIVE, &col); err != nil {
		return nil, err
	}
	defer col.Release()
	var n uint32
	if err := col.GetCount(&n); err != nil {
		return nil, err
	}
	var out []mmDev
	for i := uint32(0); i < n; i++ {
		var d *wca.IMMDevice
		if err := col.Item(i, &d); err != nil {
			continue
		}
		var id string
		_ = d.GetId(&id)
		name := deviceName(d)
		out = append(out, mmDev{dev: d, info: AudioDev{ID: id, Name: name, Default: id == defID, Virtual: isVirtualAudioName(name)}})
	}
	return out, nil
}

func releaseAll(ds []mmDev) {
	for _, d := range ds {
		d.dev.Release()
	}
}

func relayLoop() {
	runtime.LockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		// S_FALSE (already initialised) is reported as an error by go-ole; ignore it.
		logf(L("Ses aktarımı: COM uyarısı: %v", "Audio relay: COM warning: %v"), err)
	}
	var de *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &de); err != nil {
		setRelay(func(s *RelayState) {
			s.Error = fmt.Sprintf(L("ses aygıtlarına ulaşılamadı: %v", "could not reach the audio devices: %v"), err)
		})
		return
	}
	defer de.Release()

	lastErr := ""
	lastRealDefault := ""
	for {
		c := getConfig()
		devs, err := enumRender(de)
		if err != nil {
			setRelay(func(s *RelayState) { s.Running = false; s.Error = err.Error() })
			time.Sleep(5 * time.Second)
			continue
		}
		infos := make([]AudioDev, len(devs))
		for i, d := range devs {
			infos[i] = d.info
		}
		// In "auto" mode remember the last real default device: once a stream starts the
		// default becomes the virtual speaker, and we must keep playing to the headphones.
		for _, d := range infos {
			if d.Default && !d.Virtual {
				lastRealDefault = d.ID
			}
		}
		want := c.Host.RelayTarget
		if want == "" {
			want = lastRealDefault
		}
		src, dst := pickRelayDevices(infos, want)
		setRelay(func(s *RelayState) { s.Devices = infos })

		if c.Mode != "host" || c.Host.RelayOff || src < 0 || dst < 0 {
			reason := ""
			switch {
			case c.Mode == "host" && !c.Host.RelayOff && src < 0:
				reason = L("sanal hoparlör bulunamadı", "no virtual speaker found")
			case c.Mode == "host" && !c.Host.RelayOff && dst < 0:
				reason = L("kulaklık/hoparlör bulunamadı", "no headphones or speakers found")
			}
			setRelay(func(s *RelayState) { s.Running, s.Source, s.Target, s.Error = false, "", "", reason })
			releaseAll(devs)
			time.Sleep(3 * time.Second)
			continue
		}

		setRelay(func(s *RelayState) {
			s.Running, s.Source, s.Target, s.Error = true, devs[src].info.Name, devs[dst].info.Name, ""
		})
		logf(L("Ses aktarımı: %s → %s", "Audio relay: %s → %s"), devs[src].info.Name, devs[dst].info.Name)
		err = runRelay(devs[src].dev, devs[dst].dev, func() bool {
			nc := getConfig()
			return nc.Mode != "host" || nc.Host.RelayOff || nc.Host.RelayTarget != c.Host.RelayTarget
		})
		releaseAll(devs)
		if err != nil {
			msg := err.Error()
			setRelay(func(s *RelayState) { s.Running = false; s.Error = msg })
			if msg != lastErr {
				logf(L("Ses aktarımı durdu: %s", "Audio relay stopped: %s"), msg)
				lastErr = msg
			}
			time.Sleep(3 * time.Second)
		}
	}
}

// runRelay copies audio from src (loopback) to dst until stop() returns true,
// the default device changes away from usable state, or an error occurs.
func runRelay(src, dst *wca.IMMDevice, stop func() bool) error {
	capc, err := activateAudioClient(src)
	if err != nil {
		return fmt.Errorf(L("sanal hoparlör açılamadı: %v", "could not open the virtual speaker: %v"), err)
	}
	defer capc.Release()
	var wfx *wca.WAVEFORMATEX
	if err := capc.GetMixFormat(&wfx); err != nil {
		return fmt.Errorf(L("ses biçimi okunamadı: %v", "could not read the audio format: %v"), err)
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(wfx)))
	if err := capc.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, wca.AUDCLNT_STREAMFLAGS_LOOPBACK, 2_000_000, 0, wfx, nil); err != nil {
		return fmt.Errorf(L("yakalama başlatılamadı: %v", "could not start capturing: %v"), err)
	}
	var cc *wca.IAudioCaptureClient
	if err := capc.GetService(wca.IID_IAudioCaptureClient, &cc); err != nil {
		return err
	}
	defer cc.Release()

	ren, err := activateAudioClient(dst)
	if err != nil {
		return fmt.Errorf(L("kulaklık açılamadı: %v", "could not open the headphones: %v"), err)
	}
	defer ren.Release()
	// AUTOCONVERTPCM lets Windows resample if the headphones use another format.
	flags := uint32(wca.AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM | wca.AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY)
	if err := ren.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, flags, 1_000_000, 0, wfx, nil); err != nil {
		return fmt.Errorf(L("kulaklığa çalma başlatılamadı: %v", "could not start playing to the headphones: %v"), err)
	}
	var rc *wca.IAudioRenderClient
	if err := ren.GetService(wca.IID_IAudioRenderClient, &rc); err != nil {
		return err
	}
	defer rc.Release()
	var renSize uint32
	if err := ren.GetBufferSize(&renSize); err != nil {
		return err
	}

	block := int(wfx.NBlockAlign)
	rate := int(wfx.NSamplesPerSec)
	if block <= 0 || rate <= 0 {
		return errors.New(L("geçersiz ses biçimi", "invalid audio format"))
	}
	maxQueued := rate * 120 / 1000 * block // drop old audio beyond 120 ms
	keepQueued := rate * 40 / 1000 * block

	if err := capc.Start(); err != nil {
		return err
	}
	defer capc.Stop()
	if err := ren.Start(); err != nil {
		return err
	}
	defer ren.Stop()

	var queue []byte
	lastCheck := time.Now()
	for {
		if time.Since(lastCheck) > 500*time.Millisecond {
			lastCheck = time.Now()
			if stop() {
				return nil
			}
		}
		// Drain everything the virtual speaker produced.
		for {
			var next uint32
			if err := cc.GetNextPacketSize(&next); err != nil {
				return fmt.Errorf(L("yakalama hatası: %v", "capture error: %v"), err)
			}
			if next == 0 {
				break
			}
			var data *byte
			var frames, bflags uint32
			var pos, qpc uint64
			if err := cc.GetBuffer(&data, &frames, &bflags, &pos, &qpc); err != nil {
				return fmt.Errorf(L("yakalama hatası: %v", "capture error: %v"), err)
			}
			n := int(frames) * block
			if bflags&wca.AUDCLNT_BUFFERFLAGS_SILENT != 0 || data == nil {
				queue = append(queue, make([]byte, n)...)
			} else {
				queue = append(queue, unsafe.Slice(data, n)...)
			}
			if err := cc.ReleaseBuffer(frames); err != nil {
				return err
			}
		}
		if len(queue) > maxQueued {
			queue = append(queue[:0], queue[len(queue)-keepQueued:]...)
		}
		// Feed the headphones.
		var padding uint32
		if err := ren.GetCurrentPadding(&padding); err != nil {
			return fmt.Errorf(L("çalma hatası: %v", "playback error: %v"), err)
		}
		free := int(renSize - padding)
		frames := len(queue) / block
		if frames > free {
			frames = free
		}
		if frames > 0 {
			var buf *byte
			if err := rc.GetBuffer(uint32(frames), &buf); err != nil {
				return fmt.Errorf(L("çalma hatası: %v", "playback error: %v"), err)
			}
			copy(unsafe.Slice(buf, frames*block), queue[:frames*block])
			if err := rc.ReleaseBuffer(uint32(frames), 0); err != nil {
				return err
			}
			queue = append(queue[:0], queue[frames*block:]...)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
