package main

// The Stream and exit-code contract test. The exit codes are 0 success, 1 a check or a reproduction failed, 2 a usage
// error, 3 the environment is missing something the command needs; data goes to standard output and messages for the
// person go to standard error. The test holds every kind of command outcome to that contract.
//
// One table of process-level rows, each the built program (or, for `ltk repro`, the test binary as the program with a
// fake runner in the package's seam, repro_test.go) run with its arguments and environment:
//   - exit 2: nothing on standard output, one usage_error ErrorLine; the precedence rule: a usage problem is
//     exit 2 before the environment check, with no docker on PATH or a fake whose daemon is down, which is then never
//     called;
//   - exit 0: the command's data on standard output, nothing on standard error; a JSON output is exactly one JSON value
//     (a json.Decoder reads one, then io.EOF) that validates against its schema;
//   - exit 1: the command's report on standard output (one JSON value, valid) and exactly one failure ErrorLine on
//     standard error, which holds no report line;
//   - exit 3: nothing on standard output, one environment_missing ErrorLine naming what is missing;
//   - a reader that closes the pipe: every row that prints data is run a second time with standard output a pipe whose
//     reader is closed before the program starts: exit 0 and nothing on standard error.
//
// This file has no build tag: its rows run in the unit tier on every system with a go tool, and in the integration tier
// too. The `ltk check` rows here run with the fake docker, whose stellar-xdr reads the corpus: they need no Docker
// (as check_test.go's rows do). The rows with a real Docker are streams_integration_test.go's.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// streamRow is one row of the table.
type streamRow struct {
	name string
	// cmd builds the invocation; the fake engine is returned when the row has one (nil otherwise).
	cmd func(t *testing.T) (*exec.Cmd, *fakeEngine)
	// exit is the expected exit code.
	exit contract.ExitCode
	// schema is the output's schema fixture when the output is JSON; "" for text (or nothing, on exit 2 and 3).
	schema string
	// inMessage is a part of the ErrorLine's message (exit 1, 2 and 3).
	inMessage string
	// untouched is true when the fake must not have been called at all (the precedence rule).
	untouched bool
}

// ltkRow is an invocation of the built program in dir ("" for the test's own; "root" for the module root) with env.
func ltkRow(dir string, env func(t *testing.T) ([]string, *fakeEngine), args ...string) func(t *testing.T) (*exec.Cmd, *fakeEngine) {
	return func(t *testing.T) (*exec.Cmd, *fakeEngine) {
		t.Helper()
		e, f := env(t)
		d := dir
		switch dir {
		case "root":
			d = acceptRoot(t)
		case "tmp":
			d = t.TempDir()
		}
		return ltkCommand(t, d, e, expandTemp(t, args)...), f
	}
}

// expandTemp replaces "<tmp>" in each argument with a fresh temporary folder.
func expandTemp(t *testing.T, args []string) []string {
	out := make([]string, len(args))
	var tmp string
	for i, a := range args {
		if strings.Contains(a, "<tmp>") && tmp == "" {
			tmp = t.TempDir()
		}
		out[i] = strings.ReplaceAll(a, "<tmp>", tmp)
	}
	return out
}

// The rows' environments.
func inherit(*testing.T) ([]string, *fakeEngine)         { return nil, nil }
func withoutDocker(t *testing.T) ([]string, *fakeEngine) { return noDockerEnv(t), nil }
func withFake(modes, corpus string) func(t *testing.T) ([]string, *fakeEngine) {
	return func(t *testing.T) ([]string, *fakeEngine) {
		c := corpus
		if c == "root" {
			c = filepath.Join(acceptRoot(t), "corpus")
		}
		f := newFakeEngine(t, modes, c)
		return f.env, f
	}
}
func withFakeNetwork(t *testing.T) ([]string, *fakeEngine) {
	f, _ := generateEngine(t, "")
	return f.env, f
}

// reproRow is `ltk repro target` in the helper process with the fake runner of scenario.
func reproRow(scenario string, target contract.ReproTarget) func(t *testing.T) (*exec.Cmd, *fakeEngine) {
	return func(t *testing.T) (*exec.Cmd, *fakeEngine) {
		t.Helper()
		requireHelper(t)
		h := newReproHelper(t, scenario, "")
		return h.command(target), h.f
	}
}

