package main

// `ltk generate` at the process boundary in the unit tier: the built program with the fake docker of
// testdata/fakedocker on PATH and the fake quickstart of fakestellar_test.go, which applies entry 1's shape only. The
// real generation pipeline (internal/generate), the real layout writer and the real checks run; only Docker and the
// network are faked. The generation against real Docker is in generate_integration_test.go.

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
	"github.com/verticalhorizonai/ltk/internal/schema"
)

const entry1 = model.SeedSignedPayloadSigner

// decodeGenerateReport decodes stdout as one GenerateReport, validated against its schema.
func decodeGenerateReport(t *testing.T, stdout []byte) contract.GenerateReport {
	t.Helper()
	validateOutput(t, "generate-report.schema.json", stdout)
	var r contract.GenerateReport
	dec := json.NewDecoder(bytes.NewReader(stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("stdout is not a GenerateReport: %v\n%s", err, stdout)
	}
	if !bytes.HasSuffix(stdout, []byte("}\n")) || !bytes.HasPrefix(stdout, []byte("{\n  \"out\": ")) {
		t.Errorf("not one two-space indented document with one trailing newline: %q", stdout)
	}
	return r
}

// exists reports whether the path exists.
func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// coreRuns returns the images of the quickstart containers the fake ran, in order: the containers that publish a port.
func coreRuns(f *fakeEngine) []string {
	var out []string
	for _, c := range f.calls() {
		if len(c) == 0 || c[0] != "run" || !slices.Contains(c, "-p") {
			continue
		}
		if i := slices.IndexFunc(c, func(s string) bool { return strings.HasPrefix(s, "stellar/quickstart@") }); i >= 0 {
			out = append(out, c[i])
		}
	}
	return out
}

// TestProcessGenerateUsage: neither or both of --all and --shape, no --out (or an empty one), an id that names no shape
// recipe, an --out that is a file, and an extra argument exit 2 with nothing on stdout, before the environment check:
// the fake is never called, nothing is created or written.
func TestProcessGenerateUsage(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args      []string
		inMessage string
	}{
		{[]string{"generate", "--out", out}, "give --all or --shape <entry id>"},
		{[]string{"generate", "--all", "--shape", entry1, "--out", out}, "give either --all or --shape <entry id>, not both"},
		{[]string{"generate", "--all"}, "--out <folder> is required"},
		{[]string{"generate", "--all", "--out", ""}, "--out <folder> is required"},
		{[]string{"generate", "--shape", "not-a-real-entry-id", "--out", out}, "unknown --shape entry id: not-a-real-entry-id (the shapes are cap-0040-signed-payload-signer, cap-0038-pool-share-trustline, cap-0027-muxed-account)"},
		{[]string{"generate", "--shape", "cap-0040-signed-payload", "--out", out}, "unknown --shape entry id: cap-0040-signed-payload "},
		{[]string{"generate", "--shape", "", "--out", out}, "unknown --shape entry id: "},
		{[]string{"generate", "--all", "--out", file}, "the --out folder " + file + " is a file"},
		{[]string{"generate", "--all", "--out", out, "extra"}, `unexpected argument "extra"`},
		{[]string{"generate", "--all", "--out", out, "--protocol", "28"}, "flag provided but not defined: -protocol"},
	}
	for _, c := range cases {
		for _, docker := range []bool{false, true} {
			var f *fakeEngine
			env := noDockerEnv(t)
			if docker {
				if fakeDockerDir == "" {
					continue // the rows without Docker run on every system; these need the fake, built with a go tool
				}
				f = newFakeEngine(t, "", "")
				env = f.env
			}
			code, stdout, stderr := runLTK(t, env, c.args...)
			if code != int(contract.EXIT_CODES.UsageError) || len(stdout) != 0 {
				t.Errorf("ltk %q (docker %v): exit %d stdout %q stderr %q; want 2 and nothing", c.args, docker, code, stdout, stderr)
				continue
			}
			line := assertErrorLine(t, stderr, contract.ErrorCodeUsageError)
			if !strings.Contains(line.Message, c.inMessage) {
				t.Errorf("ltk %q: message %q, want %q", c.args, line.Message, c.inMessage)
			}
			if f != nil && len(f.calls()) != 0 {
				t.Errorf("ltk %q: the fake was called %v", c.args, f.calls())
			}
			if exists(out) {
				t.Fatalf("ltk %q created %s", c.args, out)
			}
		}
	}
}

