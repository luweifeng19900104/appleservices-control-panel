//go:build windows

package platform

// ExeSuffix is appended to helper executable names.
const ExeSuffix = ".exe"

func current() Capabilities {
	return Capabilities{OS: Name, NativeADI: true, NativePush: true, MacPushBootstrap: true}
}
