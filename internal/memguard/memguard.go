// Package memguard keeps the dashboard and its account workers inside a memory
// budget. It reads live system and per-process memory, decides whether a new
// worker may start, and, under pressure, which running worker to pause and
// when a paused or queued one may come back.
//
// The package only decides. Starting and stopping workers stays with the
// dashboard, which uses its normal start path and its clean stop request.
package memguard

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// MiB is one mebibyte. Settings are expressed in MiB ("MB" in the UI).
const MiB = 1 << 20

// Settings are the user-editable limits. They are persisted by the dashboard.
type Settings struct {
	// ReserveMB is memory left free for other programs on this machine.
	ReserveMB int `json:"reserve_mb"`
	// MaxAppMB caps the dashboard plus every account worker together.
	MaxAppMB int `json:"max_app_mb"`
	// IntervalSeconds is how often memory is checked.
	IntervalSeconds int `json:"interval_seconds"`
}

// Defaults for a fresh install.
const (
	DefaultReserveMB       = 1024
	DefaultMaxAppMB        = 2048
	DefaultIntervalSeconds = 5
	// DefaultWorkerEstimateMB is used before any worker has been observed.
	DefaultWorkerEstimateMB = 150
	// DefaultHysteresis is the extra headroom (10%) required before a worker
	// paused for memory is resumed, so it does not flap between states.
	DefaultHysteresis = 0.10
)

// DefaultSettings returns the documented defaults.
func DefaultSettings() Settings {
	return Settings{ReserveMB: DefaultReserveMB, MaxAppMB: DefaultMaxAppMB, IntervalSeconds: DefaultIntervalSeconds}
}

// Validate checks that the settings are usable. Chinese messages are shown in
// the dashboard as they are.
func (s Settings) Validate() error {
	switch {
	case s.ReserveMB < 0 || s.ReserveMB > 1<<20:
		return errors.New("预留内存需在 0 到 1048576 MB 之间")
	case s.MaxAppMB < 128 || s.MaxAppMB > 1<<20:
		return errors.New("本程序内存上限需在 128 到 1048576 MB 之间")
	case s.IntervalSeconds < 1 || s.IntervalSeconds > 300:
		return errors.New("检查间隔需在 1 到 300 秒之间")
	}
	return nil
}

// Interval returns the check interval as a duration.
func (s Settings) Interval() time.Duration { return time.Duration(s.IntervalSeconds) * time.Second }

// System is one reading of machine (or container) memory.
type System struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	// Source names where the reading came from, for diagnostics:
	// "GlobalMemoryStatusEx", "meminfo", "cgroup2" or "cgroup1".
	Source string `json:"source"`
	// CgroupLimitBytes and CgroupUsageBytes are set when the process runs in a
	// memory-limited cgroup (for example a systemd unit with MemoryMax=).
	CgroupLimitBytes uint64 `json:"cgroup_limit_bytes,omitempty"`
	CgroupUsageBytes uint64 `json:"cgroup_usage_bytes,omitempty"`
}

// Process is one entry in the process table: just enough to find children.
type Process struct {
	PID  int
	PPID int
}

// Reader reads live memory. NewReader returns the platform implementation;
// tests use a fake.
type Reader interface {
	System() (System, error)
	// Processes lists running processes with their parents.
	Processes() ([]Process, error)
	// RSS returns the resident set (Linux) or working set (Windows) of a process.
	RSS(pid int) (uint64, error)
}

// TreeRSS sums the memory of pid and all of its descendants found in procs.
// It returns the total and the number of processes counted. A process that
// cannot be read (already exited, access denied) is skipped.
func TreeRSS(r Reader, procs []Process, pid int) (uint64, int) {
	if pid <= 0 {
		return 0, 0
	}
	children := map[int][]int{}
	for _, p := range procs {
		if p.PID != p.PPID {
			children[p.PPID] = append(children[p.PPID], p.PID)
		}
	}
	var total uint64
	count := 0
	seen := map[int]bool{}
	queue := []int{pid}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if seen[next] {
			continue
		}
		seen[next] = true
		if rss, err := r.RSS(next); err == nil {
			total += rss
			count++
		} else if next == pid {
			// The root itself is gone; its stale children are not ours.
			return 0, 0
		}
		queue = append(queue, children[next]...)
	}
	return total, count
}

// Estimator is a rolling average of observed worker memory.
type Estimator struct {
	mu      sync.Mutex
	initial uint64
	window  []uint64
	size    int
	next    int
}

