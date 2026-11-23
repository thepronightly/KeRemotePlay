package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var errNotPaired = errors.New("bu bilgisayar host ile eşleşmemiş")

// mlStartPair opens Moonlight's pairing window with a fixed PIN. It does not wait.
func mlStartPair(hostIP, pin string) (*exec.Cmd, error) {
	exe := moonlightExe()
	if exe == "" {
		return nil, errors.New("Moonlight kurulu değil")
	}
	c := exec.Command(exe, "pair", hostIP, "--pin", pin)
	if err := c.Start(); err != nil {
		return nil, err
	}
	return c, nil
}

// outBuf collects a command's output while it runs.
type outBuf struct {
	mu sync.Mutex
	b  []byte
	at time.Time
}

func (o *outBuf) Write(p []byte) (int, error) {
	o.mu.Lock()
	o.b = append(o.b, p...)
	o.at = time.Now()
	o.mu.Unlock()
	return len(p), nil
}

func (o *outBuf) get() (string, time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return string(o.b), o.at
}

// mlListApps returns the visible app names on a paired host. It fails with
// errNotPaired when Moonlight is not paired with it, or another error when the
// host cannot be reached.
//
// Moonlight often prints the list at once but then keeps running for a long
// time (e.g. while fetching box art), so the answer is taken from the output as
// soon as it is complete and Moonlight is closed.
func mlListApps(hostIP string) ([]string, error) { return mlListAppsWithin(hostIP, 30*time.Second) }

func mlListAppsWithin(hostIP string, limit time.Duration) ([]string, error) {
	exe := moonlightExe()
	if exe == "" {
		return nil, errors.New("Moonlight kurulu değil")
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	c := exec.CommandContext(ctx, exe, "list", hostIP, "--csv")
	hideCmd(c)
	var buf outBuf
	c.Stdout, c.Stderr = &buf, &buf
	if err := c.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	var err error
	for exited := false; !exited; {
		select {
		case err = <-done:
			exited = true
			continue
		case <-time.After(200 * time.Millisecond):
		}
		out, at := buf.get()
		if listNotPaired(out) || len(parseAppCSV(out)) > 0 && time.Since(at) > 800*time.Millisecond {
			_ = c.Process.Kill()
			err = <-done
			exited = true
		}
	}
	out, _ := buf.get()
	if apps := parseAppCSV(out); len(apps) > 0 {
		return apps, nil
	}
	// The message is in the PC's language, and only "not paired" comes this fast:
	// when Moonlight cannot reach the host it stays silent until the time is up.
	msg := strings.TrimSpace(out)
	if listNotPaired(out) || msg != "" && ctx.Err() == nil {
		return nil, errNotPaired
	}
	if ctx.Err() != nil {
		msg = "Moonlight host'a ulaşamadı"
	} else if msg == "" && err != nil {
		msg = err.Error()
	}
	if len(msg) > 200 {
		msg = msg[len(msg)-200:]
	}
	return nil, fmt.Errorf("uygulama listesi alınamadı: %s", msg)
}

func listNotPaired(out string) bool { return strings.Contains(strings.ToLower(out), "not been paired") }

func parseAppCSV(out string) []string {
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "Name, ID") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil
	}
	r := csv.NewReader(strings.NewReader(strings.Join(lines[start:], "\n")))
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	var apps []string
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		if len(rec) == 0 || strings.TrimSpace(rec[0]) == "" {
			continue
		}
		if len(rec) > 4 && strings.TrimSpace(rec[4]) == "true" { // Hidden
			continue
		}
		apps = append(apps, rec[0])
	}
	return apps
}

func mlStream(hostIP, app string, extra []string) error {
	exe := moonlightExe()
	if exe == "" {
		return errors.New("Moonlight kurulu değil")
	}
	args := []string{"stream", hostIP, app}
	args = append(args, extra...)
	c := exec.Command(exe, args...)
	// Controllers mapped with the "Kolu tanıt" wizard; Moonlight also saves them.
	if m := moonlightMappingEnv(); m != "" {
		c.Env = append(os.Environ(), "SDL_GAMECONTROLLERCONFIG="+m)
	}
	setStreaming(true)
	if err := c.Start(); err != nil {
		setStreaming(false)
		return err
	}
	done := make(chan struct{})
	mlMu.Lock()
	mlCmd, mlDone = c, done
	mlMu.Unlock()
	go func() {
		_ = c.Wait()
		setStreaming(false)
		close(done)
		mlMu.Lock()
		if mlCmd == c {
			mlCmd = nil
		}
		mlMu.Unlock()
	}()
	return nil
}

var (
	mlMu   sync.Mutex
	mlCmd  *exec.Cmd // the running stream
	mlDone chan struct{}
)

// mlStop closes the stream the way a user would, so Moonlight also ends the
// session on the host (--quit-after).
func mlStop() {
	mlMu.Lock()
	c, done := mlCmd, mlDone
	mlMu.Unlock()
	if c == nil {
		return
	}
	askToClose(c)
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		_ = c.Process.Kill()
		<-done
	}
}
