package jssdk

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// fakeEngine is the Docker CLI runner of the unit tier: each release's container answers npm ci and the script with
// what the test set for its version.
type fakeEngine struct {
	mu sync.Mutex
	// out is the script's standard output per version; npmErr fails npm ci per version; nodeErr fails the script.
	out     map[contract.SdkVersion]string
	npmErr  map[contract.SdkVersion]error
	nodeErr map[contract.SdkVersion]error
	runErr  error
	// what happened
	specs   []orchestration.ContainerSpec
	copies  map[string][]byte
	execs   map[string][][]string
	removed []string
}

func newFakeEngine(before, after string) *fakeEngine {
	return &fakeEngine{
		out:     map[contract.SdkVersion]string{contract.SdkVersion17_2_0: before, contract.SdkVersion17_2_1: after},
		npmErr:  map[contract.SdkVersion]error{},
		nodeErr: map[contract.SdkVersion]error{},
		copies:  map[string][]byte{},
		execs:   map[string][][]string{},
	}
}

// versionOf maps a container name back to its release.
func versionOf(name string) contract.SdkVersion {
	if strings.HasSuffix(name, "sdk-17-2-0") {
		return contract.SdkVersion17_2_0
	}
	return contract.SdkVersion17_2_1
}

func (f *fakeEngine) Run(ctx context.Context, spec orchestration.ContainerSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.runErr != nil {
		return "", f.runErr
	}
	f.specs = append(f.specs, spec)
	return "ltk-0123abcd-" + spec.Role, nil
}

func (f *fakeEngine) CopyInto(ctx context.Context, container, dir string, tarball []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if dir != "/tmp" {
		return errors.New("copy into " + dir)
	}
	f.copies[container] = tarball
	return nil
}

func (f *fakeEngine) Exec(ctx context.Context, container string, stdin []byte, args ...string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	f.execs[container] = append(f.execs[container], args)
	v := versionOf(container)
	if args[0] == "sh" {
		if err := f.npmErr[v]; err != nil {
			return nil, []byte("npm error code E404\nnpm error 404 Not Found"), err
		}
		return []byte("added 42 packages"), nil, nil
	}
	if err := f.nodeErr[v]; err != nil {
		return nil, []byte("SyntaxError: The requested module does not provide an export named 'default'"), err
	}
	return []byte(f.out[v]), nil, nil
}

func (f *fakeEngine) Remove(ctx context.Context, container string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, container)
	return nil
}

// fixture reads a recorded line of testdata/.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// untar returns the files of a tar stream.
func untar(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = body
	}
}

