//go:build windows

package main

import "os"

// stopSignals is unchanged from the original: Ctrl+C only.
var stopSignals = []os.Signal{os.Interrupt}

// Windows defaults are the original ones: the GUI worker, the pinned
// OpenBubbles module and the Mac APS bootstrap, all relative to the app root.
const (
	defaultWorker        = "./bin/trusted-device-gui.exe"
	defaultLibrary       = "./.cache/openbubbles-store/rust_lib_bluebubbles.dll"
	defaultPython        = "python"
	defaultMacPushSetup  = "./tools/mac-push-check.py"
	defaultCredentialKey = ""
	defaultNativeADI     = true
)
