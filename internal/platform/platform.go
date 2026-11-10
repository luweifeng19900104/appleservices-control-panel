// Package platform names what the running operating system supports, so shared
// code can keep one implementation and only branch where a platform genuinely
// differs. The Windows answers reproduce the original Windows-only behaviour.
package platform

import "runtime"

// Name is the operating system this binary was built for ("windows", "linux").
const Name = runtime.GOOS

// Exe returns an executable file name with the platform suffix (".exe" on
// Windows, nothing elsewhere).
func Exe(name string) string { return name + ExeSuffix }

// Capabilities reports which native integrations are available. They are
// rendered by the dashboard so an unsupported feature is shown as such instead
// of failing later with an unrelated error.
type Capabilities struct {
	OS string `json:"os"`
	// NativeADI is local anisette header and six-digit code generation through
	// the platform-pinned OpenBubbles module.
	NativeADI bool `json:"native_adi"`
	// NativePush is the iCloud for Windows APSDaemon COM bridge.
	NativePush bool `json:"native_push"`
	// MacPushBootstrap is the account-private Mac APS certificate bootstrap.
	MacPushBootstrap bool `json:"mac_push_bootstrap"`
	// Note is a short Chinese explanation of what is missing, or empty.
	Note string `json:"note,omitempty"`
}

// Current returns the capabilities of this build.
func Current() Capabilities { return current() }
