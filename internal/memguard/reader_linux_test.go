//go:build linux

package memguard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMeminfo(t *testing.T) {
	s, err := parseMeminfo([]byte("MemTotal:  4000000 kB\nMemFree: 100 kB\nMemAvailable:   2000000 kB\n"))
	if err != nil || s.TotalBytes != 4000000*1024 || s.AvailableBytes != 2000000*1024 {
		t.Fatalf("%+v %v", s, err)
	}
	s, _ = parseMeminfo([]byte("MemTotal: 10 kB\nMemFree: 1 kB\nBuffers: 2 kB\nCached: 3 kB\n"))
	if s.AvailableBytes != 6*1024 {
		t.Fatal("pre-3.14 fallback")
	}
	if _, err := parseMeminfo([]byte("junk")); err == nil {
		t.Fatal("accepted meminfo without MemTotal")
	}
}

func TestParseStatAndStatus(t *testing.T) {
	if ppid, ok := parseStatPPID([]byte("42 (we (ird) name) S 7 42 42 0")); !ok || ppid != 7 {
		t.Fatalf("ppid=%d", ppid)
	}
	if rss, _ := parseStatusRSS([]byte("Name:\tx\nVmRSS:\t  2048 kB\n")); rss != 2048*1024 {
		t.Fatal("VmRSS")
	}
	if rss, err := parseStatusRSS([]byte("Name:\tkthreadd\n")); rss != 0 || err != nil {
		t.Fatal("kernel thread")
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCgroupV2AndV1(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	cg := filepath.Join(root, "cg")
	writeFile(t, filepath.Join(proc, "meminfo"), "MemTotal: 8388608 kB\nMemAvailable: 6291456 kB\n")
	r := linuxReader{proc: proc, cgroupRoot: cg}

	writeFile(t, filepath.Join(proc, "self", "cgroup"), "0::/system.slice/appleservices.service\n")
	unit := filepath.Join(cg, "system.slice", "appleservices.service")
	writeFile(t, filepath.Join(unit, "memory.max"), "3221225472\n")
	writeFile(t, filepath.Join(unit, "memory.current"), "1073741824\n")
	writeFile(t, filepath.Join(unit, "memory.stat"), "anon 1\ninactive_file 268435456\n")
	s, err := r.System()
	if err != nil || s.Source != "cgroup2" || s.TotalBytes != 3<<30 || s.AvailableBytes != (3<<30)-(1<<30)+(256<<20) {
		t.Fatalf("v2 %+v %v", s, err)
	}
	writeFile(t, filepath.Join(unit, "memory.max"), "max\n")
	if s, _ := r.System(); s.Source != "meminfo" || s.AvailableBytes != 6<<30 {
		t.Fatalf("unlimited v2 %+v", s)
	}

	writeFile(t, filepath.Join(proc, "self", "cgroup"), "4:memory:/app\n1:name=systemd:/app\n")
	v1 := filepath.Join(cg, "memory", "app")
	writeFile(t, filepath.Join(v1, "memory.limit_in_bytes"), "2147483648\n")
	writeFile(t, filepath.Join(v1, "memory.usage_in_bytes"), "1073741824\n")
	if s, _ := r.System(); s.Source != "cgroup1" || s.AvailableBytes != 1<<30 {
		t.Fatalf("v1 %+v", s)
	}
	writeFile(t, filepath.Join(v1, "memory.limit_in_bytes"), "9223372036854771712\n")
	if s, _ := r.System(); s.Source != "meminfo" {
		t.Fatalf("unlimited v1 %+v", s)
	}
}

func TestLiveReaderReadsThisProcess(t *testing.T) {
	r := NewReader()
	s, err := r.System()
	if err != nil || s.TotalBytes == 0 || s.AvailableBytes == 0 || s.AvailableBytes > s.TotalBytes {
		t.Fatalf("%+v %v", s, err)
	}
	procs, err := r.Processes()
	if err != nil || len(procs) == 0 {
		t.Fatal("process table")
	}
	if total, n := TreeRSS(r, procs, os.Getpid()); total == 0 || n < 1 {
		t.Fatal("own RSS")
	}
}
