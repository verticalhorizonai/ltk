//go:build unix

package main

import (
	"errors"
	"os/signal"
	"syscall"
)

// ignoreSIGPIPE keeps a write to a closed standard output from killing the program: the write returns EPIPE instead,
// which stdoutWriter reports.
func ignoreSIGPIPE() { signal.Ignore(syscall.SIGPIPE) }

// isBrokenPipe reports whether err is the write error of a pipe whose reader is gone.
func isBrokenPipe(err error) bool { return errors.Is(err, syscall.EPIPE) }
