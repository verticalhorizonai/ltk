// Package model holds the entry and expected-value models of the corpus: the seed entry definitions (seeds.go), the
// expected-value grouping table (grouping.go), and the typed errors every package returns. It needs no container and
// imports internal/contract and, for the account keys, the keypair package of Stellar's Go SDK.
package model

import (
	"errors"
	"fmt"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// CodedError is an error that carries the ErrorCode of the standard-error line. The exit-code mapper of cmd/ltk turns
// its code into the exit code of contract.EXIT_CODES and its message into the ErrorLine's message. An error that is
// not a CodedError is a failure.
type CodedError struct {
	// Code is the ErrorLine's code.
	Code contract.ErrorCode
	// Message is the ErrorLine's message: human-readable; for environment_missing it says what is missing.
	Message string
	// Err is the wrapped cause, when there is one.
	Err error
}

// Error returns the message, or the wrapped error's text when the message is empty.
func (e *CodedError) Error() string {
	if e.Message == "" && e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}

// Unwrap returns the wrapped cause, so errors.Is and errors.As see through a CodedError.
func (e *CodedError) Unwrap() error { return e.Err }

// coded formats the message as fmt.Errorf does; a %w verb's operand becomes the wrapped cause (several %w verbs: the
// formatted error itself, which unwraps to each of them).
func coded(code contract.ErrorCode, format string, args ...any) error {
	formatted := fmt.Errorf(format, args...)
	cause := errors.Unwrap(formatted)
	if _, many := formatted.(interface{ Unwrap() []error }); many {
		cause = formatted
	}
	return &CodedError{Code: code, Message: formatted.Error(), Err: cause}
}

// Usage returns a usage error (exit 2): an unknown command, flag, argument, entry id or reproduction target.
func Usage(format string, args ...any) error {
	return coded(contract.ErrorCodeUsageError, format, args...)
}

// Environment returns an environment error (exit 3): the message names what the command needs and is missing, such
// as Docker with Linux containers or a digest-pinned image.
func Environment(format string, args ...any) error {
	return coded(contract.ErrorCodeEnvironmentMissing, format, args...)
}

// Failure returns a failure (exit 1): a check, a generation or a reproduction failed.
func Failure(format string, args ...any) error {
	return coded(contract.ErrorCodeFailure, format, args...)
}
