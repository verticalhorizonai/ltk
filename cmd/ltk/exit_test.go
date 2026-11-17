package main

// The Exit code mapper's table test: each error kind in process, its exit code and its error line. The process-level
// rows, which run the built program, are streams_test.go's.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// assertErrorLine holds stderr to the ErrorLine's shape: exactly one line, ending in \n, one
// JSON object with exactly the keys code and message, code a value of the ErrorCode union, message non-empty.
func assertErrorLine(t *testing.T, stderr []byte, wantCode contract.ErrorCode) contract.ErrorLine {
	t.Helper()
	if !bytes.HasSuffix(stderr, []byte("\n")) || bytes.Count(stderr, []byte("\n")) != 1 {
		t.Fatalf("stderr is not exactly one line ending in \\n: %q", stderr)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stderr, &raw); err != nil {
		t.Fatalf("stderr is not one JSON object: %v: %q", err, stderr)
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"code", "message"}) {
		t.Fatalf("the error line's keys are %v, want exactly [code message]: %q", keys, stderr)
	}
	var line contract.ErrorLine
	dec := json.NewDecoder(bytes.NewReader(stderr))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&line); err != nil {
		t.Fatalf("the error line does not decode as an ErrorLine: %v", err)
	}
	if !slices.Contains(contract.Unions["ErrorCode"], string(line.Code)) {
		t.Fatalf("code %q is not a value of the ErrorCode union %v", line.Code, contract.Unions["ErrorCode"])
	}
	if line.Code != wantCode {
		t.Fatalf("code %q, want %q", line.Code, wantCode)
	}
	if line.Message == "" {
		t.Fatal("the error line's message is empty")
	}
	return line
}

func TestExitCodeMapper(t *testing.T) {
	closedPipe := stdoutWriter{w: writerFunc(func([]byte) (int, error) {
		return 0, &os.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EPIPE}
	})}
	_, epipe := closedPipe.Write([]byte("x"))
	if !errors.Is(epipe, errStdoutClosed) {
		t.Fatalf("a broken-pipe write of standard output must be errStdoutClosed, got %v", epipe)
	}
	cases := []struct {
		name     string
		err      error
		code     contract.ExitCode
		line     contract.ErrorCode // "" when nothing is written
		contains string
	}{
		{"success", nil, 0, "", ""},
		{"standard output closed by its reader", epipe, 0, "", ""},
		{"standard output closed, inside a failure", model.Failure("write the report: %w", epipe), 0, "", ""},
		{"check or reproduction failed", model.Failure("entry %s failed", "e1"), 1, contract.ErrorCodeFailure, "entry e1 failed"},
		{"usage error", model.Usage("unknown flag %q", "--bogus"), 2, contract.ErrorCodeUsageError, `unknown flag "--bogus"`},
		{"environment missing", model.Environment("docker CLI not found on PATH"), 3, contract.ErrorCodeEnvironmentMissing, "docker CLI not found on PATH"},
		{"usage error wrapped", fmt.Errorf("parse: %w", model.Usage("missing argument <folder>")), 2, contract.ErrorCodeUsageError, "parse: missing argument <folder>"},
		{"environment error wrapping a cause", model.Environment("Docker daemon not reachable: %w", errors.New("Cannot connect")), 3, contract.ErrorCodeEnvironmentMissing, "Docker daemon not reachable: Cannot connect"},
		{"uncoded error", errors.New("disk full"), 1, contract.ErrorCodeFailure, "disk full"},
		{"interrupt", fmt.Errorf("start the network: %w", context.Canceled), 1, contract.ErrorCodeFailure, "interrupted"},
		{"interrupt inside a coded error", model.Environment("docker version: %w", context.Canceled), 1, contract.ErrorCodeFailure, "interrupted"},
		{"coded error with no message", &model.CodedError{Code: contract.ErrorCodeUsageError}, 2, contract.ErrorCodeUsageError, "usage_error"},
		{"code outside the union", &model.CodedError{Code: "bogus", Message: "m"}, 1, contract.ErrorCodeFailure, "m"},
		{"uncoded error with no text", errors.New(""), 1, contract.ErrorCodeFailure, "failure"},
		{"message with a newline stays one line", model.Failure("first\nsecond"), 1, contract.ErrorCodeFailure, "first\nsecond"},
		// A quickstart that never became ready is a failure, the container's last log lines in the
		// message; the absent and unpullable image is an environment error (envcheck_test.go, check_test.go).
		{"quickstart readiness error", fmt.Errorf("start the network: %w", &orchestration.ReadinessError{Container: "ltk-0a1b2c3d-core",
			Waited: 5 * time.Minute, Stage: "RPC healthy", Last: errors.New("connection refused"), Logs: "core: catching up\ncore: stuck"}),
			1, contract.ErrorCodeFailure, "waiting for RPC healthy): connection refused; last log lines:\ncore: catching up\ncore: stuck"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stderr bytes.Buffer
			got := errorLineMapper{}.exitCode(c.err, &stderr)
			if got != c.code {
				t.Errorf("exit code %d, want %d", got, c.code)
			}
			if c.line == "" {
				if stderr.Len() != 0 {
					t.Errorf("stderr %q, want nothing", stderr.String())
				}
				return
			}
			line := assertErrorLine(t, stderr.Bytes(), c.line)
			if !strings.Contains(line.Message, c.contains) {
				t.Errorf("message %q does not contain %q", line.Message, c.contains)
			}
		})
	}
}

