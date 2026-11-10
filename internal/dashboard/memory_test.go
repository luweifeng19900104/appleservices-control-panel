package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Laky-64/appleservices/internal/memguard"
)

type fakeMemory struct {
	mu        sync.Mutex
	available uint64
}

func (f *fakeMemory) System() (memguard.System, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return memguard.System{TotalBytes: 8 << 30, AvailableBytes: f.available, Source: "fake"}, nil
}
func (f *fakeMemory) Processes() ([]memguard.Process, error) { return nil, nil }
func (f *fakeMemory) RSS(pid int) (uint64, error) {
	if pid == os.Getpid() {
		return 40 << 20, nil
	}
	return 0, errors.New("no such process")
}
func (f *fakeMemory) set(mb uint64) { f.mu.Lock(); f.available = mb << 20; f.mu.Unlock() }

func TestStartIsQueuedUntilMemoryFrees(t *testing.T) {
	dir := t.TempDir()
	server, err := New(Config{Dir: dir, Worker: filepath.Join(dir, "missing-worker"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	memory := &fakeMemory{}
	memory.set(1100) // 1100 - 150 estimate < 1024 reserve
	server.memoryReader = memory
	result := server.startIn(context.Background(), dir, credentials{AppleID: "user@example.invalid", Password: "test-only"}, nil)
	if !result.OK || !strings.HasPrefix(result.Message, memoryQueuedPrefix) {
		t.Fatalf("start was not queued: %+v", result)
	}
	key := accountKey(nil, dir)
	if state, _ := server.mem().guard.State(key); state != "queued" {
		t.Fatalf("state = %q", state)
	}
	// Still tight: the check leaves it queued.
	server.memoryTick(context.Background())
	if state, _ := server.mem().guard.State(key); state != "queued" {
		t.Fatal("dequeued without memory")
	}
	if view := server.memorySnapshot(); !view.Supported || len(view.Queued) != 1 || view.DashboardBytes != 40<<20 {
		t.Fatalf("snapshot %+v", view)
	}
	// Memory frees: the next check starts it through the normal path (which
	// here stops at the missing worker binary) and drops the held password.
	memory.set(6000)
	server.memoryTick(context.Background())
	if state, _ := server.mem().guard.State(key); state != "" {
		t.Fatalf("still %q after memory freed", state)
	}
	server.mem().mu.Lock()
	held := len(server.mem().queued)
	server.mem().mu.Unlock()
	if held != 0 {
		t.Fatal("queued credentials were kept after the start")
	}
	if server.message != "缺少后台程序，请重新运行启动脚本" {
		t.Fatalf("message = %q", server.message)
	}
}

func TestMemorySettingsApplyLiveAndPersist(t *testing.T) {
	dir := t.TempDir()
	server, err := New(Config{Dir: dir, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	if err := server.auth.configure("test-management-password"); err != nil {
		t.Fatal(err)
	}
	session, _, err := server.auth.newSession()
	if err != nil {
		t.Fatal(err)
	}
	authCookie := &http.Cookie{Name: dashboardSessionCookie, Value: session}
	server.memoryReader = &fakeMemory{available: 4 << 30}
	post := func(body string) map[string]any {
		request := httptest.NewRequest("POST", server.URL()+"/api/memory/settings", bytes.NewBufferString(body))
		request.AddCookie(authCookie)
		request.Header.Set("X-App-Token", server.token)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		var out map[string]any
		if recorder.Code != 200 || json.Unmarshal(recorder.Body.Bytes(), &out) != nil {
			t.Fatalf("status %d %s", recorder.Code, recorder.Body.String())
		}
		return out
	}
	if out := post(`{"reserve_mb":512,"max_app_mb":64,"interval_seconds":5}`); out["ok"] != false {
		t.Fatal("invalid settings accepted")
	}
	if out := post(`{"reserve_mb":512,"max_app_mb":1024,"interval_seconds":2}`); out["ok"] != true {
		t.Fatalf("valid settings rejected: %v", out)
	}
	if got := server.mem().guard.Settings(); got.ReserveMB != 512 || got.MaxAppMB != 1024 || got.IntervalSeconds != 2 {
		t.Fatalf("not applied live: %+v", got)
	}
	saved, note := loadMemorySettings(dir)
	if note != "" || saved.MaxAppMB != 1024 {
		t.Fatalf("not persisted: %+v %q", saved, note)
	}
	if limit := applyGoMemoryLimit(saved); limit != 256<<20 {
		t.Fatalf("go memory limit = %d", limit)
	}
	request := httptest.NewRequest("GET", server.URL()+"/api/memory", nil)
	request.AddCookie(authCookie)
	request.Header.Set("X-App-Token", server.token)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), `"reserve_mb":512`) {
		t.Fatalf("memory endpoint: %d %s", recorder.Code, recorder.Body.String())
	}
	os.WriteFile(filepath.Join(dir, memorySettingsFile), []byte(`{"reserve_mb":-5}`), 0o600)
	if s, note := loadMemorySettings(dir); note == "" || s != memguard.DefaultSettings() {
		t.Fatal("corrupt settings not replaced by defaults")
	}
}

func TestWorkerArgsKeepOriginalWindowsCommandLine(t *testing.T) {
	native := &Server{config: Config{NativeADI: true, Library: `C:\lib.dll`, Python: "py"}}
	want := []string{"-native-adi", "-background", "-control-api", "-gui-auth", "-dir", "D", "-adi-dll", `C:\lib.dll`, "-python", "py"}
	if got := native.workerArgs("D"); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("native args changed: %v", got)
	}
	plain := &Server{config: Config{CodeProvider: "/opt/provider"}}
	got := strings.Join(plain.workerArgs("D"), " ")
	if strings.Contains(got, "-native-adi") || strings.Contains(got, "-adi-dll") || !strings.Contains(got, "-code-provider /opt/provider") {
		t.Fatalf("non-native args: %s", got)
	}
}
