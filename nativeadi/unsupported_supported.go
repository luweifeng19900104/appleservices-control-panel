//go:build windows || linux

package nativeadi

import "errors"

// ErrUnsupported exists on every platform so callers can test for it with
// errors.Is. Windows x64 supports the native bridge, so it is never returned
// there; ValidateLibrary keeps its original Windows x64 checks.
var ErrUnsupported = errors.New("nativeadi: native ADI is not supported on this platform")
