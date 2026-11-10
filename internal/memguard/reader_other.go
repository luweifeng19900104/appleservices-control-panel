//go:build !linux && !windows

package memguard

import "errors"

type unsupportedReader struct{}

// NewReader returns a reader that reports memory as unreadable. The guard then
// admits every worker and never pauses one.
func NewReader() Reader { return unsupportedReader{} }

var errUnsupported = errors.New("memguard: memory readings are not implemented on this platform")

func (unsupportedReader) System() (System, error)       { return System{}, errUnsupported }
func (unsupportedReader) Processes() ([]Process, error) { return nil, errUnsupported }
func (unsupportedReader) RSS(int) (uint64, error)       { return 0, errUnsupported }