// NewEstimator starts at initial bytes and averages the last size observations.
func NewEstimator(initial uint64, size int) *Estimator {
	if size < 1 {
		size = 1
	}
	return &Estimator{initial: initial, size: size}
}

// Observe records one worker's measured memory.
func (e *Estimator) Observe(bytes uint64) {
	if bytes == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.window) < e.size {
		e.window = append(e.window, bytes)
		return
	}
	e.window[e.next] = bytes
	e.next = (e.next + 1) % e.size
}

// Estimate returns the expected memory of one more worker.
func (e *Estimator) Estimate() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.window) == 0 {
		return e.initial
	}
	var sum uint64
	for _, v := range e.window {
		sum += v
	}
	return sum / uint64(len(e.window))
}

// Sample is what one check observed.
type Sample struct {
	System   System
	AppBytes uint64 // dashboard plus every worker tree
}

// Policy turns settings into decisions. It is pure and safe to copy.
type Policy struct {
	Settings   Settings
	Hysteresis float64
}

func (p Policy) reserve() uint64 { return uint64(p.Settings.ReserveMB) * MiB }
func (p Policy) maxApp() uint64  { return uint64(p.Settings.MaxAppMB) * MiB }

func scale(v uint64, f float64) uint64 { return uint64(float64(v) * f) }

// CanStart reports whether a worker of the estimated size may start now:
// (available − estimate) ≥ reserve AND (app + estimate) ≤ max_app.
// The reason is a Chinese explanation when it may not.
func (p Policy) CanStart(s Sample, estimate uint64) (bool, string) {
	return p.fits(s, estimate, 0)
}

// CanResume is CanStart with hysteresis headroom: the reserve must be exceeded
// by the hysteresis fraction and the app total must stay that fraction below
// its cap. It is used for workers that were paused for memory.
func (p Policy) CanResume(s Sample, estimate uint64) (bool, string) {
	return p.fits(s, estimate, p.Hysteresis)
}

func (p Policy) fits(s Sample, estimate uint64, h float64) (bool, string) {
	reserve := scale(p.reserve(), 1+h)
	limit := scale(p.maxApp(), 1-h)
	if s.System.AvailableBytes < estimate || s.System.AvailableBytes-estimate < reserve {
		return false, "系统可用内存不足（需为其他程序保留预留内存）"
	}
	if s.AppBytes+estimate > limit {
		return false, "本程序内存已接近上限"
	}
	return true, ""
}

// UnderPressure reports whether a running worker should be paused now:
// available < reserve OR app total > max_app.
func (p Policy) UnderPressure(s Sample) (bool, string) {
	if s.System.AvailableBytes < p.reserve() {
		return true, "系统可用内存低于预留值"
	}
	if s.AppBytes > p.maxApp() {
		return true, "本程序内存超过上限"
	}
	return false, ""
}

// Worker is a running account worker as the guard sees it.
type Worker struct {
	ID string
	// Busy workers (starting, waiting for a verification code, or with a
	// pending request) are not paused: interrupting them loses user input.
	Busy bool
}

// Plan is the single action to take after one check. At most one field is set,
// so every action is followed by a fresh reading before the next one.
type Plan struct {
	Pause       string // account to stop cleanly and mark "paused: memory"
	PauseReason string
	StartQueued string // queued account that may start now
	Resume      string // paused account that may resume now
}

// Guard holds the queue, the paused set and per-account activity.
type Guard struct {
	mu         sync.Mutex
	policy     Policy
	estimator  *Estimator
	queued     []string
	paused     map[string]time.Time
	reasons    map[string]string
	lastActive map[string]time.Time
	now        func() time.Time
}

// New returns a guard with the given settings.
func New(settings Settings) *Guard {
	return &Guard{
		policy:     Policy{Settings: settings, Hysteresis: DefaultHysteresis},
		estimator:  NewEstimator(DefaultWorkerEstimateMB*MiB, 32),
		paused:     map[string]time.Time{},
		reasons:    map[string]string{},
		lastActive: map[string]time.Time{},
		now:        time.Now,
	}
}

// Settings returns the active settings.
func (g *Guard) Settings() Settings {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.policy.Settings
}

// SetSettings applies new settings immediately.
func (g *Guard) SetSettings(s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	g.mu.Lock()
	g.policy.Settings = s
	g.mu.Unlock()
	return nil
}

// Policy returns a copy of the current policy.
func (g *Guard) Policy() Policy {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.policy
}

// Estimator returns the worker size estimator.
func (g *Guard) Estimator() *Estimator { return g.estimator }

