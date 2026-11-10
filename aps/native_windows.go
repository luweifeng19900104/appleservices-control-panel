//go:build windows

package aps

import (
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"

	"github.com/Laky-64/appleservices/internal/backgroundexec"
)

// Windows implementation of the iCloud for Windows APSDaemon bridge, moved
// unchanged from native.go so other platforms can provide stubs.

//go:embed native_push.cs
var nativeBridge string

func NewNativeClient() (*NativeClient, error) {
	if runtime.GOOS != "windows" {
		return nil, errors.New("aps: native push requires Windows with iCloud installed")
	}
	return &NativeClient{ready: make(chan nativeReady, 1), events: make(chan Event, 16),
		selftest: make(chan struct{}, 1), done: make(chan struct{})}, nil
}

func nativeCommand(probe bool) (*exec.Cmd, error) {
	root := os.Getenv("SystemRoot")
	if !filepath.IsAbs(root) {
		return nil, errors.New("aps: Windows system directory is unavailable")
	}
	flag := "$false"
	if probe {
		flag = "$true"
	}
	script := "$ErrorActionPreference='Stop'\nAdd-Type -TypeDefinition @'\n" + nativeBridge +
		"\n'@\n[AppleServicesNativePush.Bridge]::Run(" + flag + ")\n"
	units := utf16.Encode([]rune(script))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[2*i:], unit)
	}
	// Only embedded program text goes to PowerShell. No credentials, notification
	// contents, provider paths, or user supplied shell expressions are arguments.
	command := exec.Command(filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
		"-NoLogo", "-NoProfile", "-NonInteractive", "-Mta", "-EncodedCommand", base64.StdEncoding.EncodeToString(data))
	backgroundexec.Configure(command)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "APPLESERVICES_APPLEID") && !strings.EqualFold(key, "APPLESERVICES_PASSWORD") {
			command.Env = append(command.Env, entry)
		}
	}
	return command, nil
}
