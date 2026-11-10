//go:build linux

package memguard

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type linuxReader struct {
	proc, cgroupRoot string
}

// NewReader returns the Linux reader: /proc/meminfo MemAvailable, the
// process's cgroup (v2 memory.max/memory.current, v1 limit/usage as fallback)
// and per-process VmRSS from /proc/<pid>/status.
func NewReader() Reader { return linuxReader{proc: "/proc", cgroupRoot: "/sys/fs/cgroup"} }

func (r linuxReader) System() (System, error) {
	data, err := os.ReadFile(filepath.Join(r.proc, "meminfo"))
	if err != nil {
		return System{}, err
	}
	host, err := parseMeminfo(data)
	if err != nil {
		return System{}, err
	}
	cgroup, _ := os.ReadFile(filepath.Join(r.proc, "self", "cgroup"))
	if limit, usage, source, ok := r.cgroupMemory(cgroup); ok {
		return EffectiveSystem(host, limit, usage, source), nil
	}
	return host, nil
}

func parseMeminfo(data []byte) (System, error) {
	var total, available, free, buffers, cached uint64
	haveAvailable := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		value *= 1024 // kB
		switch fields[0] {
		case "MemTotal:":
			total = value
		case "MemAvailable:":
			available, haveAvailable = value, true
		case "MemFree:":
			free = value
		case "Buffers:":
			buffers = value
		case "Cached:":
			cached = value
		}
	}
	if total == 0 {
		return System{}, errors.New("memguard: MemTotal missing from /proc/meminfo")
	}
	if !haveAvailable {
		// Kernels before 3.14 lack MemAvailable; this is the usual estimate.
		available = free + buffers + cached
	}
	return System{TotalBytes: total, AvailableBytes: available, Source: "meminfo"}, nil
}

// cgroupMemory returns the memory limit and usage of this process's cgroup, if
// it is limited. v2 subtracts reclaimable inactive file cache from usage.
func (r linuxReader) cgroupMemory(self []byte) (limit, usage uint64, source string, ok bool) {
	var v2Path, v1Path string
	for _, line := range strings.Split(string(self), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] == "0" && parts[1] == "" {
			v2Path = parts[2]
		}
		for _, controller := range strings.Split(parts[1], ",") {
			if controller == "memory" {
				v1Path = parts[2]
			}
		}
	}
	if v2Path != "" {
		dir := filepath.Join(r.cgroupRoot, filepath.Clean("/"+v2Path))
		max, err := readTrim(filepath.Join(dir, "memory.max"))
		if err == nil && max != "max" {
			if l, err := strconv.ParseUint(max, 10, 64); err == nil {
				if cur, err := readUint(filepath.Join(dir, "memory.current")); err == nil {
					if stat, err := os.ReadFile(filepath.Join(dir, "memory.stat")); err == nil {
						if inactive, found := statField(stat, "inactive_file"); found && inactive < cur {
							cur -= inactive
						}
					}
					return l, cur, "cgroup2", true
				}
			}
		}
	}
	if v1Path != "" {
		dir := filepath.Join(r.cgroupRoot, "memory", filepath.Clean("/"+v1Path))
		l, err := readUint(filepath.Join(dir, "memory.limit_in_bytes"))
		// An unlimited v1 group reports a huge page-aligned number.
		if err == nil && l < 1<<60 {
			if cur, err := readUint(filepath.Join(dir, "memory.usage_in_bytes")); err == nil {
				if stat, err := os.ReadFile(filepath.Join(dir, "memory.stat")); err == nil {
					if inactive, found := statField(stat, "total_inactive_file"); found && inactive < cur {
						cur -= inactive
					}
				}
				return l, cur, "cgroup1", true
			}
		}
	}
	return 0, 0, "", false
}

func statField(data []byte, name string) (uint64, bool) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == name {
			v, err := strconv.ParseUint(fields[1], 10, 64)
			return v, err == nil
		}
	}
	return 0, false
}

func readTrim(path string) (string, error) {
	data, err := os.ReadFile(path)
	return strings.TrimSpace(string(data)), err
}

func readUint(path string) (uint64, error) {
	text, err := readTrim(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(text, 10, 64)
}

func (r linuxReader) Processes() ([]Process, error) {
	entries, err := os.ReadDir(r.proc)
	if err != nil {
		return nil, err
	}
	var out []Process
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.proc, entry.Name(), "stat"))
		if err != nil {
			continue
		}
		if ppid, ok := parseStatPPID(data); ok {
			out = append(out, Process{PID: pid, PPID: ppid})
		}
	}
	return out, nil
}

// parseStatPPID reads field 4 of /proc/<pid>/stat. The command name (field 2)
// may contain spaces and parentheses, so parsing starts after the last ')'.
func parseStatPPID(data []byte) (int, bool) {
	end := bytes.LastIndexByte(data, ')')
	if end < 0 {
		return 0, false
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	return ppid, err == nil
}

func (r linuxReader) RSS(pid int) (uint64, error) {
	data, err := os.ReadFile(filepath.Join(r.proc, strconv.Itoa(pid), "status"))
	if err != nil {
		return 0, err
	}
	return parseStatusRSS(data)
}

func parseStatusRSS(data []byte) (uint64, error) {
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				v, err := strconv.ParseUint(fields[1], 10, 64)
				if err == nil {
					return v * 1024, nil
				}
			}
		}
	}
	// Kernel threads and zombies have no VmRSS.
	return 0, nil
}
