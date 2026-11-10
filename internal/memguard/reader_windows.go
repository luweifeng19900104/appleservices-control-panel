//go:build windows

package memguard

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	// K32GetProcessMemoryInfo is the kernel32 export of psapi's
	// GetProcessMemoryInfo (Windows 7 and later).
	procGetProcessMemoryInfo = kernel32.NewProc("K32GetProcessMemoryInfo")
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

type windowsReader struct{}

// NewReader returns the Windows reader: GlobalMemoryStatusEx for the machine,
// the process working set for each process, and a Toolhelp snapshot for
// parent/child relations.
func NewReader() Reader { return windowsReader{} }

func (windowsReader) System() (System, error) {
	var status memoryStatusEx
	status.Length = uint32(unsafe.Sizeof(status))
	ok, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if ok == 0 {
		return System{}, err
	}
	return System{TotalBytes: status.TotalPhys, AvailableBytes: status.AvailPhys, Source: "GlobalMemoryStatusEx"}, nil
}

func (windowsReader) Processes() ([]Process, error) {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer syscall.CloseHandle(snapshot)
	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := syscall.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}
	var out []Process
	for {
		out = append(out, Process{PID: int(entry.ProcessID), PPID: int(entry.ParentProcessID)})
		if err := syscall.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return out, nil
}

func (windowsReader) RSS(pid int) (uint64, error) {
	if pid <= 0 {
		return 0, errors.New("memguard: invalid pid")
	}
	// PROCESS_QUERY_LIMITED_INFORMATION | PROCESS_VM_READ
	handle, err := syscall.OpenProcess(0x1000|0x0010, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer syscall.CloseHandle(handle)
	if wait, err := syscall.WaitForSingleObject(handle, 0); err != nil || wait != syscall.WAIT_TIMEOUT {
		return 0, errors.New("memguard: process has exited")
	}
	var counters processMemoryCounters
	counters.CB = uint32(unsafe.Sizeof(counters))
	ok, _, callErr := procGetProcessMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.CB))
	if ok == 0 {
		return 0, callErr
	}
	return uint64(counters.WorkingSetSize), nil
}