// streamRows is the table.
func streamRows() []streamRow {
	const (
		usage    = contract.ExitCode(2)
		env      = contract.ExitCode(3)
		failure  = contract.ExitCode(1)
		success  = contract.ExitCode(0)
		valid    = fixtureCorpus + "valid"
		hash     = fixtureCorpus + "hash"
		wb       = contract.ReproTargetWalletBackend729
		js       = contract.ReproTargetJsSdk1766
		checkSch = "check-report.schema.json"
		genSch   = "generate-report.schema.json"
		cmpSch   = "compare-report.schema.json"
	)
	return []streamRow{
		// exit 2: usage errors, and the precedence rule (a usage error comes before the environment check).
		{"no arguments", ltkRow("", inherit), usage, "", "", false},
		{"an unknown command", ltkRow("", inherit, "frobnicate"), usage, "", "", false},
		{"list with an unknown flag", ltkRow("root", withoutDocker, "list", corpusArg, "--bogus"), usage, "", "flag provided but not defined", false},
		{"list with no folder", ltkRow("", withoutDocker, "list"), usage, "", "", false},
		{"compare with one folder", ltkRow("root", withoutDocker, "compare", corpusArg), usage, "", "", false},
		{"repro with an unknown name", ltkRow("tmp", withoutDocker, "repro", "nope"), usage, "", "", false},
		{"repro with no name", ltkRow("tmp", withoutDocker, "repro"), usage, "", "", false},
		{"generate with an unknown flag, no docker on PATH (precedence)", ltkRow("", withoutDocker, "generate", "--bogus", "--all", "--out", "<tmp>/out"), usage, "", "flag provided but not defined: -bogus", false},
		{"generate with an unknown flag, the daemon down (precedence)", ltkRow("", withFake("no-daemon", ""), "generate", "--bogus", "--all", "--out", "<tmp>/out"), usage, "", "flag provided but not defined: -bogus", true},
		{"generate with --all and --shape, no docker (precedence)", ltkRow("", withoutDocker, "generate", "--all", "--shape", entry1, "--out", "<tmp>/out"), usage, "", "not both", false},
		{"generate with an unknown --shape, no docker (precedence)", ltkRow("", withoutDocker, "generate", "--shape", "cap-0000-nothing", "--out", "<tmp>/out"), usage, "", "unknown --shape entry id: cap-0000-nothing", false},
		{"check with an unknown flag, no docker (precedence)", ltkRow("root", withoutDocker, "check", "--bogus", corpusArg), usage, "", "flag provided but not defined: -bogus", false},
		{"check with no folder, no docker (precedence)", ltkRow("root", withoutDocker, "check"), usage, "", "", false},
		{"check of a folder that does not exist, the daemon down (precedence)", ltkRow("tmp", withFake("no-daemon", ""), "check", "no-such-folder/"), usage, "", "", true},
		{"repro js-sdk-1766 with an extra argument, no docker (precedence)", ltkRow("tmp", withoutDocker, "repro", string(js), "extra"), usage, "", `unexpected argument "extra"`, false},
		{"repro wallet-backend-729 with a flag, the daemon down (precedence)", ltkRow("tmp", withFake("no-daemon", ""), "repro", string(wb), "--deadline=1"), usage, "", "", true},

		// exit 0: data on stdout, nothing on stderr.
		{"--help", ltkRow("", inherit, "--help"), success, "", "", false},
		{"list corpus/", ltkRow("root", withoutDocker, "list", corpusArg), success, "", "", false},
		{"list corpus/ --json", ltkRow("root", withoutDocker, "list", corpusArg, "--json"), success, "list-output.schema.json", "", false},
		{"compare corpus/ corpus/", ltkRow("root", withoutDocker, "compare", corpusArg, corpusArg), success, cmpSch, "", false},
		{"compare of the ledger-only pair", ltkRow("", withoutDocker, "compare", fixtureLedgerOnlyA, fixtureLedgerOnlyB), success, cmpSch, "", false},
		{"check corpus/ with the fake docker", ltkRow("root", withFake("", "root"), "check", corpusArg), success, checkSch, "", false},
		{"generate --shape with the fake network", ltkRow("", withFakeNetwork, "generate", "--shape", entry1, "--out", "<tmp>/out"), success, genSch, "", false},
		{"repro js-sdk-1766 passed", reproRow("js-passed", js), success, "js-sdk-1766-report.schema.json", "", false},
		{"repro wallet-backend-729 passed", reproRow("wb-passed", wb), success, "wallet-backend-729-report.schema.json", "", false},

		// exit 1: the report on stdout, one failure ErrorLine on stderr.
		{"compare of the reproduced-value-changed pair", ltkRow("", withoutDocker, "compare", fixtureReproducedA, fixtureReproducedB), failure, cmpSch, "", false},
		{"check of a corpus whose hash differs, with the fake docker", ltkRow("", withFake("", hash), "check", hash), failure, checkSch, "file_integrity: the hash differs", false},
		{"check of a valid corpus whose tool container is not removed", ltkRow("", withFake("rm-fails", valid), "check", valid), failure, checkSch, "", false},
		{"generate --all with shapes refused at both protocols", ltkRow("", withFakeNetwork, "generate", "--all", "--out", "<tmp>/out"), failure, genSch, "generation failed: 2 of 3 shapes not generated", false},
		{"repro js-sdk-1766 not reproduced", reproRow("js-not-reproduced", js), failure, "js-sdk-1766-report.schema.json", "not reproduced", false},
		{"repro wallet-backend-729 not reproduced", reproRow("wb-fix-not-ingested", wb), failure, "wallet-backend-729-report.schema.json", "reproduction failed", false},

		// exit 3: the environment is missing something.
		{"generate --all, no docker", ltkRow("", withoutDocker, "generate", "--all", "--out", "<tmp>/out"), env, "", "docker CLI not found on PATH", false},
		{"generate --shape, the daemon down", ltkRow("", withFake("no-daemon", ""), "generate", "--shape", entry1, "--out", "<tmp>/out"), env, "", "Docker daemon not reachable", false},
		{"check corpus/, Windows containers", ltkRow("root", withFake("windows", ""), "check", corpusArg), env, "", "Docker is not running Linux containers", false},
		{"repro wallet-backend-729, no docker", ltkRow("tmp", withoutDocker, "repro", string(wb)), env, "", "docker CLI not found on PATH", false},
		{"repro js-sdk-1766, its image cannot be pulled", ltkRow("tmp", withFake("no-image", ""), "repro", string(js)), env, "", "digest-pinned image cannot be pulled: " + orchestration.NodeImage, false},
	}
}

