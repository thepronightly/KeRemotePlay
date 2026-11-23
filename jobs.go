package main

import (
	"fmt"
	"sync"
	"time"
)

// A Job is one long-running user action (install, join, pair...). Only one runs
// at a time; the UI polls its progress.
type Job struct {
	mu      sync.Mutex
	ID      int      `json:"id"`
	Name    string   `json:"name"`
	Running bool     `json:"running"`
	OK      bool     `json:"ok"`
	Err     string   `json:"error,omitempty"`
	Lines   []string `json:"lines"`
	Started int64    `json:"started"`
}

func (j *Job) Logf(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	logf("[%s] %s", j.Name, line)
	j.mu.Lock()
	j.Lines = append(j.Lines, line)
	if len(j.Lines) > 40 {
		j.Lines = j.Lines[len(j.Lines)-40:]
	}
	j.mu.Unlock()
}

type JobView struct {
	ID      int      `json:"id"`
	Name    string   `json:"name"`
	Running bool     `json:"running"`
	OK      bool     `json:"ok"`
	Err     string   `json:"error,omitempty"`
	Lines   []string `json:"lines"`
}

func (j *Job) view() *JobView {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return &JobView{ID: j.ID, Name: j.Name, Running: j.Running, OK: j.OK, Err: j.Err, Lines: append([]string(nil), j.Lines...)}
}

var (
	jobMu  sync.Mutex
	curJob *Job
	jobSeq int
)

var errBusy = newErr("başka bir işlem sürüyor, bitmesini bekle", "something else is running, wait for it to finish")

func runJob(name string, fn func(j *Job) error) error {
	jobMu.Lock()
	if curJob != nil && curJob.Running {
		jobMu.Unlock()
		return errBusy
	}
	jobSeq++
	j := &Job{ID: jobSeq, Name: name, Running: true, Started: time.Now().Unix()}
	curJob = j
	jobMu.Unlock()
	logf(L("İşlem başladı: %s", "Started: %s"), name)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				j.mu.Lock()
				j.Running, j.OK, j.Err = false, false, fmt.Sprintf(L("beklenmeyen hata: %v", "unexpected error: %v"), r)
				j.mu.Unlock()
				logf("PANIC in %s: %v", name, r)
			}
		}()
		err := fn(j)
		tsInvalidate()
		j.mu.Lock()
		j.Running = false
		if err != nil {
			j.Err = err.Error()
		} else {
			j.OK = true
		}
		j.mu.Unlock()
		if err != nil {
			logf(L("İşlem başarısız: %s: %v", "Failed: %s: %v"), name, err)
		} else {
			logf(L("İşlem tamam: %s", "Done: %s"), name)
		}
	}()
	return nil
}

func currentJob() *JobView {
	jobMu.Lock()
	j := curJob
	jobMu.Unlock()
	return j.view()
}

func dismissJob() {
	jobMu.Lock()
	if curJob != nil && !curJob.Running {
		curJob = nil
	}
	jobMu.Unlock()
}

// ---- network info cache --------------------------------------------------

type NetInfo struct {
	Known   bool   `json:"known"`
	Adapter string `json:"adapter,omitempty"`
	WiFi    bool   `json:"wifi"`
	Speed   string `json:"speed,omitempty"`
}

var (
	netMu sync.Mutex
	netV  NetInfo
	netAt time.Time
	netIn bool
)

// netInfoCached never blocks: it returns the last value and refreshes in the background.
func netInfoCached() NetInfo {
	netMu.Lock()
	defer netMu.Unlock()
	if time.Since(netAt) > 60*time.Second && !netIn {
		netIn = true
		go func() {
			v := detectNetwork()
			netMu.Lock()
			netV, netAt, netIn = v, time.Now(), false
			netMu.Unlock()
		}()
	}
	return netV
}

// ---- audio info cache ----------------------------------------------------

type AudioInfo struct {
	Known       bool   `json:"known"`
	VirtualSink string `json:"virtualSink,omitempty"`
	Steam       bool   `json:"steam"`
}

var (
	audMu sync.Mutex
	audV  AudioInfo
	audAt time.Time
	audIn bool
)

func audioInfoCached() AudioInfo {
	audMu.Lock()
	defer audMu.Unlock()
	if time.Since(audAt) > 60*time.Second && !audIn {
		audIn = true
		go func() {
			v := detectAudio()
			audMu.Lock()
			audV, audAt, audIn = v, time.Now(), false
			audMu.Unlock()
		}()
	}
	return audV
}
