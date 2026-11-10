package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/Laky-64/appleservices/internal/control"
	"github.com/Laky-64/appleservices/internal/memguard"
)

// The memory guard keeps the dashboard and its account workers inside the
// budget the user set in the panel. It never stops the dashboard itself: under
// pressure it stops the least-recently-active idle worker through the normal
// stop request, marks it "paused: memory", and resumes it from its saved
// session once memory is back above the thresholds with hysteresis.

const memorySettingsFile = "memory-guard.json"

// goMemoryLimitFraction of max_app_mb is given to the dashboard process as its
// Go soft memory limit (debug.SetMemoryLimit), with a floor of 64 MiB.
const goMemoryLimitFraction = 0.25

type memoryState struct {
	once     sync.Once
	guard    *memguard.Guard
	reader   memguard.Reader
	mu       sync.Mutex
	snapshot MemoryView
	// queued holds the start request of an account waiting for memory. The
	// password of a fresh login stays only in this process's memory and is
	// dropped as soon as the worker starts or the account is forgotten.
	queued map[string]queuedStart
	note   string
	wake   chan struct{}
}

type queuedStart struct {
	account *Account
	dir     string
	creds   credentials
	resume  bool
}

// MemoryAccount is one account's line in the memory panel.
type MemoryAccount struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Running   bool   `json:"running"`
	Bytes     uint64 `json:"bytes"`
	Processes int    `json:"processes"`
	State     string `json:"state,omitempty"` // "queued", "paused" or ""
	Reason    string `json:"reason,omitempty"`
}

// MemoryView is served at /api/memory and embedded in /api/view.
type MemoryView struct {
	Supported           bool              `json:"supported"`
	Error               string            `json:"error,omitempty"`
	Settings            memguard.Settings `json:"settings"`
	System              memguard.System   `json:"system"`
	AppBytes            uint64            `json:"app_bytes"`
	DashboardBytes      uint64            `json:"dashboard_bytes"`
	WorkerEstimateBytes uint64            `json:"worker_estimate_bytes"`
	GoMemoryLimitBytes  int64             `json:"go_memory_limit_bytes"`
	UnderPressure       bool              `json:"under_pressure"`
	PressureReason      string            `json:"pressure_reason,omitempty"`
	Accounts            []MemoryAccount   `json:"accounts"`
	Queued              []string          `json:"queued"`
	Paused              []string          `json:"paused"`
	CheckedAt           time.Time         `json:"checked_at"`
}

func (s *Server) mem() *memoryState {
	s.memory.once.Do(func() {
		settings, note := loadMemorySettings(s.config.Dir)
		s.memory.guard = memguard.New(settings)
		if s.memoryReader != nil {
			s.memory.reader = s.memoryReader
		} else {
			s.memory.reader = memguard.NewReader()
		}
		s.memory.queued = map[string]queuedStart{}
		s.memory.note = note
		s.memory.wake = make(chan struct{}, 1)
		applyGoMemoryLimit(settings)
	})
	return &s.memory
}

func loadMemorySettings(dir string) (memguard.Settings, string) {
	settings := memguard.DefaultSettings()
	data, err := os.ReadFile(filepath.Join(dir, memorySettingsFile))
	if errors.Is(err, os.ErrNotExist) {
		return settings, ""
	}
	if err != nil {
		return settings, "内存设置无法读取，已使用默认值"
	}
	var saved memguard.Settings
	if json.Unmarshal(data, &saved) != nil || saved.Validate() != nil {
		return settings, "内存设置文件无效，已使用默认值"
	}
	return saved, ""
}

func saveMemorySettings(dir string, settings memguard.Settings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp := filepath.Join(dir, memorySettingsFile+".tmp")
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, filepath.Join(dir, memorySettingsFile))
}

func applyGoMemoryLimit(settings memguard.Settings) int64 {
	limit := int64(float64(settings.MaxAppMB) * goMemoryLimitFraction * memguard.MiB)
	if limit < 64*memguard.MiB {
		limit = 64 * memguard.MiB
	}
	debug.SetMemoryLimit(limit)
	return limit
}

func accountKey(account *Account, dir string) string {
	if account != nil {
		return account.ID
	}
	return "dir:" + filepath.Clean(dir)
}

// touchAccount records dashboard activity on an account; the least recently
// active idle worker is the one paused under pressure.
func (s *Server) touchAccount(account *Account) {
	if account != nil {
		s.mem().guard.Touch(account.ID)
	}
}