// TestProcessStreamsAndExitCodes runs the table.
func TestProcessStreamsAndExitCodes(t *testing.T) {
	for _, row := range streamRows() {
		t.Run(row.name, func(t *testing.T) {
			cmd, f := row.cmd(t)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			code := exitCodeOfRun(t, cmd.Run())
			if code != int(row.exit) {
				t.Fatalf("exit %d, want %d; stdout %q stderr %q", code, row.exit, stdout.Bytes(), stderr.Bytes())
			}
			out, errOut := stdout.Bytes(), stderr.Bytes()
			switch row.exit {
			case contract.EXIT_CODES.Success:
				if len(errOut) != 0 || len(out) == 0 {
					t.Errorf("stdout %d bytes, stderr %q; want the data and nothing", len(out), errOut)
				}
			case contract.EXIT_CODES.Failure:
				line := assertErrorLine(t, errOut, contract.ErrorCodeFailure)
				if !strings.Contains(line.Message, row.inMessage) {
					t.Errorf("message %q, want it to hold %q", line.Message, row.inMessage)
				}
			case contract.EXIT_CODES.UsageError, contract.EXIT_CODES.EnvironmentMissing:
				if len(out) != 0 {
					t.Errorf("stdout %q, want nothing", out)
				}
				want := contract.ErrorCodeUsageError
				if row.exit == contract.EXIT_CODES.EnvironmentMissing {
					want = contract.ErrorCodeEnvironmentMissing
				}
				line := assertErrorLine(t, errOut, want)
				if !strings.Contains(line.Message, row.inMessage) {
					t.Errorf("message %q, want it to hold %q", line.Message, row.inMessage)
				}
			}
			if row.schema != "" {
				oneJSONValue(t, out)
				validateOutput(t, row.schema, out)
			}
			if row.untouched && f != nil && len(f.calls()) != 0 {
				t.Errorf("the fake was called %v: a usage error comes before the environment check", f.calls())
			}
			if len(out) == 0 {
				return
			}
			// The same invocation into a pipe whose reader is gone: exit 0 and nothing on standard error.
			cmd, _ = row.cmd(t)
			if code, errOut := runIntoClosedReader(t, cmd); code != 0 || len(errOut) != 0 {
				t.Errorf("into a closed pipe: exit %d stderr %q; want 0 and nothing", code, errOut)
			}
		})
	}
}

// oneJSONValue fails unless b is exactly one JSON value: a json.Decoder reads one, then io.EOF.
func oneJSONValue(t *testing.T, b []byte) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	var v json.RawMessage
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("stdout is not a JSON value: %v: %q", err, b)
	}
	if err := dec.Decode(&v); !errors.Is(err, io.EOF) {
		t.Errorf("stdout holds more than one JSON value: %v", err)
	}
}

// runIntoClosedReader runs cmd with a standard output whose reader is closed before it starts, and returns its exit
// code and standard error. A reader that closes the pipe ends the run with exit 0 and nothing on standard error.
func runIntoClosedReader(t *testing.T, cmd *exec.Cmd) (int, []byte) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	errFile := filepath.Join(t.TempDir(), "stderr")
	errOut, err := os.Create(errFile)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = w, errOut
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	code := exitCodeOfRun(t, cmd.Wait())
	errOut.Close()
	stderr, _ := os.ReadFile(errFile)
	return code, stderr
}
