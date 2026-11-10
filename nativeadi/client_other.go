//go:build !windows && !linux

package nativeadi

import (
	"context"
	"errors"
	"runtime"
)

// ErrUnsupported is returned on platforms without a native ADI implementation.
// The Windows bridge loads a pinned Windows PE module through msvcrt/kernel32
// and a MinGW-built ws2_32 import fence; none of that exists on Linux. Phase A
// of docs/LINUX-MIGRATION-PLAN.md replaces this stub with a Linux helper.
var ErrUnsupported = errors.New("nativeadi: native ADI header and six-digit code generation is not yet supported on " + runtime.GOOS + " (Linux migration phase A pending)")

// CheckRuntime reports that the native bridge is unavailable on this platform.
func CheckRuntime(python string) error { return ErrUnsupported }

// ValidateLibrary reports that no native module can be used on this platform.
func ValidateLibrary(path string) error { return ErrUnsupported }

// Start never succeeds on this platform. NewClient already refuses, so this is
// only reached by code that builds a Client by hand.
func (c *Client) Start(ctx context.Context) error { return ErrUnsupported }
