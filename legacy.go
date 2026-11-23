package main

// Cleanup after the player-order panel of 0.8.x, which was removed. It could
// leave a controller switched off in Windows, and it installed a scheduled task
// plus a program copy under Program Files. Both are undone once, inside the
// next elevated setup.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var legacyPadIDRe = regexp.MustCompile(`(?m)(?:Disable-PnpDevice|kapat:) (\S+) \(`)

// legacyTouchedPads lists controllers the old panel tried to switch off.
func legacyTouchedPads() []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if k := strings.ToUpper(id); id != "" && !seen[k] {
			seen[k] = true
			ids = append(ids, id)
		}
	}
	var st struct {
		Disabled []struct {
			ID string `json:"id"`
		} `json:"disabled"`
	}
	if b, err := os.ReadFile(filepath.Join(dataDir(), "padorder-state.json")); err == nil {
		_ = json.Unmarshal(b, &st)
		for _, p := range st.Disabled {
			add(p.ID)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dataDir(), "padorder.log")); err == nil {
		for _, m := range legacyPadIDRe.FindAllStringSubmatch(string(b), -1) {
			add(m[1])
		}
	}
	return ids
}

// legacyNeedsCleanup: the old task/program copy exists or a controller it
// touched is still switched off.
func legacyNeedsCleanup() bool {
	if legacyTaskPresent() {
		return true
	}
	for _, id := range legacyTouchedPads() {
		if padSwitchedOff(id) {
			return true
		}
	}
	return false
}

// legacyCleanup runs elevated.
func legacyCleanup(logl func(string, ...any)) {
	for _, id := range legacyTouchedPads() {
		if !padSwitchedOff(id) {
			continue
		}
		if err := padSwitchOn(id); err != nil {
			logl("Kapalı kalan kol açılamadı (%v). Aygıt Yöneticisi'nden \"Cihazı etkinleştir\" ya da bilgisayarı yeniden başlat.", err)
		} else {
			logl("Eski sürümün kapalı bıraktığı kol açıldı.")
		}
	}
	if legacyTaskPresent() {
		removeLegacyTask()
		logl("Eski kol sıralama görevi kaldırıldı.")
	}
	for _, f := range []string{"padorder-state.json", "padorder-req.json", "padorder.log"} {
		_ = os.Remove(filepath.Join(dataDir(), f))
	}
}