// TestProcessGenerateEnvironmentMissing (the fail-fast row for ltk generate): without Docker with Linux containers, or
// with a quickstart image absent and unpullable, `ltk generate` exits 3 before any work: nothing on stdout, one
// environment_missing ErrorLine naming what is missing, only `version` (and the image's inspection and pull) called,
// and the --out folder neither created nor written.
func TestProcessGenerateEnvironmentMissing(t *testing.T) {
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
		for _, args := range [][]string{{"--all"}, {"--shape", entry1}} {
			t.Run(c.name+" "+args[0], func(t *testing.T) {
				var f *fakeEngine
				env := noDockerEnv(t)
				if !c.noDocker {
					f = newFakeEngine(t, c.modes, "")
					env = f.env
				}
				out := filepath.Join(t.TempDir(), "out")
				code, stdout, stderr := runLTK(t, env, append(append([]string{"generate"}, args...), "--out", out)...)
				if code != int(contract.EXIT_CODES.EnvironmentMissing) || len(stdout) != 0 {
					t.Fatalf("exit %d stdout %q stderr %q; want 3 and nothing on stdout", code, stdout, stderr)
				}
				line := assertErrorLine(t, stderr, contract.ErrorCodeEnvironmentMissing)
				if !strings.HasPrefix(line.Message, "ltk generate: ") || !strings.Contains(line.Message, c.inMessage) {
					t.Errorf("message %q, want %q", line.Message, c.inMessage)
				}
				if f != nil && !slices.Equal(f.subcommands(), c.calls) {
					t.Errorf("the fake was called with %v, want %v", f.subcommands(), c.calls)
				}
				if exists(out) {
					t.Errorf("the --out folder was created")
				}
			})
		}
	}
}

// assertTornDown: no container is left in the fake engine; the quickstart was removed by name before the checks encoded
// anything; the reaper ran after the work.
func assertTornDown(t *testing.T, f *fakeEngine, checked bool) {
	t.Helper()
	if left := f.containers(); len(left) != 0 {
		t.Errorf("containers left: %v", left)
	}
	calls := f.calls()
	stopCore, firstEncode := -1, -1
	for i, c := range calls {
		if len(c) > 3 && c[0] == "rm" && strings.HasSuffix(c[len(c)-1], "-core") && stopCore < 0 {
			stopCore = i
		}
		if c[0] == "exec" && slices.Contains(c, "encode") && firstEncode < 0 {
			firstEncode = i
		}
	}
	if stopCore < 0 {
		t.Errorf("the quickstart was never removed by name: %v", f.subcommands())
	}
	if checked && (firstEncode < 0 || firstEncode < stopCore) {
		t.Errorf("the checks ran before the generation network was removed: %v", f.subcommands())
	}
	reapedAfterWork(t, f)
}

