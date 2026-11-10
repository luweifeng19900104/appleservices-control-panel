//go:build !windows

package aps

import (
	"errors"
	"os/exec"
	"runtime"
)

// ErrNativePushUnsupported is returned where the iCloud for Windows APSDaemon
// COM bridge is requested on another platform. Phase A of the Linux migration
// plan replaces it with the Go TLS transport plus a Linux certificate bootstrap.
var ErrNativePushUnsupported = errors.New("aps: iCloud for Windows native push is not available on " + runtime.GOOS + "; use the Go APS client")

func NewNativeClient() (*NativeClient, error) { return nil, ErrNativePushUnsupported }

func nativeCommand(probe bool) (*exec.Cmd, error) { return nil, ErrNativePushUnsupported }