// TestExitCodesOneToOne: each ErrorCode maps to its own non-zero exit code of EXIT_CODES, and the mapper's table
// covers the whole union.
func TestExitCodesOneToOne(t *testing.T) {
	want := map[contract.ErrorCode]contract.ExitCode{
		contract.ErrorCodeFailure:            contract.EXIT_CODES.Failure,
		contract.ErrorCodeUsageError:         contract.EXIT_CODES.UsageError,
		contract.ErrorCodeEnvironmentMissing: contract.EXIT_CODES.EnvironmentMissing,
	}
	if len(exitCodeOf) != len(contract.Unions["ErrorCode"]) {
		t.Fatalf("the mapper maps %d codes, the union has %d", len(exitCodeOf), len(contract.Unions["ErrorCode"]))
	}
	for code, exit := range want {
		if exitCodeOf[code] != exit {
			t.Errorf("%s → %d, want %d", code, exitCodeOf[code], exit)
		}
		var stderr bytes.Buffer
		if got := (errorLineMapper{}).exitCode(&model.CodedError{Code: code, Message: "m"}, &stderr); got != exit {
			t.Errorf("a %s error exits %d, want %d", code, got, exit)
		}
	}
	if contract.EXIT_CODES.Success != 0 || contract.EXIT_CODES.Failure != 1 || contract.EXIT_CODES.UsageError != 2 ||
		contract.EXIT_CODES.EnvironmentMissing != 3 {
		t.Errorf("EXIT_CODES %+v, want 0 1 2 3 (success, failure, usage error, environment missing)", contract.EXIT_CODES)
	}
}

// TestStdoutWriterPassesOtherErrors: only a broken pipe is the reader's close; any other write error is returned as
// it is.
func TestStdoutWriterPassesOtherErrors(t *testing.T) {
	other := errors.New("no space left on device")
	_, err := stdoutWriter{w: writerFunc(func([]byte) (int, error) { return 0, other })}.Write([]byte("x"))
	if !errors.Is(err, other) || errors.Is(err, errStdoutClosed) {
		t.Errorf("got %v", err)
	}
	var buf bytes.Buffer
	if n, err := (stdoutWriter{w: &buf}).Write([]byte("data")); n != 4 || err != nil || buf.String() != "data" {
		t.Errorf("a plain write: %d %v %q", n, err, buf.String())
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

var _ io.Writer = writerFunc(nil)
