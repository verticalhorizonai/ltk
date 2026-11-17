package main

// `ltk check` at the process boundary in the unit tier: the built program with the fake docker of testdata/fakedocker
// on PATH, whose stellar-xdr reads the fixture corpus it is given. The success rows on real XDR are in the integration
// tier, because they need a real Docker; the rows here need no Docker and run on every system with a go tool
// (cmd/ltk/check_integration_test.go holds the real ones).

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

const fixtureCorpus = "../../testdata/corpus/"

// decodeReport decodes stdout as one CheckReport, validated against its schema (schemas/check-report.schema.json).
func decodeReport(t *testing.T, stdout []byte) contract.CheckReport {
	t.Helper()
	validateOutput(t, "check-report.schema.json", stdout)
	var r contract.CheckReport
	dec := json.NewDecoder(bytes.NewReader(stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("stdout is not a CheckReport: %v\n%s", err, stdout)
	}
	return r
}

// TestProcessCheckEnvironmentMissing: `ltk check` fails fast without its environment:
// without Docker with Linux containers, or with the quickstart image absent and unpullable, `ltk check` exits 3 before
// any check: nothing on stdout, one environment_missing ErrorLine naming what is missing, and the fake docker called
// with nothing but `version` (and, for the image, its inspection and pull). The daemon's timeout is in
// envcheck_test.go, with an injected deadline rather than a measured wait.
func TestProcessCheckEnvironmentMissing(t *testing.T) {
	cases := []struct {
		name, modes string
		noDocker    bool
		inMessage   string
		calls       []string
	}{
		{"docker CLI not on PATH", "", true, "Docker with Linux containers is not available: docker CLI not found on PATH", nil},
		{"daemon not reachable", "no-daemon", false, "Docker with Linux containers is not available: Docker daemon not reachable: Cannot connect to the Docker daemon", []string{"version"}},
		{"not Linux containers", "windows", false, "Docker with Linux containers is not available: Docker is not running Linux containers", []string{"version"}},
		{"quickstart image cannot be pulled", "no-image", false, "digest-pinned image cannot be pulled: " + orchestration.QuickstartCurrent, []string{"version", "image inspect", "pull"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var f *fakeEngine
			env := noDockerEnv(t) // no fake needed: this row runs on every system
			if !c.noDocker {
				f = newFakeEngine(t, c.modes, fixtureCorpus+"valid")
				env = f.env
			}
			code, stdout, stderr := runLTK(t, env, "check", fixtureCorpus+"valid")
			if code != int(contract.EXIT_CODES.EnvironmentMissing) || len(stdout) != 0 {
				t.Fatalf("exit %d stdout %q stderr %q; want 3 and nothing on stdout", code, stdout, stderr)
			}
			line := assertErrorLine(t, stderr, contract.ErrorCodeEnvironmentMissing)
			if !strings.HasPrefix(line.Message, "ltk check: ") || !strings.Contains(line.Message, c.inMessage) {
				t.Errorf("message %q, want %q", line.Message, c.inMessage)
			}
			if f != nil && !slices.Equal(f.subcommands(), c.calls) {
				t.Errorf("the fake was called with %v, want %v", f.subcommands(), c.calls)
			}
		})
	}
}

// TestProcessCheckUsageBeforeEnvironment: usage errors are found before the environment check. A missing or extra
// argument, an unknown flag, and a folder that does not exist (also with --also-go) exit 2 with nothing on stdout,
// without Docker, and with Docker the fake is never called.
func TestProcessCheckUsageBeforeEnvironment(t *testing.T) {
	cases := [][]string{
		{"check"},
		{"check", fixtureCorpus + "valid", fixtureCorpus + "hash"},
		{"check", "--also-rust", fixtureCorpus + "valid"},
		{"check", filepath.Join(t.TempDir(), "absent")},
		{"check", fixtureCorpus + "valid/catalogue.json"},
		{"check", filepath.Join(t.TempDir(), "absent"), "--also-go"},
		{"check", "--also-go"},
	}
	for _, args := range cases {
		for _, docker := range []bool{false, true} {
			var f *fakeEngine
			env := noDockerEnv(t)
			if docker {
				if fakeDockerDir == "" {
					continue // the rows without Docker run on every system; these need the fake, built for the host
				}
				f = newFakeEngine(t, "", fixtureCorpus+"valid")
				env = f.env
			}
			code, stdout, stderr := runLTK(t, env, args...)
			if code != int(contract.EXIT_CODES.UsageError) || len(stdout) != 0 {
				t.Errorf("ltk %q (docker %v): exit %d stdout %q stderr %q; want 2 and nothing", args, docker, code, stdout, stderr)
				continue
			}
			assertErrorLine(t, stderr, contract.ErrorCodeUsageError)
			if f == nil {
				continue
			}
			if calls := f.calls(); len(calls) != 0 {
				t.Errorf("ltk %q: the fake was called %v", args, calls)
			}
		}
	}
}

// treeOf maps every file under root to its bytes.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		out[p] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestProcessCheckValid: on a corpus whose checks
// pass, exit 0 with nothing on stderr and the full report on stdout: catalogue_schema, then every entry in catalogue
// order with file_integrity, round_trip and expected_values passing and goSdkRoundTrip null. The Rust XDR tool's one
// container is created labelled after the environment check and the snapshot, each entry is decoded then re-encoded in
// it, and it is removed with `rm -f -v` before exit. `ltk check` writes nothing: the folder is unchanged.
func TestProcessCheckValid(t *testing.T) {
	folder := fixtureCorpus + "valid"
	f := newFakeEngine(t, "", folder)
	before := treeOf(t, folder)
	code, stdout, stderr := runLTK(t, f.env, "check", folder)
	if code != 0 || len(stderr) != 0 {
		t.Fatalf("exit %d stderr %q; want 0 and nothing", code, stderr)
	}
	r := decodeReport(t, stdout)
	ids := []string{model.SeedSignedPayloadSigner, model.SeedPoolShareTrustline, model.SeedMuxedAccount}
	if !r.Passed || r.Folder != folder || !r.CatalogueSchema.Passed || len(r.CatalogueSchema.Failures) != 0 || len(r.Entries) != 3 {
		t.Fatalf("%+v", r)
	}
	for i, e := range r.Entries {
		if e.ID != ids[i] || !e.FileIntegrity.Passed || !e.RoundTrip.Passed || !e.ExpectedValues.Passed || e.GoSdkRoundTrip != nil ||
			e.RoundTrip.BytesCompared != e.FileIntegrity.Length || len(e.ExpectedValues.Mismatches) != 0 {
			t.Errorf("entry %d: %+v", i, e)
		}
	}
	got := f.subcommands()
	want := []string{"version", "image inspect", "ps", "network ls", "volume ls", "run",
		"exec", "exec", "exec", "exec", "exec", "exec",
		"ps", "rm", "network ls", "volume ls", "ps", "network ls", "volume ls"}
	if !slices.Equal(got, want) {
		t.Errorf("docker calls %v\nwant %v", got, want)
	}
	for _, c := range f.calls() {
		if c[0] == "run" && (!slices.Contains(c, orchestration.QuickstartCurrent) || !slices.Contains(c, "none") || !slices.Contains(c, "ltk.managed=true")) {
			t.Errorf("the tool's container %v: the current quickstart, no network, labelled", c)
		}
		if c[0] == "exec" && !(slices.Contains(c, "decode") || slices.Contains(c, "encode")) {
			t.Errorf("exec %v: a stellar-xdr decode or encode", c)
		}
	}
	if left := f.containers(); len(left) != 0 {
		t.Errorf("containers left: %v", left)
	}
	if after := treeOf(t, folder); len(after) != len(before) {
		t.Errorf("ltk check wrote into the folder: %d files before, %d after", len(before), len(after))
	} else {
		for p, b := range before {
			if after[p] != b {
				t.Errorf("ltk check changed %s", p)
			}
		}
	}
}

// TestProcessCheckFailures: one failing check
// exits 1 with the full report on stdout (passed false) and one failure ErrorLine naming the entry and the check;
// a catalogue that fails its schema starts no container and reports entries [].
func TestProcessCheckFailures(t *testing.T) {
	cases := []struct {
		kind, inMessage string
		entries         int
		container       bool
	}{
		{"hash", "ltk check: 1 of 3 entries failed; catalogue_schema passed; entry cap-0040-signed-payload-signer: file_integrity: the hash differs", 3, true},
		{"length", "entry cap-0038-pool-share-trustline: file_integrity: the length differs", 3, true},
		{"expected-value", "entry cap-0040-signed-payload-signer: expected_values: 1 of", 3, true},
		{"schema-copy", "ltk check: 0 of 3 entries failed; catalogue_schema failed; catalogue_schema: 1 failure(s)", 3, true},
		{"schema-pointer", "ltk check: catalogue_schema failed; no entry was checked; catalogue_schema: 2 failure(s), the first at \"/entries/0/length\"", 0, false},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			folder := fixtureCorpus + c.kind
			f := newFakeEngine(t, "", folder)
			code, stdout, stderr := runLTK(t, f.env, "check", folder)
			if code != int(contract.EXIT_CODES.Failure) {
				t.Fatalf("exit %d stderr %q; want 1", code, stderr)
			}
			r := decodeReport(t, stdout)
			if r.Passed || len(r.Entries) != c.entries || r.Entries == nil {
				t.Errorf("%+v", r)
			}
			line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
			if !strings.Contains(line.Message, c.inMessage) {
				t.Errorf("message %q\nwant it to hold %q", line.Message, c.inMessage)
			}
			if started := slices.Contains(f.subcommands(), "run"); started != c.container {
				t.Errorf("container started %v, want %v: %v", started, c.container, f.subcommands())
			}
			if left := f.containers(); len(left) != 0 {
				t.Errorf("containers left: %v", left)
			}
		})
	}
}

