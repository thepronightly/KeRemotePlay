package main

import "os"

// systemLang returns "tr" when Windows' display language is Turkish, "en"
// otherwise. KRP_LANG overrides it (tests).
func systemLang() string {
	if v := os.Getenv("KRP_LANG"); v != "" {
		return v
	}
	id, _, _ := kernel32.NewProc("GetUserDefaultUILanguage").Call()
	if id&0x3ff == 0x1f { // LANG_TURKISH
		return "tr"
	}
	return "en"
}
