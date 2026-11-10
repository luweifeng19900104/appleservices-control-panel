//go:build linux

package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Laky-64/appleservices/internal/memguard"
)

type pidMemory struct {
	mu        sync.Mutex
	available uint64
	rss       map[int]uint64
}

func (f *pidMemory) System() (memguard.System, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return memguard.System{TotalBytes: 8 << 30, AvailableBytes: f.available, Source: "fake"}, nil
}
func (f *pidMemory) Processes() ([]memguard.Process, error) { return nil, nil }
func (f *pidMemory) RSS(pid int) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.rss[pid]; ok {
		return v, nil
	}
	return 0, os.ErrNotExist
}

// TestPressurePausesWorkerCleanlyThenReportsResume runs a stand-in worker
// process, puts the guard under pressure, and checks that the worker is
// stopped through the normal stop request (never killed by the dashboard),
// marked paused, and that resume goes through the saved-session start path.
func TestPressurePausesWorkerCleanlyThenReportsResume(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep unavailable")
	}
	root := t.TempDir()
	child := exec.Command(sleep, "60")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Process.Kill()
	exe := processPath(child.Process.Pid)
	if exe == "" {
		t.Skip("cannot resolve /proc/<pid>/exe")
	}
	server, err := New(Config{Dir: root, Worker: exe, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	memory := &pidMemory{available: 6 << 30, rss: map[int]uint64{os.Getpid(): 30 << 20, child.Process.Pid: 1900 << 20}}
	server.memoryReader = memory
	dir := filepath.Join(root, "accounts", "01-a")
	os.MkdirAll(dir, 0o700)
	account := &Account{ID: "01-a", Name: "a", AppleID: "a@example.invalid", Dir: dir}
	server.accounts = []*Account{account}
	status, _ := json.Marshal(Status{PID: child.Process.Pid, Running: true, Authenticated: true, State: "online"})
	os.WriteFile(filepath.Join(dir, "runtime.json"), status, 0o600)

	// The stand-in honours the dashboard's clean stop request.
	go func() {
		for i := 0; i < 200; i++ {
			if _, err := os.Stat(filepath.Join(dir, "stop-request.json")); err == nil {
				child.Process.Signal(os.Interrupt)
				child.Wait()
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	// 30 + 1900 MB is under the 2048 cap: no action.
	server.memoryTick(context.Background())
	if state, _ := server.mem().guard.State("01-a"); state != "" {
		t.Fatalf("acted without pressure: %q", state)
	}
	// The worker grows past the cap.
	memory.mu.Lock()
	memory.rss[child.Process.Pid] = 2100 << 20
	memory.mu.Unlock()
	server.memoryTick(context.Background())
	if state, reason := server.mem().guard.State("01-a"); state != "paused" || reason == "" {
		t.Fatalf("not paused: %q", state)
	}
	if !strings.Contains(account.message, "已因内存不足暂停") {
		t.Fatalf("message = %q", account.message)
	}
	if server.accountStatus(account).Running {
		t.Fatal("worker still running")
	}
	view := server.memorySnapshot()
	if len(view.Accounts) != 1 || view.Accounts[0].Bytes != 2100<<20 || !view.UnderPressure {
		t.Fatalf("snapshot %+v", view)
	}
	// The guard learned this worker is ~2 GB, so with the 2048 MB cap it stays
	// paused: 30 + 2000 > 2048 * 0.9 (hysteresis).
	server.memoryTick(context.Background())
	if state, _ := server.mem().guard.State("01-a"); state != "paused" {
		t.Fatalf("resumed without hysteresis headroom: %q", state)
	}
	// The user raises the cap in the panel; it applies on the next check and
	// the paused account resumes through the saved-session path. With no saved
	// session here, that is reported and it is dropped.
	if ok, _ := server.updateMemorySettings(memguard.Settings{ReserveMB: 1024, MaxAppMB: 4096, IntervalSeconds: 5}); !ok {
		t.Fatal("settings rejected")
	}
	server.memoryTick(context.Background())
	if state, _ := server.mem().guard.State("01-a"); state != "" {
		t.Fatalf("still %q", state)
	}
	if !strings.Contains(account.message, "因内存暂停的账号未能自动恢复") {
		t.Fatalf("resume message = %q", account.message)
	}
}
