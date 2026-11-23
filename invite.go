package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
)

const invitePrefix = "KRP1-"

// Invite is everything a friend needs: how to join the network, where the host
// is, and the shared secret that lets the host's agent approve pairing.
type Invite struct {
	V      int    `json:"v"`
	Key    string `json:"k"`
	IP     string `json:"h"`
	Name   string `json:"n"`
	Secret string `json:"s"`
	Port   int    `json:"p"`
	// Tailnet: the host's Tailscale network, so a friend who already has a
	// profile for it switches to it instead of logging in again.
	Tailnet string `json:"t,omitempty"`
}

func encodeInvite(inv Invite) string {
	inv.V = 1
	b, _ := json.Marshal(inv)
	return invitePrefix + base64.RawURLEncoding.EncodeToString(b)
}

func decodeInvite(s string) (Invite, error) {
	s = strings.Join(strings.Fields(s), "")
	if !strings.HasPrefix(s, invitePrefix) {
		return Invite{}, errors.New("davet kodu KRP1- ile başlamalı")
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, invitePrefix))
	if err != nil {
		return Invite{}, errors.New("davet kodu bozuk (eksik kopyalanmış olabilir)")
	}
	var inv Invite
	if err := json.Unmarshal(b, &inv); err != nil {
		return Invite{}, errors.New("davet kodu okunamadı")
	}
	if inv.V != 1 || inv.Secret == "" {
		return Invite{}, errors.New("davet kodu bu sürümle uyumlu değil")
	}
	if _, err := netip.ParseAddr(inv.IP); err != nil {
		return Invite{}, errors.New("davet kodunda geçerli bir host adresi yok")
	}
	if !strings.HasPrefix(inv.Key, "tskey-") {
		return Invite{}, errors.New("davet kodunda geçerli bir Tailscale anahtarı yok")
	}
	if inv.Port == 0 {
		inv.Port = AgentPort
	}
	return inv, nil
}