type workerReading struct {
	account   *Account
	key       string
	status    Status
	bytes     uint64
	processes int
}

// sampleMemory reads system memory and the memory of the dashboard and every
// running worker (each worker's whole process tree, so helper processes such
// as the native ADI bridge are counted too).
func (s *Server) sampleMemory() (memguard.Sample, []workerReading, uint64, error) {
	m := s.mem()
	system, err := m.reader.System()
	if err != nil {
		return memguard.Sample{}, nil, 0, err
	}
	procs, _ := m.reader.Processes()
	self, _ := m.reader.RSS(os.Getpid())
	sample := memguard.Sample{System: system, AppBytes: self}
	s.accountsMu.Lock()
	accounts := append([]*Account(nil), s.accounts...)
	s.accountsMu.Unlock()
	var workers []workerReading
	for _, account := range accounts {
		status := s.accountStatus(account)
		reading := workerReading{account: account, key: account.ID, status: status}
		if status.Running && status.PID > 0 && status.PID != os.Getpid() {
			reading.bytes, reading.processes = memguard.TreeRSS(m.reader, procs, status.PID)
			sample.AppBytes += reading.bytes
		}
		workers = append(workers, reading)
	}
	return sample, workers, self, nil
}

// admitWorker is the start gate. It returns false with a Chinese reason when
// the worker must wait, after queueing the request so it starts by itself.
func (s *Server) admitWorker(dir string, account *Account, creds credentials, resume bool) (bool, string) {
	m := s.mem()
	sample, _, _, err := s.sampleMemory()
	if err != nil {
		// Without a reading the guard cannot judge; never block a start on it.
		return true, ""
	}
	key := accountKey(account, dir)
	ok, reason := m.guard.Admit(key, sample)
	m.mu.Lock()
	if ok {
		delete(m.queued, key)
	} else if state, _ := m.guard.State(key); state == "queued" {
		m.queued[key] = queuedStart{account: account, dir: dir, creds: creds, resume: resume}
	}
	m.mu.Unlock()
	return ok, reason
}

// forgetMemory drops an account from the queue and the paused set, for example
// after the user stopped or removed it.
func (s *Server) forgetMemory(account *Account, dir string) {
	m := s.mem()
	key := accountKey(account, dir)
	m.guard.Forget(key)
	m.mu.Lock()
	delete(m.queued, key)
	m.mu.Unlock()
}

func workerBusy(account *Account, status Status) bool {
	account.mu.Lock()
	launching := account.launching
	account.mu.Unlock()
	if launching || status.VerificationRequired {
		return true
	}
	switch status.State {
	case "starting", "initializing", "signing_in", "verifying_pairing":
		return true
	}
	return strings.HasPrefix(status.State, "awaiting_")
}

