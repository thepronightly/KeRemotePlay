package main

// Two languages, Turkish and English. Every user-visible text is written in
// place as L("Türkçe", "English"), so both versions sit next to each other and
// nothing needs a key catalog.

import (
	"errors"
	"strings"
	"sync/atomic"
)

var uiLang atomic.Value // "tr" | "en"

func lang() string {
	if v, _ := uiLang.Load().(string); v == "en" {
		return "en"
	}
	return "tr"
}

func setLang(l string) { uiLang.Store(normLang(l)) }

func normLang(l string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(l)), "en") {
		return "en"
	}
	return "tr"
}

// L picks the text for the current language.
func L(tr, en string) string { return Lin(lang(), tr, en) }

// Lin picks the text for the given language (e.g. a friend's, sent along with a request).
func Lin(l, tr, en string) string {
	if l == "en" {
		return en
	}
	return tr
}

// lerr is an error that speaks the current language whenever it is shown, so
// package-level errors (compared with errors.Is) follow a language change too.
type lerr struct{ tr, en string }

func (e *lerr) Error() string { return L(e.tr, e.en) }

func newErr(tr, en string) error { return &lerr{tr, en} }

// errIn renders err in the given language when it knows both versions.
func errIn(err error, l string) string {
	var le *lerr
	if errors.As(err, &le) && err == error(le) {
		return Lin(l, le.tr, le.en)
	}
	return err.Error()
}

// initLang sets the language from the saved choice, or from Windows' display
// language the first time (Turkish Windows → Turkish, anything else → English).
func initLang(saved string) {
	if saved != "" {
		setLang(saved)
		return
	}
	if strings.HasPrefix(strings.ToLower(systemLang()), "tr") {
		setLang("tr")
	} else {
		setLang("en")
	}
}