// The unit tier, on the recorded lines of the native probe: one script runs against 17.2.0 then 17.2.1, each in its
// own node:22-slim container that takes the script and that release's package files by `docker cp`, runs
// `npm ci --ignore-scripts`, then `node repro.mjs` with entry 2's asset code and issuers in key-byte order from
// internal/model; 17.2.0 rejects at build with the expected text and 17.2.1 round-trips, so the report passes, and
// each container is removed after its release.
func TestRunReproducesOnTheRecordedLines(t *testing.T) {
	f := newFakeEngine(fixture(t, "17.2.0-rejects"), fixture(t, "17.2.1-round-trips"))
	rep, err := run(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Passed || Reasons(rep) != nil {
		t.Fatalf("passed %v, reasons %q", rep.Passed, Reasons(rep))
	}
	if rep.Target != contract.ReproTargetJsSdk1766 || len(rep.Versions) != 2 {
		t.Fatalf("report %+v", rep)
	}
	v0, v1 := rep.Versions[0], rep.Versions[1]
	if v0.Version != "17.2.0" || v0.Role != "before_fix" || v1.Version != "17.2.1" || v1.Role != "fix" {
		t.Fatalf("versions %+v", rep.Versions)
	}
	if len(v0.Steps) != 1 || v0.Steps[0].Step != "build" || v0.Steps[0].Passed || *v0.Steps[0].Error != "Assets are not in lexicographic order" || v0.RoundTripIdentical {
		t.Fatalf("17.2.0 %+v", v0)
	}
	if len(v1.Steps) != 3 || !v1.RoundTripIdentical {
		t.Fatalf("17.2.1 %+v", v1)
	}
	for i, s := range v1.Steps {
		if s.Step != stepOrder[i] || !s.Passed || s.Error != nil {
			t.Fatalf("17.2.1 step %d %+v", i, s)
		}
	}
	// The report document is the contract's: the success example's shape, error null on a passed step.
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"target":"js-sdk-1766","versions":[{"version":"17.2.0","role":"before_fix","steps":[{"step":"build","passed":false,"error":"Assets are not in lexicographic order"}],"roundTripIdentical":false},{"version":"17.2.1","role":"fix","steps":[{"step":"build","passed":true,"error":null},{"step":"decode","passed":true,"error":null},{"step":"re_encode","passed":true,"error":null}],"roundTripIdentical":true}],"passed":true}`
	if string(b) != want {
		t.Fatalf("report\n%s\nwant\n%s", b, want)
	}

	if len(f.specs) != 2 {
		t.Fatalf("containers %+v", f.specs)
	}
	for i, rel := range releases {
		spec := f.specs[i]
		if spec.Image != orchestration.NodeImage || spec.Role != rel.containerRole || spec.Entrypoint != "sleep" || !slices.Equal(spec.Args, []string{"infinity"}) || len(spec.Mounts) != 0 || len(spec.Publish) != 0 {
			t.Fatalf("container %d spec %+v", i, spec)
		}
		name := "ltk-0123abcd-" + rel.containerRole
		got := untar(t, f.copies[name])
		script, _ := files.ReadFile("repro.mjs")
		pkg, _ := files.ReadFile(rel.dir + "/package.json")
		lock, _ := files.ReadFile(rel.dir + "/package-lock.json")
		if !bytes.Equal(got["ltk-repro/repro.mjs"], script) || !bytes.Equal(got["ltk-repro/package.json"], pkg) || !bytes.Equal(got["ltk-repro/package-lock.json"], lock) {
			t.Fatalf("%s: the copied files are not the embedded ones: %v", name, keys(got))
		}
		execs := f.execs[name]
		if len(execs) != 2 {
			t.Fatalf("%s execs %q", name, execs)
		}
		if !slices.Equal(execs[0], []string{"sh", "-c", "cd /tmp/ltk-repro && npm ci --ignore-scripts --no-audit --no-fund"}) {
			t.Fatalf("%s npm call %q", name, execs[0])
		}
		wantNode := []string{"node", "/tmp/ltk-repro/repro.mjs", model.PoolAssetCode, model.PoolIssuerLowerBytes, model.PoolIssuerHigherBytes}
		if !slices.Equal(execs[1], wantNode) {
			t.Fatalf("%s node call %q; want %q", name, execs[1], wantNode)
		}
	}
	if !slices.Equal(f.removed, []string{"ltk-0123abcd-sdk-17-2-0", "ltk-0123abcd-sdk-17-2-1"}) {
		t.Fatalf("removed %q", f.removed)
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// One fixture per failing condition of the verdict; each gives passed false and names its reason, the reasons going
// to the error line since the report's schema has no field for them.
func TestRunNotReproducedNamesTheReason(t *testing.T) {
	for _, tc := range []struct {
		before, after string
		reason        string
	}{
		{"17.2.0-accepts", "17.2.1-round-trips", "17.2.0 did not reject entry 2's pool-share trustline: every step it ran passed"},
		{"17.2.0-other-message", "17.2.1-round-trips", `17.2.0 failed at build with "Cannot read properties of undefined (reading 'toXDRObject')", not "Assets are not in lexicographic order"`},
		{"17.2.0-fails-at-decode", "17.2.1-round-trips", `17.2.0 built entry 2's pool-share trustline and failed at decode with "Assets are not in lexicographic order"; the rejection is expected at build with "Assets are not in lexicographic order"`},
		{"17.2.0-rejects", "17.2.1-rejects", "17.2.1 failed at build: Assets are not in lexicographic order"},
		{"17.2.0-rejects", "17.2.1-bytes-differ", "17.2.1 re-encoded bytes differ from the built bytes"},
	} {
		t.Run(tc.before+"+"+tc.after, func(t *testing.T) {
			rep, err := run(context.Background(), newFakeEngine(fixture(t, tc.before), fixture(t, tc.after)))
			if err != nil {
				t.Fatal(err)
			}
			if rep.Passed {
				t.Fatal("passed")
			}
			if got := Reasons(rep); !slices.Equal(got, []string{tc.reason}) {
				t.Fatalf("reasons %q; want %q", got, tc.reason)
			}
		})
	}
}

