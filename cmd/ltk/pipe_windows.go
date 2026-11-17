//go:build windows

package main

import (
	"errors"
	"syscall"
)

// errorNoData is Windows' ERROR_NO_DATA ("the pipe is being closed"), which a write to a pipe with no reader returns.
const errorNoData = syscall.Errno(232)

// ignoreSIGPIPE does nothing: Windows has no SIGPIPE; the write itself fails.
func ignoreSIGPIPE() {}

// isBrokenPipe reports whether err is the write error of a pipe whose reader is gone.
func isBrokenPipe(err error) bool {
	return errors.Is(err, syscall.ERROR_BROKEN_PIPE) || errors.Is(err, errorNoData) || errors.Is(err, syscall.EPIPE)
}