// Touch records user or worker activity on an account.
func (g *Guard) Touch(id string) {
	if id == "" {
		return
	}
	g.mu.Lock()
	g.lastActive[id] = g.now()
	g.mu.Unlock()
}

// LastActive returns when the account was last active (zero if never).
func (g *Guard) LastActive(id string) time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastActive[id]
}

// Admit decides whether an account worker may start now. When it may not, the
// account is queued (once) and the Chinese reason is returned.
func (g *Guard) Admit(id string, s Sample) (bool, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ok, reason := g.policy.CanStart(s, g.estimator.Estimate())
	if ok {
		g.removeQueuedLocked(id)
		delete(g.paused, id)
		delete(g.reasons, id)
		return true, ""
	}
	// A paused account stays paused (it resumes with hysteresis); anything
	// else waits in the queue, once.
	if _, paused := g.paused[id]; !paused && !g.containsQueuedLocked(id) {
		g.queued = append(g.queued, id)
	}
	g.reasons[id] = reason
	return false, reason
}

// Forget removes an account from the queue and the paused set, for example
// when the user stops or removes it.
func (g *Guard) Forget(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.removeQueuedLocked(id)
	delete(g.paused, id)
	delete(g.reasons, id)
}

// MarkPaused records that an account was stopped for memory.
func (g *Guard) MarkPaused(id, reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.removeQueuedLocked(id)
	g.paused[id] = g.now()
	g.reasons[id] = reason
}

// State returns "queued", "paused" or "" for an account, and the reason.
func (g *Guard) State(id string) (string, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.paused[id]; ok {
		return "paused", g.reasons[id]
	}
	if g.containsQueuedLocked(id) {
		return "queued", g.reasons[id]
	}
	return "", ""
}

// Queued returns the queue in start order.
func (g *Guard) Queued() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.queued...)
}

// Paused returns paused accounts, longest paused first.
func (g *Guard) Paused() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pausedOrderLocked()
}

func (g *Guard) pausedOrderLocked() []string {
	out := make([]string, 0, len(g.paused))
	for id := range g.paused {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := g.paused[out[i]], g.paused[out[j]]
		if a.Equal(b) {
			return out[i] < out[j]
		}
		return a.Before(b)
	})
	return out
}

// Plan decides the next action from one sample and the running workers.
//
// Under pressure it pauses the least-recently-active worker that is not busy.
// Otherwise it starts the first queued account if it fits, or else resumes the
// longest-paused account if it fits with hysteresis headroom.
func (g *Guard) Plan(s Sample, running []Worker) Plan {
	g.mu.Lock()
	defer g.mu.Unlock()
	if pressure, reason := g.policy.UnderPressure(s); pressure {
		var victim string
		var victimAt time.Time
		for _, w := range running {
			if w.Busy {
				continue
			}
			at := g.lastActive[w.ID]
			if victim == "" || at.Before(victimAt) || (at.Equal(victimAt) && w.ID < victim) {
				victim, victimAt = w.ID, at
			}
		}
		if victim != "" {
			return Plan{Pause: victim, PauseReason: reason}
		}
		return Plan{}
	}
	estimate := g.estimator.Estimate()
	if len(g.queued) > 0 {
		if ok, _ := g.policy.CanStart(s, estimate); ok {
			return Plan{StartQueued: g.queued[0]}
		}
		return Plan{}
	}
	if order := g.pausedOrderLocked(); len(order) > 0 {
		if ok, _ := g.policy.CanResume(s, estimate); ok {
			return Plan{Resume: order[0]}
		}
	}
	return Plan{}
}

func (g *Guard) containsQueuedLocked(id string) bool {
	for _, q := range g.queued {
		if q == id {
			return true
		}
	}
	return false
}

func (g *Guard) removeQueuedLocked(id string) {
	out := g.queued[:0]
	for _, q := range g.queued {
		if q != id {
			out = append(out, q)
		}
	}
	g.queued = out
}

// EffectiveSystem folds a cgroup limit into a host reading: inside a limited
// cgroup the usable total and available memory are the smaller of the two.
func EffectiveSystem(host System, limit, usage uint64, source string) System {
	out := host
	if limit == 0 || limit >= host.TotalBytes && host.TotalBytes != 0 {
		return out
	}
	out.CgroupLimitBytes, out.CgroupUsageBytes = limit, usage
	out.TotalBytes = limit
	var free uint64
	if usage < limit {
		free = limit - usage
	}
	if free < out.AvailableBytes {
		out.AvailableBytes = free
	}
	out.Source = source
	return out
}
