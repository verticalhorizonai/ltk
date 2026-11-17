package main

// The Exit code mapper: the one place an error becomes an exit code of contract.EXIT_CODES and the one ErrorLine on
// standard error.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// errorLineMapper implements exitMapper: nothing on standard error and exit 0 for a nil error or a closed standard
// output; otherwise exactly one ErrorLine and the exit code its ErrorCode corresponds to.
type errorLineMapper struct{}

// exitCode maps err and writes its ErrorLine.
//   - nil: exit 0, nothing written.
//   - standard output closed by its reader (errStdoutClosed anywhere in the chain): exit 0, nothing written: a reader
//     that stops reading early (`ltk list corpus/ | head`) is not a failure.
//   - a cancellation of the command's context (an interrupt): failure, the message saying the command was interrupted.
//   - a *model.CodedError anywhere in the chain: its code's exit code; the message is err's text.
//   - any other error: failure, with the error's text.
func (errorLineMapper) exitCode(err error, stderr io.Writer) contract.ExitCode {
	if err == nil || errors.Is(err, errStdoutClosed) {
		return contract.EXIT_CODES.Success
	}
	line := contract.ErrorLine{Code: contract.ErrorCodeFailure, Message: err.Error()}
	var coded *model.CodedError
	switch {
	case errors.Is(err, context.Canceled):
		line.Message = "interrupted before the command finished: " + err.Error()
	case errors.As(err, &coded):
		line.Code = coded.Code // the message is the whole chain's text, any context wrapped around the coded error kept
	}
	code, known := exitCodeOf[line.Code]
	if !known {
		// A code outside the ErrorCode union is a defect of the code that made it; it is still reported as a failure.
		line.Code, code = contract.ErrorCodeFailure, contract.EXIT_CODES.Failure
	}
	if line.Message == "" {
		line.Message = string(line.Code) // the ErrorLine's message is never empty
	}
	writeErrorLine(stderr, line)
	return code
}

// exitCodeOf maps each ErrorCode to its exit code, one to one.
var exitCodeOf = map[contract.ErrorCode]contract.ExitCode{
	contract.ErrorCodeFailure:            contract.EXIT_CODES.Failure,
	contract.ErrorCodeUsageError:         contract.EXIT_CODES.UsageError,
	contract.ErrorCodeEnvironmentMissing: contract.EXIT_CODES.EnvironmentMissing,
}

// writeErrorLine writes the ErrorLine as one JSON object and one \n, in one write. A failed write of standard error
// cannot be reported anywhere and is ignored.
func writeErrorLine(stderr io.Writer, line contract.ErrorLine) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // `<folder>` in a usage message stays readable; the line is the same JSON either way
	if err := enc.Encode(line); err != nil {
		return
	}
	_, _ = stderr.Write(buf.Bytes())
}