// Reasons of a passed report is nil; an inconsistent report (passed false, every condition met) still
// says something; a report of the wrong shape names it.
func TestReasonsEdges(t *testing.T) {
	rep, err := run(context.Background(), newFakeEngine(fixture(t, "17.2.0-rejects"), fixture(t, "17.2.1-round-trips")))
	if err != nil {
		t.Fatal(err)
	}
	if Reasons(rep) != nil {
		t.Fatal("reasons for a passed report")
	}
	rep.Passed = false
	if got := Reasons(rep); len(got) != 1 || !strings.Contains(got[0], "says not reproduced") {
		t.Fatalf("inconsistent report reasons %q", got)
	}
	swapped := rep
	swapped.Versions = []contract.SdkVersionResult{rep.Versions[1], rep.Versions[0]}
	if got := Reasons(swapped); len(got) != 2 || !strings.Contains(got[0], "release 0 is 17.2.1 (fix), not 17.2.0 (before_fix)") {
		t.Fatalf("swapped report reasons %q", got)
	}
	if got := Reasons(contract.JsSdk1766Report{Target: contract.ReproTargetWalletBackend729}); len(got) != 2 {
		t.Fatalf("empty report reasons %q", got)
	}
}

// A run that could not produce a report is an error naming what failed, never a rejection.
func TestRunInfrastructureFailuresAreErrors(t *testing.T) {
	good0, good1 := fixture(t, "17.2.0-rejects"), fixture(t, "17.2.1-round-trips")
	for _, tc := range []struct {
		name  string
		setup func(f *fakeEngine)
		want  string
	}{
		{"container does not start", func(f *fakeEngine) { f.runErr = errors.New("docker run: no space left") }, "js-sdk-1766: @stellar/stellar-sdk 17.2.0: docker run: no space left"},
		{"npm ci fails", func(f *fakeEngine) { f.npmErr[contract.SdkVersion17_2_1] = errors.New("exit status 1") }, "17.2.1: npm ci --ignore-scripts failed: exit status 1; standard error: npm error code E404 | npm error 404 Not Found"},
		{"script crashes", func(f *fakeEngine) { f.nodeErr[contract.SdkVersion17_2_0] = errors.New("exit status 1") }, "the reproduction script printed no result line: exit status 1"},
		{"no line", func(f *fakeEngine) { f.out[contract.SdkVersion17_2_0] = "" }, "want exactly one JSON line"},
		{"two lines", func(f *fakeEngine) { f.out[contract.SdkVersion17_2_0] = good0 + good0 }, "want exactly one JSON line"},
		{"not JSON", func(f *fakeEngine) { f.out[contract.SdkVersion17_2_0] = "[THROW] build\n" }, "is not its JSON result"},
		{"unknown field", func(f *fakeEngine) {
			f.out[contract.SdkVersion17_2_0] = strings.Replace(good0, `"steps"`, `"accepted":false,"steps"`, 1)
		}, "is not its JSON result"},
		{"other release", func(f *fakeEngine) { f.out[contract.SdkVersion17_2_0] = good1 }, `ran @stellar/stellar-sdk "17.2.1", not 17.2.0`},
		{"no steps", func(f *fakeEngine) {
			f.out[contract.SdkVersion17_2_0] = `{"sdkVersion":"17.2.0","steps":[],"roundTripIdentical":false,"builtXdr":null,"reEncodedXdr":null}`
		}, "0 steps; want 1 to 3"},
		{"steps out of order", func(f *fakeEngine) {
			f.out[contract.SdkVersion17_2_0] = `{"sdkVersion":"17.2.0","steps":[{"step":"decode","passed":false,"error":"x"}],"roundTripIdentical":false,"builtXdr":null,"reEncodedXdr":null}`
		}, `step 0 is "decode"; want "build"`},
		{"failed step without message", func(f *fakeEngine) {
			f.out[contract.SdkVersion17_2_0] = `{"sdkVersion":"17.2.0","steps":[{"step":"build","passed":false,"error":null}],"roundTripIdentical":false,"builtXdr":null,"reEncodedXdr":null}`
		}, "step build failed with no message"},
		{"passed step with message", func(f *fakeEngine) {
			f.out[contract.SdkVersion17_2_0] = `{"sdkVersion":"17.2.0","steps":[{"step":"build","passed":true,"error":"x"}],"roundTripIdentical":false,"builtXdr":null,"reEncodedXdr":null}`
		}, "step build passed with an error"},
		{"stopped after a passed step", func(f *fakeEngine) {
			f.out[contract.SdkVersion17_2_0] = `{"sdkVersion":"17.2.0","steps":[{"step":"build","passed":true,"error":null}],"roundTripIdentical":false,"builtXdr":null,"reEncodedXdr":null}`
		}, "it stopped after the passed step build"},
		{"ran on after a failure", func(f *fakeEngine) {
			f.out[contract.SdkVersion17_2_0] = `{"sdkVersion":"17.2.0","steps":[{"step":"build","passed":false,"error":"x"},{"step":"decode","passed":true,"error":null}],"roundTripIdentical":false,"builtXdr":null,"reEncodedXdr":null}`
		}, "steps run after the failed step build"},
		{"round trip claimed with different bytes", func(f *fakeEngine) {
			f.out[contract.SdkVersion17_2_1] = strings.Replace(fixture(t, "17.2.1-bytes-differ"), `"roundTripIdentical":false`, `"roundTripIdentical":true`, 1)
		}, "roundTripIdentical is true but the built and re-encoded bytes differ"},
		{"round trip claimed after a failure", func(f *fakeEngine) {
			f.out[contract.SdkVersion17_2_1] = `{"sdkVersion":"17.2.1","steps":[{"step":"build","passed":false,"error":"x"}],"roundTripIdentical":true,"builtXdr":null,"reEncodedXdr":null}`
		}, "roundTripIdentical is true without the three steps passed"},
		{"no roundTripIdentical", func(f *fakeEngine) {
			f.out[contract.SdkVersion17_2_0] = `{"sdkVersion":"17.2.0","steps":[{"step":"build","passed":false,"error":"x"}],"builtXdr":null,"reEncodedXdr":null}`
		}, "no roundTripIdentical"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeEngine(good0, good1)
			tc.setup(f)
			rep, err := run(context.Background(), f)
			if err == nil {
				t.Fatalf("no error; report %+v", rep)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q; want it to contain %q", err, tc.want)
			}
			if rep.Versions != nil || rep.Target != "" {
				t.Fatalf("a report came with the error: %+v", rep)
			}
		})
	}
}

