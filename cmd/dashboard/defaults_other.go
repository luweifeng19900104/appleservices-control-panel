//go:build !windows

package main

import (
	"os"
	"syscall"
)

// stopSignals adds SIGTERM, which is how systemd and kill stop the service.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// Linux defaults, relative to the release directory the dashboard runs in
// (/data/appleservices/current under systemd). The pinned OpenBubbles Linux
// module is not shipped in the release; install.sh / setup-native.sh download
// it to /data/appleservices/native and link it as ./native inside the release.
// Workers start with -native-adi, as on Windows; push uses the Go client with
// the account-private certificate written by tools/mac-push-check.py.
const (
	defaultWorker        = "./bin/trusted-device"
	defaultLibrary       = "./native/librust_lib_bluebubbles.so"
	defaultPython        = "python3"
	defaultMacPushSetup  = "./tools/mac-push-check.py"
	defaultCredentialKey = "/etc/appleservices/credential.key"
	defaultNativeADI     = true
)