// TestProcessGenerateShape: `ltk generate --shape <entry 1> --out <new folder>` starts the current quickstart, runs
// entry 1's recipe on it, decodes its capture with the run's one Rust XDR tool container, writes the entry's folder
// (its XDR file and decoded JSON), the catalogue holding that one record and the schema copy, removes the network, runs
// the checks on the folder and prints the report: exit 0, nothing on stderr, the shape generated at the current
// protocol without fallback, check passed with goSdkRoundTrip null. Nothing is left in the engine.
func TestProcessGenerateShape(t *testing.T) {
	f, _ := generateEngine(t, "")
	out := filepath.Join(t.TempDir(), "new", "out")
	code, stdout, stderr := runLTK(t, f.env, "generate", "--shape", entry1, "--out", out)
	if code != 0 || len(stderr) != 0 {
		t.Fatalf("exit %d stderr %s; want 0 and nothing\n%s", code, stderr, stdout)
	}
	r := decodeGenerateReport(t, stdout)
	want := contract.ShapeGenerationResult{ID: entry1, ProtocolVersion: orchestration.CurrentProtocol, Generated: true}
	if r.Out != out || len(r.Shapes) != 1 || r.Shapes[0] != want || r.Check == nil || !r.Check.Passed || r.Check.Folder != out {
		t.Fatalf("%s", stdout)
	}
	if len(r.Check.Entries) != 1 || r.Check.Entries[0].ID != entry1 || r.Check.Entries[0].GoSdkRoundTrip != nil {
		t.Errorf("check entries %+v", r.Check.Entries)
	}
	for _, p := range []string{catalogue.CatalogueFileName, catalogue.SchemaCopyFileName, entry1 + "/entry.xdr", entry1 + "/entry.json"} {
		if !exists(filepath.Join(out, filepath.FromSlash(p))) {
			t.Errorf("%s was not written", p)
		}
	}
	cat, err := catalogue.ReadCatalogue(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Entries) != 1 || cat.Entries[0].ID != entry1 || cat.Entries[0].ProtocolVersion != orchestration.CurrentProtocol ||
		cat.Entries[0].ProtocolFallback || cat.Entries[0].FilePath != entry1+"/entry.xdr" {
		t.Errorf("catalogue %+v", cat)
	}
	if res, _ := schema.CheckCatalogue(out); !res.Passed {
		t.Errorf("the written catalogue fails its check: %+v", res)
	}
	if got := coreRuns(f); !slices.Equal(got, []string{orchestration.QuickstartCurrent}) {
		t.Errorf("quickstarts started %v: one, the current", got)
	}
	runs := 0
	for _, c := range f.calls() {
		if c[0] == "run" {
			runs++
			if !slices.Contains(c, "ltk.managed=true") {
				t.Errorf("an unlabelled container %v", c)
			}
		}
	}
	if runs != 2 {
		t.Errorf("%d containers started; want the quickstart and the one Rust XDR tool: %v", runs, f.subcommands())
	}
	assertTornDown(t, f, true)
}

// TestProcessGenerateShapeMerges: into a folder that already holds a catalogue of three entries, --shape replaces only
// that entry's folder and record; every other record and every other entry folder is left byte for byte as it was, and
// the checks run on the whole folder.
func TestProcessGenerateShapeMerges(t *testing.T) {
	out := copyTree(t, fixtureCorpus+"valid")
	before := treeOf(t, out)
	f, _ := generateEngine(t, "", out)
	code, stdout, stderr := runLTK(t, f.env, "generate", "--shape", entry1, "--out", out)
	if code != 0 || len(stderr) != 0 {
		t.Fatalf("exit %d stderr %s; want 0\n%s", code, stderr, stdout)
	}
	r := decodeGenerateReport(t, stdout)
	if len(r.Shapes) != 1 || r.Check == nil || len(r.Check.Entries) != 3 || !r.Check.Passed {
		t.Fatalf("%s", stdout)
	}
	after := treeOf(t, out)
	for p, b := range before {
		rel, _ := filepath.Rel(out, p)
		rel = filepath.ToSlash(rel)
		changed := after[p] != b
		mine := strings.HasPrefix(rel, entry1+"/") || rel == catalogue.CatalogueFileName
		if changed != mine {
			t.Errorf("%s changed %v; only entry 1's files and the catalogue change", rel, changed)
		}
	}
	if len(after) != len(before) {
		t.Errorf("%d files before, %d after", len(before), len(after))
	}
	oldCat, newCat := catalogueOf(t, before, out), catalogueOf(t, after, out)
	for i := range oldCat.Entries {
		o, _ := json.Marshal(oldCat.Entries[i])
		n, _ := json.Marshal(newCat.Entries[i])
		if (oldCat.Entries[i].ID == entry1) == bytes.Equal(o, n) {
			t.Errorf("record %s: changed %v; only entry 1's record changes", oldCat.Entries[i].ID, !bytes.Equal(o, n))
		}
	}
	assertTornDown(t, f, true)
}