// An interrupt is a cancelled context in the error chain, and the release's container is still removed.
func TestRunInterruptedIsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := newFakeEngine(fixture(t, "17.2.0-rejects"), fixture(t, "17.2.1-round-trips"))
	_, err := run(ctx, f)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v; want context.Canceled in its chain", err)
	}
	if !slices.Equal(f.removed, []string{"ltk-0123abcd-sdk-17-2-0"}) {
		t.Fatalf("removed %q", f.removed)
	}
}

// before/ and after/ each pin exactly one release of @stellar/stellar-sdk, and the committed lockfile
// resolves that release; nothing else is a direct dependency.
func TestPackageFilesPinTheReleases(t *testing.T) {
	for _, rel := range releases {
		b, err := files.ReadFile(rel.dir + "/package.json")
		if err != nil {
			t.Fatal(err)
		}
		var pkg struct {
			Private      bool              `json:"private"`
			Dependencies map[string]string `json:"dependencies"`
			Scripts      map[string]string `json:"scripts"`
		}
		if err := json.Unmarshal(b, &pkg); err != nil {
			t.Fatal(err)
		}
		if !pkg.Private || len(pkg.Dependencies) != 1 || pkg.Dependencies["@stellar/stellar-sdk"] != string(rel.version) || len(pkg.Scripts) != 0 {
			t.Fatalf("%s/package.json: %+v", rel.dir, pkg)
		}
		lb, err := files.ReadFile(rel.dir + "/package-lock.json")
		if err != nil {
			t.Fatal(err)
		}
		var lock struct {
			LockfileVersion int `json:"lockfileVersion"`
			Packages        map[string]struct {
				Version      string            `json:"version"`
				Resolved     string            `json:"resolved"`
				Integrity    string            `json:"integrity"`
				Dependencies map[string]string `json:"dependencies"`
			} `json:"packages"`
		}
		if err := json.Unmarshal(lb, &lock); err != nil {
			t.Fatal(err)
		}
		root, sdk := lock.Packages[""], lock.Packages["node_modules/@stellar/stellar-sdk"]
		if lock.LockfileVersion != 3 || root.Dependencies["@stellar/stellar-sdk"] != string(rel.version) || sdk.Version != string(rel.version) || sdk.Integrity == "" ||
			sdk.Resolved != "https://registry.npmjs.org/@stellar/stellar-sdk/-/stellar-sdk-"+string(rel.version)+".tgz" {
			t.Fatalf("%s/package-lock.json: lockfile %d, root %+v, sdk %+v", rel.dir, lock.LockfileVersion, root, sdk)
		}
		for path, p := range lock.Packages {
			if path != "" && (p.Integrity == "" || !strings.HasPrefix(p.Resolved, "https://registry.npmjs.org/")) {
				t.Fatalf("%s/package-lock.json: %s is not pinned to the registry with its integrity: %+v", rel.dir, path, p)
			}
		}
	}
}

// The script is run, never imported (it has no export and no class), and it holds no
// issuer or asset code of its own: they are its arguments, from internal/model, and no corpus file is read.
func TestScriptHoldsNoExportClassOrSeed(t *testing.T) {
	b, err := files.ReadFile("repro.mjs")
	if err != nil {
		t.Fatal(err)
	}
	code := regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(string(b), "")
	if regexp.MustCompile(`\bexport\b`).MatchString(code) || regexp.MustCompile(`\bclass\b`).MatchString(code) {
		t.Fatal("repro.mjs holds an export or a class statement")
	}
	for _, s := range []string{model.PoolIssuerLowerBytes, model.PoolIssuerHigherBytes, `"USD"`} {
		if strings.Contains(string(b), s) {
			t.Fatalf("repro.mjs holds %s; it takes it from its arguments", s)
		}
	}
	for _, s := range []string{"new LiquidityPoolAsset(", "Operation.fromXDRObject(", "Operation.changeTrust(", `"build"`, `"decode"`, `"re_encode"`} {
		if !strings.Contains(string(b), s) {
			t.Fatalf("repro.mjs lacks %s", s)
		}
	}
}