// TestProcessCheckTeardownLeftover: a run whose checks passed but whose teardown left the
// tool's container exits 1, the report on stdout and an ErrorLine naming the leftover.
func TestProcessCheckTeardownLeftover(t *testing.T) {
	folder := fixtureCorpus + "valid"
	f := newFakeEngine(t, "rm-fails", folder)
	code, stdout, stderr := runLTK(t, f.env, "check", folder)
	if code != int(contract.EXIT_CODES.Failure) {
		t.Fatalf("exit %d stderr %q; want 1", code, stderr)
	}
	if r := decodeReport(t, stdout); !r.Passed {
		t.Errorf("the checks passed: %+v", r)
	}
	left := f.containers()
	if len(left) != 1 {
		t.Fatalf("the fake engine holds %v", left)
	}
	line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
	if !strings.Contains(line.Message, "left container "+left[0].Name) {
		t.Errorf("message %q, want it to name container %s", line.Message, left[0].Name)
	}
}

// TestProcessCheckDecoderUnavailable: a tool container that does not start stops the command: exit 1, nothing on
// stdout (no report could be trusted), one ErrorLine saying why.
func TestProcessCheckDecoderUnavailable(t *testing.T) {
	folder := fixtureCorpus + "valid"
	f := newFakeEngine(t, "run-fails", folder)
	code, stdout, stderr := runLTK(t, f.env, "check", folder)
	if code != int(contract.EXIT_CODES.Failure) || len(stdout) != 0 {
		t.Fatalf("exit %d stdout %q; want 1 and nothing", code, stdout)
	}
	line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
	if !strings.Contains(line.Message, "Stellar's Rust XDR tool is not available") || !strings.Contains(line.Message, "fake create failure") {
		t.Errorf("message %q", line.Message)
	}
}

