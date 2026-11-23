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
		return Invite{}, errors.New(L("davet kodu KRP1- ile başlamalı", "the invite code must start with KRP1-"))
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, invitePrefix))
	if err != nil {
		return Invite{}, errors.New(L("davet kodu bozuk (eksik kopyalanmış olabilir)", "the invite code is broken (maybe it was not copied in full)"))
	}
	var inv Invite
	if err := json.Unmarshal(b, &inv); err != nil {
		return Invite{}, errors.New(L("davet kodu okunamadı", "could not read the invite code"))
	}
	if inv.V != 1 || inv.Secret == "" {
		return Invite{}, errors.New(L("davet kodu bu sürümle uyumlu değil", "the invite code does not fit this version"))
	}
	if _, err := netip.ParseAddr(inv.IP); err != nil {
		return Invite{}, errors.New(L("davet kodunda geçerli bir host adresi yok", "the invite code has no valid host address"))
	}
	if !strings.HasPrefix(inv.Key, "tskey-") {
		return Invite{}, errors.New(L("davet kodunda geçerli bir Tailscale anahtarı yok", "the invite code has no valid Tailscale key"))
	}
	if inv.Port == 0 {
		inv.Port = AgentPort
	}
	return inv, nil
}
