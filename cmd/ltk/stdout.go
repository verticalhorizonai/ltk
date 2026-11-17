package main

// Standard output and a reader that closes the pipe: the program ignores SIGPIPE
// (pipe_unix.go), a write that fails because the reader is gone ends the command with errStdoutClosed, and the
// mapper turns that into exit 0 with nothing on standard error.

import (
	"errors"
	"fmt"
	"io"
)

// errStdoutClosed marks a write of standard output that failed because its reader closed the pipe.
var errStdoutClosed = errors.New("standard output closed by its reader")

// stdoutWriter is standard output as every command writes it.
type stdoutWriter struct {
	w io.Writer
}

// Write writes p; a broken-pipe error comes back wrapping errStdoutClosed, every other error as it is.
func (s stdoutWriter) Write(p []byte) (int, error) {
	n, err := s.w.Write(p)
	if err != nil && isBrokenPipe(err) {
		return n, fmt.Errorf("%w: %w", errStdoutClosed, err)
	}
	return n, err
}