// waitForCall waits until the fake has been called with sub, or the program ended. The bound is an upper bound for a
// loaded shared runner, with a wide margin: the calls before it take milliseconds.
func waitForCall(t *testing.T, f *fakeEngine, sub string, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for !slices.Contains(f.subcommands(), sub) {
		select {
		case <-done:
			t.Fatalf("the program ended before calling docker %s: %v", sub, f.subcommands())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("docker %s was not called: %v", sub, f.subcommands())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestProcessCheckInterrupt: SIGINT while the tool decodes cancels the work; the teardown
// still removes the container, a second SIGINT during the teardown is consumed and does not abort it, and the program
// exits 1 saying it was interrupted, with nothing on stdout. The order is measured, not timed: the second signal
// is sent while the fake's `rm` waits for the test's release.
func TestProcessCheckInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipped on windows: os.Process.Signal cannot send an interrupt there")
	}
	folder := fixtureCorpus + "valid"
	f := newFakeEngine(t, "exec-hang,rm-wait", folder)
	cmd := ltkCommand(t, "", f.env, "check", folder)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	defer func() { _ = cmd.Process.Kill() }()

	waitForCall(t, f, "exec", done)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	waitForCall(t, f, "rm", done)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	// The second signal is delivered while rm waits; a program that did not consume it would die of it here. The pause
	// lets the kernel deliver it before the release (a late delivery would make the test fail, never pass wrongly).
	time.Sleep(500 * time.Millisecond)
	if err := os.WriteFile(f.state+".release", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	<-done
	code := exitCodeOfRun(t, waitErr)
	if code != int(contract.EXIT_CODES.Failure) || stdout.Len() != 0 {
		t.Fatalf("exit %d stdout %q stderr %q; want 1 and nothing on stdout", code, stdout.Bytes(), stderr.Bytes())
	}
	line := assertErrorLine(t, stderr.Bytes(), contract.ErrorCodeFailure)
	if !strings.HasPrefix(line.Message, "interrupted before the command finished: ltk check: ") {
		t.Errorf("message %q", line.Message)
	}
	if left := f.containers(); len(left) != 0 {
		t.Errorf("containers left after the interrupt: %v", left)
	}
}

// realXDRCorpus writes the real protocol-30 capture of testdata/real as entry 1 of a corpus in a new temporary folder,
// through the layout writer, its expected values read from its decoded JSON through the grouping table.
func realXDRCorpus(t *testing.T) string {
	t.Helper()
	folder := t.TempDir()
	raw, err := os.ReadFile(realCaptureXDR)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := os.ReadFile(realCaptureJSON)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := model.SeedByID(model.SeedSignedPayloadSigner)
	path := catalogue.EntryFilePath(seed.ID)
	if err := catalogue.WriteEntryFiles(folder, path, raw, decoded); err != nil {
		t.Fatal(err)
	}
	expected, err := catalogue.SplitExpectedValues(seed.ID, decoded)
	if err != nil {
		t.Fatal(err)
	}
	sha, n := catalogue.Measure(raw)
	cat := contract.Catalogue{Entries: []contract.CatalogueEntry{{ID: seed.ID, Cap: seed.Cap, ProtocolVersion: 30,
		Source: "the protocol-30 capture of testdata/real", ExpectedDecodedValues: expected, FilePath: path,
		XdrType: seed.XdrType, Sha256: sha, Length: n}}}
	if err := catalogue.WriteCatalogue(folder, cat); err != nil {
		t.Fatal(err)
	}
	return folder
}

// TestProcessCheckAlsoGo: `ltk check <folder> --also-go` adds each
// entry's Go SDK round trip, reported in goSdkRoundTrip beside the Rust tool's roundTrip.
//   - On the real capture both decoders pass: exit 0, nothing on stderr, goSdkRoundTrip passed with bytesCompared 1928.
//   - On the fixture corpus, whose bytes are not XDR, the (fake) Rust tool round-trips every entry and the Go SDK decodes
//     none: exit 1, the report on stdout with every roundTrip passed and every goSdkRoundTrip failed (bytesCompared
//     0), and one ErrorLine naming each entry with the check go_sdk_round_trip.
func TestProcessCheckAlsoGo(t *testing.T) {
	t.Run("real capture", func(t *testing.T) {
		folder := realXDRCorpus(t)
		f := newFakeEngine(t, "", folder)
		code, stdout, stderr := runLTK(t, f.env, "check", folder, "--also-go")
		if code != 0 || len(stderr) != 0 {
			t.Fatalf("exit %d stderr %s; want 0 and nothing\n%s", code, stderr, stdout)
		}
		r := decodeReport(t, stdout)
		e := r.Entries[0]
		if !r.Passed || len(r.Entries) != 1 || !e.RoundTrip.Passed || e.GoSdkRoundTrip == nil || !e.GoSdkRoundTrip.Passed ||
			e.GoSdkRoundTrip.Check != contract.CheckNameGoSdkRoundTrip || e.GoSdkRoundTrip.BytesCompared != 1928 {
			t.Errorf("%s", stdout)
		}
	})
	t.Run("fixture corpus", func(t *testing.T) {
		folder := fixtureCorpus + "valid"
		f := newFakeEngine(t, "", folder)
		code, stdout, stderr := runLTK(t, f.env, "--also-go" /* placed first: a flag before the command is not one */, "check", folder)
		if code != int(contract.EXIT_CODES.UsageError) {
			t.Fatalf("a flag before the command: exit %d, want 2", code)
		}
		code, stdout, stderr = runLTK(t, f.env, "check", "--also-go", folder)
		if code != int(contract.EXIT_CODES.Failure) {
			t.Fatalf("exit %d stderr %s; want 1\n%s", code, stderr, stdout)
		}
		r := decodeReport(t, stdout)
		if r.Passed || len(r.Entries) != 3 {
			t.Fatalf("%s", stdout)
		}
		for _, e := range r.Entries {
			if !e.RoundTrip.Passed || e.GoSdkRoundTrip == nil || e.GoSdkRoundTrip.Passed || e.GoSdkRoundTrip.BytesCompared != 0 {
				t.Errorf("%s: roundTrip %+v goSdkRoundTrip %+v", e.ID, e.RoundTrip, e.GoSdkRoundTrip)
			}
		}
		line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
		if !strings.HasPrefix(line.Message, "ltk check: 3 of 3 entries failed; catalogue_schema passed; entry cap-0040-signed-payload-signer: go_sdk_round_trip: ") {
			t.Errorf("message %q", line.Message)
		}
		for _, id := range []string{model.SeedPoolShareTrustline, model.SeedMuxedAccount} {
			if !strings.Contains(line.Message, "entry "+id+": go_sdk_round_trip: ") {
				t.Errorf("message %q does not name %s", line.Message, id)
			}
		}
		if strings.Contains(line.Message, ": round_trip: ") {
			t.Errorf("message %q: the Rust tool's round trip passed and is not named", line.Message)
		}
	})
}

// TestProcessCheckDecodedJSONHeld: `ltk check` holds each entry's decoded JSON
// file to the tool's fresh decoding of its XDR file, here the fake tool's decoding of the original valid corpus, on a
// copy of it: one leaf edited, the file missing or the file not JSON fails that entry's round trip (bytesCompared the
// file's length) with exit 1 and an ErrorLine naming the entry, the check and why; the file reformatted passes, exit 0.
func TestProcessCheckDecodedJSONHeld(t *testing.T) {
	const id = model.SeedSignedPayloadSigner
	cases := []struct {
		name      string
		alter     func(b []byte) []byte // nil removes the file
		passes    bool
		inMessage string
	}{
		{"one leaf edited", func(b []byte) []byte {
			return []byte(strings.Replace(string(b), `"set_options": "success"`, `"set_options": "malformed"`, 1))
		}, false, `entry ` + id + `: round_trip: the decoded JSON file ` + id + `/entry.json differs from the Rust XDR tool's decoding of ` + id + `/entry.xdr at "/v2/tx_processing/0/result/result/result/tx_success/0/op_inner/set_options"`},
		{"the file missing", nil, false, "entry " + id + ": round_trip: the decoded JSON file " + id + "/entry.json cannot be read"},
		{"the file not JSON", func(b []byte) []byte { return b[:len(b)/2] }, false, "entry " + id + ": round_trip: the decoded JSON file " + id + "/entry.json is not JSON"},
		{"the file reformatted", func(b []byte) []byte {
			var out bytes.Buffer
			if err := json.Compact(&out, b); err != nil {
				t.Fatal(err)
			}
			return out.Bytes()
		}, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			folder := copyTree(t, fixtureCorpus+"valid")
			file := filepath.Join(folder, id, "entry.json")
			b, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if c.alter == nil {
				err = os.Remove(file)
			} else {
				after := c.alter(b)
				if bytes.Equal(after, b) {
					t.Fatal("the alteration changed nothing")
				}
				err = os.WriteFile(file, after, 0o644)
			}
			if err != nil {
				t.Fatal(err)
			}
			f := newFakeEngine(t, "", fixtureCorpus+"valid")
			code, stdout, stderr := runLTK(t, f.env, "check", folder)
			r := decodeReport(t, stdout)
			e := r.Entries[0]
			if e.ID != id || !e.FileIntegrity.Passed || e.RoundTrip.BytesCompared != 1920 || !e.ExpectedValues.Passed || e.RoundTrip.Passed != c.passes {
				t.Errorf("entry 1: %+v", e)
			}
			for _, other := range r.Entries[1:] {
				if !other.RoundTrip.Passed {
					t.Errorf("%s: %+v", other.ID, other.RoundTrip)
				}
			}
			if c.passes {
				if code != 0 || len(stderr) != 0 || !r.Passed {
					t.Errorf("exit %d stderr %s; want 0 and nothing", code, stderr)
				}
				return
			}
			if code != int(contract.EXIT_CODES.Failure) || r.Passed {
				t.Fatalf("exit %d stderr %s; want 1", code, stderr)
			}
			line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
			if !strings.Contains(line.Message, "ltk check: 1 of 3 entries failed; catalogue_schema passed; "+c.inMessage) {
				t.Errorf("message %q\nwant it to hold %q", line.Message, c.inMessage)
			}
			if left := f.containers(); len(left) != 0 {
				t.Errorf("containers left: %v", left)
			}
		})
	}
}
