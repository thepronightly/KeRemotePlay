package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const appVersion = "0.9.7"

// AgentPort is the TCP port the host-side pairing agent listens on (Tailscale only).
const AgentPort = 47900

type HostConfig struct {
	SunUser string `json:"sunUser,omitempty"`
	SunPass string `json:"sunPass,omitempty"`
	AuthKey string `json:"authKey,omitempty"`
	Tailnet string `json:"tailnet,omitempty"` // the Tailscale network AuthKey belongs to
	// Secret: what makes each invite code different. New with every start as host,
	// never stored.
	Secret string `json:"-"`
	// DiscordOK: host confirmed Discord outputs to real headphones, not "Default".
	DiscordOK bool `json:"discordOK,omitempty"`
	// Couch co-op: copy the stream audio to the host's own headphones.
	RelayOff    bool   `json:"relayOff,omitempty"`
	RelayTarget string `json:"relayTarget,omitempty"`
}

type ClientConfig struct {
	// The current host. It all comes from the invite code, which is asked for at
	// every start, so none of it is stored.
	HostIP    string       `json:"-"`
	HostName  string       `json:"-"`
	Secret    string       `json:"-"`
	AgentPort int          `json:"-"`
	AuthKey   string       `json:"-"`
	Tailnet   string       `json:"-"` // host's Tailscale network, to switch back to it
	Paired    bool         `json:"-"`
	Apps      []string     `json:"-"`
	HostDisp  *DisplayInfo `json:"-"`
	Notice    string       `json:"-"` // why the app went back to asking for a code

	Stream StreamSettings `json:"stream"`
	Speed  *SpeedResult   `json:"speed,omitempty"`
	// Mappings: SDL controller mappings made with the controller wizard, by GUID.
	Mappings map[string]string `json:"mappings,omitempty"`
}

type Config struct {
	Mode   string       `json:"-"`              // "host" | "client" | "", chosen at every start
	Lang   string       `json:"lang,omitempty"` // "tr" | "en"; empty = Windows' language
	Host   HostConfig   `json:"host"`
	Client ClientConfig `json:"client"`
}

var (
	cfgMu sync.Mutex
	cfg   Config
)

func dataDir() string {
	if d := os.Getenv("KRP_DATA_DIR"); d != "" {
		return d
	}
	d, err := os.UserConfigDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "KeRemotePlay")
}

func configPath() string { return filepath.Join(dataDir(), "config.json") }

func loadConfig() {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	b, err := os.ReadFile(configPath())
	if err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	if cfg.Host.Secret == "" {
		cfg.Host.Secret = randHex(16)
	}
	if !validRes(cfg.Client.Stream.Res) {
		cfg.Client.Stream.Res = "auto"
	}
	initLang(cfg.Lang)
}

// withConfig runs fn under the config lock and persists the result.
func withConfig(fn func(c *Config)) {
	cfgMu.Lock()
	fn(&cfg)
	snapshot := cfg
	cfgMu.Unlock()
	if err := saveConfig(snapshot); err != nil {
		logf(L("Ayarlar kaydedilemedi: %v", "Could not save settings: %v"), err)
	}
}

func getConfig() Config {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	c := cfg
	c.Client.Apps = append([]string(nil), cfg.Client.Apps...)
	return c
}

func saveConfig(c Config) error {
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := configPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, configPath())
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func randPassword(n int) string {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func randPIN() string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%04d", (int(b[0])<<8|int(b[1]))%10000)
}

// ---- logging -------------------------------------------------------------

var (
	logMu   sync.Mutex
	logRing []string
	logFile *os.File
)

func initLog() {
	_ = os.MkdirAll(dataDir(), 0o700)
	p := filepath.Join(dataDir(), "krp.log")
	if st, err := os.Stat(p); err == nil && st.Size() > 2<<20 {
		_ = os.Rename(p, p+".old")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		logFile = f
	}
	logf("KeRemotePlay %s", appVersion)
}

func logf(format string, a ...any) {
	line := time.Now().Format("15:04:05") + "  " + strings.TrimRight(fmt.Sprintf(format, a...), "\r\n")
	logMu.Lock()
	defer logMu.Unlock()
	logRing = append(logRing, line)
	if len(logRing) > 300 {
		logRing = logRing[len(logRing)-300:]
	}
	if logFile != nil {
		_, _ = logFile.WriteString(time.Now().Format("2006-01-02 ") + line + "\n")
	}
}

func logTail(n int) []string {
	logMu.Lock()
	defer logMu.Unlock()
	if len(logRing) < n {
		n = len(logRing)
	}
	return append([]string(nil), logRing[len(logRing)-n:]...)
}

func maskSecret(s string) string {
	if len(s) <= 14 {
		return strings.Repeat("•", len(s))
	}
	return s[:10] + "…" + s[len(s)-4:]
}
