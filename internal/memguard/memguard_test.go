package memguard

import (
	"errors"
	"testing"
	"time"
)

type fakeReader struct {
	system System
	procs  []Process
	rss    map[int]uint64
}

func (f *fakeReader) System() (System, error)       { return f.system, nil }
func (f *fakeReader) Processes() ([]Process, error) { return f.procs, nil }
func (f *fakeReader) RSS(pid int) (uint64, error) {
	if v, ok := f.rss[pid]; ok {
		return v, nil
	}
	return 0, errors.New("gone")
}

func mb(n float64) uint64 { return uint64(n * MiB) }

func sample(availMB, appMB float64) Sample {
	return Sample{System: System{TotalBytes: mb(8192), AvailableBytes: mb(availMB)}, AppBytes: mb(appMB)}
}

func TestDefaultsAndValidation(t *testing.T) {
	d := DefaultSettings()
	if d.ReserveMB != 1024 || d.MaxAppMB != 2048 || d.IntervalSeconds != 5 || d.Validate() != nil {
		t.Fatalf("unexpected defaults %+v", d)
	}
	for _, bad := range []Settings{{-1, 2048, 5}, {1024, 64, 5}, {1024, 2048, 0}, {1024, 2048, 301}} {
		if bad.Validate() == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	g := New(d)
	if g.SetSettings(Settings{1024, 10, 5}) == nil || g.Settings() != d {
		t.Fatal("invalid settings were applied")
	}
	if err := g.SetSettings(Settings{512, 1024, 2}); err != nil || g.Settings().ReserveMB != 512 {
		t.Fatal("valid settings were not applied live")
	}
}

func TestGateBoundaries(t *testing.T) {
	p := Policy{Settings: DefaultSettings(), Hysteresis: DefaultHysteresis}
	est := mb(150)
	cases := []struct {
		avail, app float64
		want       bool
	}{
		{1174, 0, true},    // 1174-150 = 1024 = reserve
		{1173, 0, false},   // one MB short of the reserve
		{100, 0, false},    // less than the estimate itself
		{4096, 1898, true}, // 1898+150 = 2048 = cap
		{4096, 1899, false},
	}
	for _, c := range cases {
		if ok, reason := p.CanStart(sample(c.avail, c.app), est); ok != c.want || (!ok && reason == "") {
			t.Fatalf("avail=%v app=%v: got %v %q", c.avail, c.app, ok, reason)
		}
	}
}

func TestPressureThresholds(t *testing.T) {
	p := Policy{Settings: DefaultSettings()}
	if on, _ := p.UnderPressure(sample(1024, 2048)); on {
		t.Fatal("exactly at the limits is not pressure")
	}
	if on, _ := p.UnderPressure(sample(1023, 100)); !on {
		t.Fatal("available below reserve must be pressure")
	}
	if on, _ := p.UnderPressure(sample(4096, 2049)); !on {
		t.Fatal("app above cap must be pressure")
	}
}

func TestHysteresisForResume(t *testing.T) {
	p := Policy{Settings: DefaultSettings(), Hysteresis: 0.10}
	est := mb(150)
	// 5% above the reserve: a new start fits, a resume does not.
	s := sample(150+1024*1.05, 0)
	if ok, _ := p.CanStart(s, est); !ok {
		t.Fatal("start should fit")
	}
	if ok, _ := p.CanResume(s, est); ok {
		t.Fatal("resume must wait for the hysteresis headroom")
	}
	if ok, _ := p.CanResume(sample(150+1024*1.11, 0), est); !ok {
		t.Fatal("resume should fit with more than 10% headroom")
	}
	// App cap side: 2048*0.9 = 1843.2
	if ok, _ := p.CanResume(sample(8000, 1843.2-150+1), est); ok {
		t.Fatal("resume must stay 10% under the cap")
	}
	if ok, _ := p.CanResume(sample(8000, 1843.2-150-1), est); !ok {
		t.Fatal("resume should fit 10% under the cap")
	}
}

func TestAdmitQueuesOnceAndStartsWhenMemoryFrees(t *testing.T) {
	g := New(DefaultSettings())
	tight := sample(1100, 0)
	for i := 0; i < 3; i++ {
		if ok, reason := g.Admit("02-b", tight); ok || reason == "" {
			t.Fatal("admitted without memory")
		}
	}
	g.Admit("03-c", tight)
	if q := g.Queued(); len(q) != 2 || q[0] != "02-b" || q[1] != "03-c" {
		t.Fatalf("queue = %v", q)
	}
	if state, _ := g.State("02-b"); state != "queued" {
		t.Fatalf("state = %q", state)
	}
	if plan := g.Plan(tight, nil); plan != (Plan{}) {
		t.Fatalf("started while tight: %+v", plan)
	}
	free := sample(4096, 300)
	if plan := g.Plan(free, nil); plan.StartQueued != "02-b" {
		t.Fatalf("plan = %+v", plan)
	}
	// The dashboard starts it through the same gate, which dequeues it.
	if ok, _ := g.Admit("02-b", free); !ok {
		t.Fatal("not admitted after memory freed")
	}
	if q := g.Queued(); len(q) != 1 || q[0] != "03-c" {
		t.Fatalf("queue after start = %v", q)
	}
	g.Forget("03-c")
	if len(g.Queued()) != 0 {
		t.Fatal("forget did not dequeue")
	}
}

func TestPressurePausesLeastRecentlyActiveIdleWorker(t *testing.T) {
	g := New(DefaultSettings())
	clock := time.Unix(1000, 0)
	g.now = func() time.Time { return clock }
	for _, id := range []string{"01-a", "02-b", "03-c"} {
		g.Touch(id)
		clock = clock.Add(time.Minute)
	}
	g.Touch("01-a") // most recent now; 02-b is the oldest
	running := []Worker{{ID: "01-a"}, {ID: "02-b"}, {ID: "03-c"}}
	plan := g.Plan(sample(500, 900), running)
	if plan.Pause != "02-b" || plan.PauseReason == "" {
		t.Fatalf("plan = %+v", plan)
	}
	// A busy worker (e.g. waiting for a code) is never chosen.
	running[1].Busy = true
	if plan := g.Plan(sample(500, 900), running); plan.Pause != "03-c" {
		t.Fatalf("busy worker chosen: %+v", plan)
	}
	if plan := g.Plan(sample(500, 900), []Worker{{ID: "x", Busy: true}}); plan != (Plan{}) {
		t.Fatalf("paused a busy worker: %+v", plan)
	}
	// Pressure also blocks queued starts and resumes.
	g.MarkPaused("09-z", "test")
	g.Admit("08-y", sample(10, 0))
	if plan := g.Plan(sample(500, 900), nil); plan != (Plan{}) {
		t.Fatalf("acted under pressure: %+v", plan)
	}
}

// TestPauseResumeDoesNotFlap simulates workers of a steady size against a fake
// reader: after a pause, the freed memory must not immediately resume it.
func TestPauseResumeDoesNotFlap(t *testing.T) {
	const workerMB = 400
	g := New(Settings{ReserveMB: 1024, MaxAppMB: 2048, IntervalSeconds: 5})
	for i := 0; i < 8; i++ {
		g.Estimator().Observe(mb(workerMB))
	}
	running := map[string]bool{"01": true, "02": true, "03": true, "04": true, "05": true}
	otherProgramsMB := 0.0
	reader := &fakeReader{}
	read := func() Sample {
		app := 50.0 + workerMB*float64(len(running))
		reader.system = System{TotalBytes: mb(4096), AvailableBytes: mb(4096 - app - otherProgramsMB)}
		s, _ := reader.System()
		return Sample{System: s, AppBytes: mb(app)}
	}
	tick := func() Plan {
		var ws []Worker
		for id := range running {
			ws = append(ws, Worker{ID: id})
		}
		plan := g.Plan(read(), ws)
		switch {
		case plan.Pause != "":
			delete(running, plan.Pause)
			g.MarkPaused(plan.Pause, plan.PauseReason)
		case plan.Resume != "":
			if ok, _ := g.Admit(plan.Resume, read()); ok {
				running[plan.Resume] = true
			}
		}
		return plan
	}
	// 5 workers = 2050 MB > 2048 cap: exactly one is paused, then it stays put.
	pauses, resumes := 0, 0
	for i := 0; i < 20; i++ {
		p := tick()
		if p.Pause != "" {
			pauses++
		}
		if p.Resume != "" {
			resumes++
		}
	}
	if pauses != 1 || resumes != 0 || len(g.Paused()) != 1 {
		t.Fatalf("pauses=%d resumes=%d paused=%v", pauses, resumes, g.Paused())
	}
	// Another program grabs memory: available drops below the reserve.
	otherProgramsMB = 1600 // available = 4096-1650-1600 = 846 < 1024
	for i := 0; i < 20; i++ {
		tick()
	}
	if len(running) != 3 { // one more paused frees 400 -> 1246 available
		t.Fatalf("running after squeeze = %d", len(running))
	}
	// The other program exits: paused workers come back one per tick, but
	// only while the hysteresis headroom holds.
	otherProgramsMB = 0
	for i := 0; i < 20; i++ {
		tick()
	}
	// 4 workers = 1650 MB; a 5th would need 1650+400 <= 1843 (cap - 10%): no.
	if len(running) != 4 || len(g.Paused()) != 1 {
		t.Fatalf("running=%d paused=%v", len(running), g.Paused())
	}
}

func TestEstimatorRollingAverage(t *testing.T) {
	e := NewEstimator(mb(150), 3)
	if e.Estimate() != mb(150) {
		t.Fatal("initial estimate")
	}
	e.Observe(0) // ignored
	e.Observe(mb(100))
	e.Observe(mb(200))
	if e.Estimate() != mb(150) {
		t.Fatalf("avg = %d", e.Estimate()/MiB)
	}
	e.Observe(mb(300))
	e.Observe(mb(400)) // replaces 100
	if e.Estimate() != mb(300) {
		t.Fatalf("rolling avg = %d", e.Estimate()/MiB)
	}
}

func TestTreeRSSSumsDescendantsOnly(t *testing.T) {
	r := &fakeReader{
		procs: []Process{{1, 0}, {10, 1}, {11, 10}, {12, 11}, {13, 1}, {14, 14}, {15, 12}, {12, 15}},
		rss:   map[int]uint64{10: mb(100), 11: mb(50), 12: mb(25), 13: mb(999), 14: mb(1), 15: mb(5)},
	}
	total, count := TreeRSS(r, r.procs, 10)
	if total != mb(180) || count != 4 {
		t.Fatalf("total=%d count=%d", total/MiB, count)
	}
	if total, _ := TreeRSS(r, r.procs, 99); total != 0 {
		t.Fatal("dead root must count nothing")
	}
	if total, _ := TreeRSS(r, r.procs, 14); total != mb(1) {
		t.Fatal("self-parented process loops")
	}
}

func TestEffectiveSystemUsesSmallerCgroupLimit(t *testing.T) {
	host := System{TotalBytes: mb(8192), AvailableBytes: mb(6000), Source: "meminfo"}
	s := EffectiveSystem(host, mb(3072), mb(1024), "cgroup2")
	if s.TotalBytes != mb(3072) || s.AvailableBytes != mb(2048) || s.Source != "cgroup2" {
		t.Fatalf("%+v", s)
	}
	if s := EffectiveSystem(host, 0, 0, "cgroup2"); s != host {
		t.Fatal("unlimited cgroup changed reading")
	}
	if s := EffectiveSystem(host, mb(9000), 0, "cgroup2"); s != host {
		t.Fatal("limit above host changed reading")
	}
	if s := EffectiveSystem(host, mb(1000), mb(2000), "cgroup1"); s.AvailableBytes != 0 {
		t.Fatal("over-limit usage must leave nothing available")
	}
}