// memoryLoop checks memory at the configured interval and applies at most one
// action per check, so every action is judged on a fresh reading.
func (s *Server) memoryLoop(ctx context.Context) {
	m := s.mem()
	for {
		s.memoryTick(ctx)
		timer := time.NewTimer(m.guard.Settings().Interval())
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-m.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (s *Server) wakeMemory() {
	select {
	case s.mem().wake <- struct{}{}:
	default:
	}
}

func (s *Server) memoryTick(ctx context.Context) {
	m := s.mem()
	sample, workers, self, err := s.sampleMemory()
	view := MemoryView{Settings: m.guard.Settings(), CheckedAt: time.Now().UTC(), GoMemoryLimitBytes: debug.SetMemoryLimit(-1)}
	if err != nil {
		view.Error = "无法读取本机内存：" + err.Error()
		m.mu.Lock()
		m.snapshot = view
		m.mu.Unlock()
		return
	}
	var running []memguard.Worker
	paused := map[string]bool{}
	for _, id := range m.guard.Paused() {
		paused[id] = true
	}
	for _, w := range workers {
		if w.status.Running && w.bytes > 0 && w.status.State == "online" {
			m.guard.Estimator().Observe(w.bytes)
		}
		if w.status.Running && !paused[w.key] {
			running = append(running, memguard.Worker{ID: w.key, Busy: workerBusy(w.account, w.status)})
		}
	}
	plan := m.guard.Plan(sample, running)
	s.publishMemory(view, sample, workers, self)
	switch {
	case plan.Pause != "":
		s.pauseForMemory(ctx, plan.Pause, plan.PauseReason)
	case plan.StartQueued != "":
		s.startQueued(ctx, plan.StartQueued)
	case plan.Resume != "":
		s.resumeFromPause(ctx, plan.Resume)
	}
}

func (s *Server) publishMemory(view MemoryView, sample memguard.Sample, workers []workerReading, self uint64) {
	m := s.mem()
	view.Supported = true
	view.System = sample.System
	view.AppBytes = sample.AppBytes
	view.DashboardBytes = self
	view.WorkerEstimateBytes = m.guard.Estimator().Estimate()
	view.UnderPressure, view.PressureReason = m.guard.Policy().UnderPressure(sample)
	view.Queued = append([]string{}, m.guard.Queued()...)
	view.Paused = append([]string{}, m.guard.Paused()...)
	view.Accounts = make([]MemoryAccount, 0, len(workers))
	for _, w := range workers {
		state, reason := m.guard.State(w.key)
		view.Accounts = append(view.Accounts, MemoryAccount{ID: w.account.ID, Name: w.account.Name, Running: w.status.Running,
			Bytes: w.bytes, Processes: w.processes, State: state, Reason: reason})
	}
	if view.Error == "" && m.note != "" {
		view.Error = m.note
	}
	m.mu.Lock()
	m.snapshot = view
	m.mu.Unlock()
}

func (s *Server) memorySnapshot() *MemoryView {
	m := s.mem()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snapshot.CheckedAt.IsZero() {
		return &MemoryView{Settings: m.guard.Settings(), Error: "正在读取内存状态", Accounts: []MemoryAccount{}, Queued: []string{}, Paused: []string{}}
	}
	snapshot := m.snapshot
	return &snapshot
}

func (s *Server) pauseForMemory(ctx context.Context, id, reason string) {
	account := s.lookup(id)
	if account == nil {
		return
	}
	m := s.mem()
	// Marked first so the next check does not pick the same worker while its
	// clean shutdown is still in progress.
	m.guard.MarkPaused(id, reason)
	if err := s.stopIn(ctx, account.Dir); err != nil {
		m.guard.Forget(id)
		return
	}
	s.setMessage(account, "已因内存不足暂停（"+reason+"）；内存恢复后会自动继续")
}

func (s *Server) startQueued(ctx context.Context, id string) {
	m := s.mem()
	m.mu.Lock()
	request, ok := m.queued[id]
	m.mu.Unlock()
	if !ok {
		m.guard.Forget(id)
		return
	}
	result := s.startInMode(ctx, request.dir, request.creds, request.account, request.resume)
	if stillWaiting(result) {
		return // the launch lock was busy or the gate queued it again
	}
	m.mu.Lock()
	delete(m.queued, id)
	m.mu.Unlock()
	m.guard.Forget(id)
	if !result.OK {
		s.setMessage(request.account, result.Message)
	}
}

func (s *Server) resumeFromPause(ctx context.Context, id string) {
	account := s.lookup(id)
	m := s.mem()
	if account == nil {
		m.guard.Forget(id)
		return
	}
	result := s.startSavedAccount(ctx, account)
	if stillWaiting(result) {
		return // stays paused; it resumes when memory allows
	}
	m.guard.Forget(id)
	if !result.OK {
		s.setMessage(account, "因内存暂停的账号未能自动恢复："+result.Message)
	}
}

// launchBusyMessage is startInMode's reply while another start holds the lock.
const launchBusyMessage = "正在启动，请稍等"

// memoryQueuedPrefix starts startInMode's reply when the gate deferred a start.
const memoryQueuedPrefix = "内存不足"

func stillWaiting(result control.Result) bool {
	return result.Message == launchBusyMessage || (result.OK && strings.HasPrefix(result.Message, memoryQueuedPrefix))
}

// updateMemorySettings validates, applies live and persists new settings.
func (s *Server) updateMemorySettings(settings memguard.Settings) (bool, string) {
	m := s.mem()
	if err := m.guard.SetSettings(settings); err != nil {
		return false, err.Error()
	}
	applyGoMemoryLimit(settings)
	m.note = ""
	message := "内存设置已生效"
	if err := saveMemorySettings(s.config.Dir, settings); err != nil {
		message = "内存设置已生效，但未能保存到磁盘"
	}
	s.wakeMemory()
	return true, message
}