// catalogueOf decodes the catalogue of a tree from treeOf.
func catalogueOf(t *testing.T, tree map[string]string, folder string) contract.Catalogue {
	t.Helper()
	var c contract.Catalogue
	if err := json.Unmarshal([]byte(tree[filepath.Join(folder, catalogue.CatalogueFileName)]), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// copyTree copies a folder into a new temporary folder and returns the copy.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "corpus")
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// TestProcessGenerateAllShapeFails, the protocol selector's path: `ltk generate --all` on a network that applies entry
// 1 only: every shape is attempted in seed order; entry 1 is generated at the current protocol; entries 2 and 3,
// refused there, are retried on the protocol-28 network, which is started after the first is removed, and refused
// again: generated false, each with its error. Their records and folders are not written, entry 1's are; check is null;
// the report is on stdout and the exit is 1 with one ErrorLine naming each failed shape. Nothing is left in the engine.
func TestProcessGenerateAllShapeFails(t *testing.T) {
	f, _ := generateEngine(t, "")
	out := filepath.Join(t.TempDir(), "out")
	code, stdout, stderr := runLTK(t, f.env, "generate", "--all", "--out", out)
	if code != int(contract.EXIT_CODES.Failure) {
		t.Fatalf("exit %d stderr %s; want 1\n%s", code, stderr, stdout)
	}
	r := decodeGenerateReport(t, stdout)
	if r.Check != nil || len(r.Shapes) != 3 {
		t.Fatalf("%s", stdout)
	}
	ids := []string{entry1, model.SeedPoolShareTrustline, model.SeedMuxedAccount}
	for i, s := range r.Shapes {
		if s.ID != ids[i] {
			t.Errorf("shape %d is %s, want %s (seed order)", i, s.ID, ids[i])
		}
	}
	if s := r.Shapes[0]; !s.Generated || s.Error != nil || s.ProtocolVersion != orchestration.CurrentProtocol || s.ProtocolFallback {
		t.Errorf("entry 1: %+v", s)
	}
	for _, s := range r.Shapes[1:] {
		if s.Generated || s.Error == nil || !strings.Contains(*s.Error, "at protocol 30: ") || !strings.Contains(*s.Error, "at protocol 28: ") ||
			!strings.Contains(*s.Error, "sendTransaction answered ERROR") {
			t.Errorf("%s: %+v; want not generated, refused at 30 and at 28", s.ID, s)
		}
	}
	line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
	if !strings.HasPrefix(line.Message, "ltk generate: generation failed: 2 of 3 shapes not generated; "+model.SeedPoolShareTrustline+": at protocol 30: ") ||
		!strings.Contains(line.Message, "; "+model.SeedMuxedAccount+": at protocol 30: ") {
		t.Errorf("message %q", line.Message)
	}
	cat, err := catalogue.ReadCatalogue(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Entries) != 1 || cat.Entries[0].ID != entry1 {
		t.Errorf("catalogue %+v: entry 1's record only", cat)
	}
	for _, id := range ids[1:] {
		if exists(filepath.Join(out, id)) {
			t.Errorf("the folder of %s, which was not generated, was written", id)
		}
	}
	if got := coreRuns(f); !slices.Equal(got, []string{orchestration.QuickstartCurrent, orchestration.QuickstartProtocol28}) {
		t.Errorf("quickstarts started %v: the current, then the protocol-28 one", got)
	}
	assertTornDown(t, f, false)
}

// TestProcessGenerateWriteFails (no ambiguous partial state): a write of the folder that fails in a way root cannot
// override, a directory in a file's place, runs as root in the build's container. The command exits 1 with one
// ErrorLine naming the path and no report; what was written before the failure stays, nothing after it is written, and
// every container is removed. A second run with the directory still there fails the same way and rewrites the same
// bytes; a run after it is removed writes the entry and its record.
//   - The catalogue, the last file a generation writes, with --all (with --shape the catalogue is read first, to merge
//     into it, and a directory there stops the run before anything starts): the entry's two files and the schema copy
//     are written. The fake network applies entry 1 only, so the last run exits 1 with entry 1's record written.
//   - The entry's decoded JSON, the second of its two files, with --shape: its XDR file is written, no catalogue and no
//     schema copy. The last run exits 0.
func TestProcessGenerateWriteFails(t *testing.T) {
	cases := []struct {
		name string
		args []string
		// blocked is the file a directory is put in the place of, relative to the folder.
		blocked string
		// written are what the failed run wrote; absent what it did not.
		written, absent []string
		// lastExit is the exit of the run after the directory is removed.
		lastExit int
	}{
		{"the catalogue (the last file)", []string{"--all"}, catalogue.CatalogueFileName,
			[]string{entry1 + "/entry.xdr", entry1 + "/entry.json", catalogue.SchemaCopyFileName}, nil, 1},
		{"the entry's decoded JSON (the second of its two files)", []string{"--shape", entry1}, entry1 + "/entry.json",
			[]string{entry1 + "/entry.xdr"}, []string{catalogue.CatalogueFileName, catalogue.SchemaCopyFileName}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			args := append(append([]string{"generate"}, c.args...), "--out", out)
			blocked := filepath.Join(out, filepath.FromSlash(c.blocked))
			if err := os.MkdirAll(filepath.Join(blocked, "inside"), 0o755); err != nil {
				t.Fatal(err)
			}
			var firstXDR []byte
			for run := 1; run <= 2; run++ {
				f, _ := generateEngine(t, "")
				code, stdout, stderr := runLTK(t, f.env, args...)
				if code != int(contract.EXIT_CODES.Failure) || len(stdout) != 0 {
					t.Fatalf("run %d: exit %d stdout %q stderr %s; want 1 and no report", run, code, stdout, stderr)
				}
				line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
				if !strings.HasPrefix(line.Message, "ltk generate: ") ||
					!strings.Contains(line.Message, "write "+filepath.ToSlash(blocked)+": something other than a file is in its place") {
					t.Errorf("run %d: message %q, want it to name %s", run, line.Message, blocked)
				}
				for _, p := range c.written {
					if st, err := os.Stat(filepath.Join(out, filepath.FromSlash(p))); err != nil || !st.Mode().IsRegular() {
						t.Errorf("run %d: %s was not written before the failure", run, p)
					}
				}
				for _, p := range c.absent {
					if exists(filepath.Join(out, filepath.FromSlash(p))) {
						t.Errorf("run %d: %s was written after the failure", run, p)
					}
				}
				if !exists(filepath.Join(blocked, "inside")) {
					t.Errorf("run %d: the directory in the way was changed", run)
				}
				if left := f.containers(); len(left) != 0 {
					t.Errorf("run %d: containers left %v", run, left)
				}
				b, err := os.ReadFile(filepath.Join(out, entry1, "entry.xdr"))
				if err != nil {
					t.Fatal(err)
				}
				if run == 1 {
					firstXDR = b
				} else if !bytes.Equal(b, firstXDR) {
					t.Errorf("the second run's XDR file differs from the first's")
				}
			}
			if err := os.RemoveAll(blocked); err != nil {
				t.Fatal(err)
			}
			f, _ := generateEngine(t, "")
			code, stdout, stderr := runLTK(t, f.env, args...)
			if code != c.lastExit {
				t.Fatalf("the run after the directory was removed: exit %d stderr %s, want %d\n%s", code, stderr, c.lastExit, stdout)
			}
			r := decodeGenerateReport(t, stdout)
			if !r.Shapes[0].Generated || (c.lastExit == 0) != (r.Check != nil && r.Check.Passed) {
				t.Errorf("%s", stdout)
			}
			if res, cat := schema.CheckCatalogue(out); !res.Passed || cat == nil || len(cat.Entries) != 1 || cat.Entries[0].ID != entry1 {
				t.Errorf("the catalogue after the last run: %+v %+v", res, cat)
			}
		})
	}
}

// TestProcessGenerateTeardownLeftover: when the containers cannot be removed, the run fails (exit 1) with an ErrorLine
// naming the quickstart container the teardown left, whatever the generation did.
func TestProcessGenerateTeardownLeftover(t *testing.T) {
	f, _ := generateEngine(t, "rm-fails")
	out := filepath.Join(t.TempDir(), "out")
	code, _, stderr := runLTK(t, f.env, "generate", "--shape", entry1, "--out", out)
	if code != int(contract.EXIT_CODES.Failure) {
		t.Fatalf("exit %d stderr %s; want 1", code, stderr)
	}
	line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
	var core string
	for _, c := range f.containers() {
		if strings.HasSuffix(c.Name, "-core") {
			core = c.Name
		}
	}
	if core == "" || !strings.Contains(line.Message, "left ") || !strings.Contains(line.Message, "container "+core) {
		t.Errorf("message %q; want it to name the container left, %q", line.Message, core)
	}
}

// startGenerate starts the program as `ltk generate args...` and returns its command, its outputs and a channel closed
// when it ends.
func startGenerate(t *testing.T, f *fakeEngine, args ...string) (wait func() (int, []byte, []byte), done <-chan struct{}, signal func()) {
	t.Helper()
	cmd := ltkCommand(t, "", f.env, append([]string{"generate"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ch := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(ch) }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return func() (int, []byte, []byte) {
			<-ch
			return exitCodeOfRun(t, waitErr), stdout.Bytes(), stderr.Bytes()
		}, ch, func() {
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
		}
}

// TestProcessGenerateInterrupt: SIGINT to a running `ltk generate` stops it: every container is removed, the exit is 1
// with one ErrorLine saying the command was interrupted, and no report is written. The files already written under the
// folder are left as they are.
//   - During the submission (the fake RPC holds sendTransaction until the request is cancelled): nothing was written.
//   - During the checks (the fake tool's encode never answers), after the folder was written: the folder is left as the
//     generation wrote it.
//
// The signal is sent once the program is seen at that step; nothing is timed.
func TestProcessGenerateInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipped on windows: os.Process.Signal cannot send an interrupt there")
	}
	t.Run("during the submission", func(t *testing.T) {
		f, current := generateEngine(t, "")
		current.mu.Lock()
		current.hangSend = true
		current.mu.Unlock()
		out := filepath.Join(t.TempDir(), "out")
		wait, done, interrupt := startGenerate(t, f, "--shape", entry1, "--out", out)
		select {
		case <-current.sends:
		case <-done:
			t.Fatal("the program ended before submitting")
		case <-time.After(2 * time.Minute): // an upper bound for a loaded shared runner; the calls before it take milliseconds
			t.Fatal("nothing was submitted")
		}
		if !slices.Contains(f.subcommands(), "run") {
			t.Fatalf("the quickstart container was not started: %v", f.subcommands())
		}
		interrupt()
		code, stdout, stderr := wait()
		assertInterrupted(t, f, code, stdout, stderr)
		if exists(filepath.Join(out, catalogue.CatalogueFileName)) || exists(filepath.Join(out, entry1)) {
			t.Errorf("an interrupted submission wrote into the folder")
		}
	})
	t.Run("during the checks", func(t *testing.T) {
		f, _ := generateEngine(t, "encode-hang")
		out := filepath.Join(t.TempDir(), "out")
		wait, done, interrupt := startGenerate(t, f, "--shape", entry1, "--out", out)
		deadline := time.Now().Add(2 * time.Minute) // as above
		for !slices.ContainsFunc(f.calls(), func(c []string) bool { return c[0] == "exec" && slices.Contains(c, "encode") }) {
			select {
			case <-done:
				t.Fatalf("the program ended before the checks: %v", f.subcommands())
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("the checks did not start: %v", f.subcommands())
			}
			time.Sleep(10 * time.Millisecond)
		}
		written := treeOf(t, out)
		interrupt()
		code, stdout, stderr := wait()
		assertInterrupted(t, f, code, stdout, stderr)
		after := treeOf(t, out)
		if len(written) != 4 || len(after) != len(written) {
			t.Errorf("files before the interrupt %d, after %d; want the 4 the generation wrote", len(written), len(after))
		}
		for p, b := range written {
			if after[p] != b {
				t.Errorf("%s changed after the interrupt", p)
			}
		}
	})
}

// assertInterrupted asserts an interrupted run's outcome: exit 1, nothing on stdout, one ErrorLine saying so, nothing
// left in the engine, the reaper run after the work.
func assertInterrupted(t *testing.T, f *fakeEngine, code int, stdout, stderr []byte) {
	t.Helper()
	if code != int(contract.EXIT_CODES.Failure) || len(stdout) != 0 {
		t.Fatalf("exit %d stdout %q stderr %s; want 1 and no report", code, stdout, stderr)
	}
	line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
	if !strings.HasPrefix(line.Message, "interrupted before the command finished: ltk generate: ") {
		t.Errorf("message %q", line.Message)
	}
	if left := f.containers(); len(left) != 0 {
		t.Errorf("containers left after the interrupt: %v", left)
	}
	reapedAfterWork(t, f)
}
