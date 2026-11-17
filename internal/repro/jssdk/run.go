package jssdk

// The js-sdk reproduction runner. For 17.2.0, then 17.2.1, it starts one labelled node:22-slim container, copies the
// embedded script and that release's package.json and package-lock.json into it with `docker cp`, runs
// `npm ci --ignore-scripts`, then `node repro.mjs <code> <issuer A> <issuer B>` with entry 2's asset code and issuers
// in key-byte order from internal/model (no corpus file is read), and parses the script's one JSON line into the
// release's SdkVersionResult. The verdict (verdict.go) sets `passed`.

import (
	"archive/tar"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// files are the script and the two releases' package files, embedded so that the tool carries them.
//
//go:embed repro.mjs before/package.json before/package-lock.json after/package.json after/package-lock.json
var files embed.FS

const (
	// copyDir is the container directory the tar stream is extracted into; it exists in every Linux image.
	copyDir = "/tmp"
	// scriptDir is where the script and the package files land: copyDir/ltk-repro.
	scriptDir = copyDir + "/ltk-repro"
	// removeTimeout bounds the removal of a release's container after its run; the run's reaper removes whatever this
	// leaves.
	removeTimeout = 2 * time.Minute
)

// release is one @stellar/stellar-sdk release under test.
type release struct {
	version contract.SdkVersion
	role    contract.FixRole
	// dir is its package files' directory in files: before/ or after/.
	dir string
	// containerRole names its container, ltk-<runID>-<containerRole>.
	containerRole string
}

// releases are the two releases, in the report's order: 17.2.0 (before_fix), then 17.2.1 (fix).
var releases = [...]release{
	{version: contract.SdkVersion17_2_0, role: contract.FixRoleBeforeFix, dir: "before", containerRole: "sdk-17-2-0"},
	{version: contract.SdkVersion17_2_1, role: contract.FixRoleFix, dir: "after", containerRole: "sdk-17-2-1"},
}

// engine is the part of the Docker CLI runner the reproduction uses; *orchestration.Runner satisfies it, and the unit
// tests give a fake: the verdict and the runner are tested without containers.
type engine interface {
	Run(ctx context.Context, spec orchestration.ContainerSpec) (string, error)
	CopyInto(ctx context.Context, container, dir string, tarball []byte) error
	Exec(ctx context.Context, container string, stdin []byte, args ...string) (stdout, stderr []byte, err error)
	Remove(ctx context.Context, container string) error
}

// Run reproduces js-stellar-sdk 1766 on the run whose Docker CLI runner is r: both releases in order, each in its own
// container, and the report with `versions` (17.2.0 before_fix, 17.2.1 fix) and `passed`, true when 17.2.0 fails at
// build with "Assets are not in lexicographic order" and 17.2.1 passes every step with identical bytes.
// `ltk repro js-sdk-1766` (cmd/ltk/repro_jssdk.go) calls it inside its withContainers scope with JsSdkImages ensured,
// saves the report under repro/ and prints it, and exits 0 when passed, else 1 with Reasons in its ErrorLine: the
// report's schema has no field for them.
//
// The error is non-nil only for a run that could not produce a report (a crashing script is never read as the bug):
// an interrupt (the context's cancellation is in its chain), a container that could not start or take the files,
// `npm ci` failing, or a script that printed no line, or a line that is not the JSON line of its release.
func Run(ctx context.Context, r *orchestration.Runner) (contract.JsSdk1766Report, error) {
	return run(ctx, r)
}

// run is Run on any engine.
func run(ctx context.Context, eng engine) (contract.JsSdk1766Report, error) {
	rep := contract.JsSdk1766Report{Target: contract.ReproTargetJsSdk1766}
	for _, rel := range releases {
		res, err := runRelease(ctx, eng, rel)
		if err != nil {
			return contract.JsSdk1766Report{}, fmt.Errorf("js-sdk-1766: @stellar/stellar-sdk %s: %w", rel.version, err)
		}
		rep.Versions = append(rep.Versions, res)
	}
	rep.Passed = len(verdict(rep)) == 0
	return rep, nil
}

// scriptArgs are the script's arguments: entry 2's asset code and its issuers in key-byte order, the pool the
// protocol accepts. They come from the tool's own seed definitions, the source `ltk generate` uses; no corpus file
// is read.
func scriptArgs() []string {
	return []string{model.PoolAssetCode, model.PoolIssuerLowerBytes, model.PoolIssuerHigherBytes}
}

// runRelease runs the script against one release in its own container and returns its parsed result.
func runRelease(ctx context.Context, eng engine, rel release) (contract.SdkVersionResult, error) {
	name, err := eng.Run(ctx, orchestration.ContainerSpec{
		Role:       rel.containerRole,
		Image:      orchestration.NodeImage,
		Entrypoint: "sleep",
		Args:       []string{"infinity"},
	})
	if err != nil {
		return contract.SdkVersionResult{}, err
	}
	defer func() {
		// The container goes as soon as its release is done; the reaper still removes it if this fails.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), removeTimeout)
		defer cancel()
		_ = eng.Remove(rctx, name)
	}()
	tarball, err := packageTar(rel)
	if err != nil {
		return contract.SdkVersionResult{}, err
	}
	if err := eng.CopyInto(ctx, name, copyDir, tarball); err != nil {
		return contract.SdkVersionResult{}, err
	}
	if _, stderr, err := eng.Exec(ctx, name, nil, "sh", "-c", "cd "+scriptDir+" && npm ci --ignore-scripts --no-audit --no-fund"); err != nil {
		if ctx.Err() != nil {
			return contract.SdkVersionResult{}, err
		}
		return contract.SdkVersionResult{}, fmt.Errorf("npm ci --ignore-scripts failed: %w%s", err, tail(stderr))
	}
	args := append([]string{"node", scriptDir + "/repro.mjs"}, scriptArgs()...)
	stdout, stderr, err := eng.Exec(ctx, name, nil, args...)
	if err != nil {
		if ctx.Err() != nil {
			return contract.SdkVersionResult{}, err
		}
		return contract.SdkVersionResult{}, fmt.Errorf("the reproduction script printed no result line: %w%s", err, tail(stderr))
	}
	res, err := parseLine(stdout, rel.version)
	if err != nil {
		return contract.SdkVersionResult{}, err
	}
	res.Role = rel.role
	return res, nil
}

// packageTar is the tar stream of ltk-repro/ with the script and the release's package.json and package-lock.json.
func packageTar(rel release) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "ltk-repro/", Mode: 0o755}); err != nil {
		return nil, err
	}
	for _, f := range []struct{ src, dst string }{
		{"repro.mjs", "repro.mjs"},
		{rel.dir + "/package.json", "package.json"},
		{rel.dir + "/package-lock.json", "package-lock.json"},
	} {
		b, err := files.ReadFile(f.src)
		if err != nil {
			return nil, err
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "ltk-repro/" + f.dst, Mode: 0o644, Size: int64(len(b))}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(b); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// scriptLine is the script's one JSON line (repro.mjs).
type scriptLine struct {
	SdkVersion string `json:"sdkVersion"`
	Steps      []struct {
		Step   contract.SdkReproStep `json:"step"`
		Passed *bool                 `json:"passed"`
		Error  *string               `json:"error"`
	} `json:"steps"`
	RoundTripIdentical *bool   `json:"roundTripIdentical"`
	BuiltXdr           *string `json:"builtXdr"`
	ReEncodedXdr       *string `json:"reEncodedXdr"`
}

// stepOrder is the order of the script's steps in the report.
var stepOrder = [...]contract.SdkReproStep{contract.SdkReproStepBuild, contract.SdkReproStepDecode, contract.SdkReproStepReEncode}

// parseLine reads the script's standard output: exactly one JSON line from the release want, its steps a prefix of
// build, decode, re_encode that stops at the first failed step, a failed step with its message and a passed one
// without, and roundTripIdentical true only with the three steps passed and the two byte strings equal. Anything else
// is an error of the run, never a rejection.
func parseLine(stdout []byte, want contract.SdkVersion) (contract.SdkVersionResult, error) {
	text := strings.TrimRight(string(stdout), "\r\n")
	if text == "" || strings.ContainsAny(text, "\r\n") {
		return contract.SdkVersionResult{}, fmt.Errorf("the reproduction script printed %q; want exactly one JSON line", clip(string(stdout)))
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	var line scriptLine
	if err := dec.Decode(&line); err != nil {
		return contract.SdkVersionResult{}, fmt.Errorf("the reproduction script's line is not its JSON result: %v: %q", err, clip(text))
	}
	if dec.More() {
		return contract.SdkVersionResult{}, fmt.Errorf("the reproduction script's line holds more than one JSON value: %q", clip(text))
	}
	bad := func(format string, args ...any) error {
		return fmt.Errorf("the reproduction script's line is malformed: "+format, args...)
	}
	if line.SdkVersion != string(want) {
		return contract.SdkVersionResult{}, fmt.Errorf("the reproduction script ran @stellar/stellar-sdk %q, not %s", line.SdkVersion, want)
	}
	if len(line.Steps) == 0 || len(line.Steps) > len(stepOrder) {
		return contract.SdkVersionResult{}, bad("%d steps; want 1 to 3", len(line.Steps))
	}
	if line.RoundTripIdentical == nil {
		return contract.SdkVersionResult{}, bad("no roundTripIdentical")
	}
	res := contract.SdkVersionResult{Version: want, RoundTripIdentical: *line.RoundTripIdentical}
	for i, s := range line.Steps {
		switch {
		case s.Step != stepOrder[i]:
			return contract.SdkVersionResult{}, bad("step %d is %q; want %q", i, s.Step, stepOrder[i])
		case s.Passed == nil:
			return contract.SdkVersionResult{}, bad("step %s has no passed", s.Step)
		case *s.Passed && s.Error != nil:
			return contract.SdkVersionResult{}, bad("step %s passed with an error", s.Step)
		case !*s.Passed && (s.Error == nil || *s.Error == ""):
			return contract.SdkVersionResult{}, bad("step %s failed with no message", s.Step)
		case !*s.Passed && i != len(line.Steps)-1:
			return contract.SdkVersionResult{}, bad("steps run after the failed step %s", s.Step)
		}
		res.Steps = append(res.Steps, contract.SdkStepResult{Step: s.Step, Passed: *s.Passed, Error: s.Error})
	}
	last := line.Steps[len(line.Steps)-1]
	if len(line.Steps) < len(stepOrder) && *last.Passed {
		return contract.SdkVersionResult{}, bad("it stopped after the passed step %s", last.Step)
	}
	if res.RoundTripIdentical {
		if len(line.Steps) != len(stepOrder) || !*last.Passed {
			return contract.SdkVersionResult{}, bad("roundTripIdentical is true without the three steps passed")
		}
		if line.BuiltXdr == nil || line.ReEncodedXdr == nil || *line.BuiltXdr != *line.ReEncodedXdr {
			return contract.SdkVersionResult{}, bad("roundTripIdentical is true but the built and re-encoded bytes differ")
		}
	}
	return res, nil
}

// tail is "; standard error: <its last lines>" when stderr is not empty: what npm or node said before it failed.
func tail(stderr []byte) string {
	s := strings.TrimSpace(string(stderr))
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return "; standard error: " + clip(strings.Join(lines, " | "))
}

// clip bounds a text quoted in an error message to 300 bytes.
func clip(s string) string {
	const max = 300
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
