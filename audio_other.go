//go:build !windows

package main

// Development stub: pretends a virtual speaker and headphones exist.
func relayLoop() {
	devs := []AudioDev{
		{ID: "v", Name: "Steam Streaming Speakers", Virtual: true},
		{ID: "h", Name: "Headphones (USB Headset)", Default: true},
		{ID: "s", Name: "Speakers (Realtek)"},
	}
	for {
		c := getConfig()
		src, dst := pickRelayDevices(devs, c.Host.RelayTarget)
		on := c.Mode == "host" && !c.Host.RelayOff && src >= 0 && dst >= 0
		setRelay(func(s *RelayState) {
			s.Devices, s.Running, s.Error = devs, on, ""
			s.Source, s.Target = "", ""
			if on {
				s.Source, s.Target = devs[src].Name, devs[dst].Name
			}
		})
		sleepSeconds(1)
	}
}
