//go:build integration

package orchestration_test

// The local network of `ltk generate`, seen from outside: its own labelled Docker network, the pinned images, ports
// bound to 127.0.0.1 only, friendbot funding, and nothing left behind. The built program, named by LTK_BINARY
// (or built from ../../cmd/ltk when that is unset and a go tool is found), generates entry 1 into a temporary folder
// while the test watches Docker from outside, every 200 ms, the containers and networks that are new since the run
// began. Run serially, with nothing else using Docker, natively:
//
//	LTK_BINARY=<ltk built for this system> go test -tags integration -p 1 -timeout 120m -run IntegrationGenerate ./internal/orchestration/

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// exeSuffix is name with .exe on Windows, so that the tests run there too.
func exeSuffix(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// ltkProgram is the program under test: LTK_BINARY made absolute when it is set (a missing file fails the
// test), otherwise ../../cmd/ltk built with the go tool found on PATH or under runtime.GOROOT(). What it returns has
// been run once (`ltk --help`); a program that cannot be had, or does not run on this system, skips the test saying
// why: a go tool on PATH may be a wrapper that builds somewhere else.
func ltkProgram(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("LTK_BINARY"); p != "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			t.Fatalf("LTK_BINARY: %v", err)
		}
		if _, err := os.Stat(abs); err != nil {
			t.Fatalf("LTK_BINARY: %v", err)
		}
		if err := exec.Command(abs, "--help").Run(); err != nil {
			t.Skipf("skipped: LTK_BINARY %s does not run on this system: %v", abs, err)
		}
		return abs
	}
	goBin, err := exec.LookPath("go")
	if err != nil && runtime.GOROOT() != "" {
		goBin = filepath.Join(runtime.GOROOT(), "bin", exeSuffix("go"))
		if _, statErr := os.Stat(goBin); statErr != nil {
			goBin = ""
		}
	}
	if goBin == "" {
		t.Skip("skipped: LTK_BINARY is not set and no go tool is on PATH or under runtime.GOROOT()")
	}
	out := filepath.Join(t.TempDir(), exeSuffix("ltk"))
	if b, err := exec.Command(goBin, "build", "-o", out, "../../cmd/ltk").CombinedOutput(); err != nil {
		t.Fatalf("go build ../../cmd/ltk: %v\n%s", err, b)
	}
	if err := exec.Command(out, "--help").Run(); err != nil {
		t.Skipf("skipped: the go tool %s built no ltk that runs here: %v", goBin, err)
	}
	return out
}

// portBinding is one host binding of a published port, as `docker inspect` reports it.
type portBinding struct {
	HostIp   string
	HostPort string
}

// inspectedContainer is the part of `docker inspect --type container` the test reads.
type inspectedContainer struct {
	ID    string `json:"Id"`
	Name  string
	Image string
	Path  string
	Args  []string
	State struct{ Status string }
	// Config.Image is the reference the container was started from.
	Config struct {
		Image  string
		Labels map[string]string
	}
	HostConfig struct {
		NetworkMode  string
		PortBindings map[string][]portBinding
	}
	NetworkSettings struct {
		Ports    map[string][]portBinding
		Networks map[string]json.RawMessage
	}
}

// inspectedNetwork is the part of `docker network inspect` the test reads.
type inspectedNetwork struct {
	ID         string `json:"Id"`
	Name       string
	Driver     string
	Labels     map[string]string
	Containers map[string]struct{ Name string }
}

// containerSeen is everything the watcher saw of one container over the run.
type containerSeen struct {
	first    inspectedContainer
	states   map[string]bool
	bindings map[string]bool // "<container port> <host ip>:<host port>", from HostConfig and NetworkSettings
	networks map[string]bool
	samples  int
}

// networkSeen is everything the watcher saw of one network over the run.
type networkSeen struct {
	first    inspectedNetwork
	attached map[string]bool // container names
	samples  int
}

// watcher polls Docker from outside while the program runs: every container and network that is not in the snapshot
// taken before the run is inspected and recorded.
type watcher struct {
	before orchestration.Snapshot
	mu     sync.Mutex
	conts  map[string]*containerSeen
	nets   map[string]*networkSeen
	polls  int
	errs   []string
}

// newIDs lists the ids `docker <list...>` prints that are not keys of known.
func newIDs(known map[string]string, list ...string) ([]string, error) {
	out, err := exec.Command("docker", list...).Output()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w", strings.Join(list, " "), err)
	}
	var ids []string
	for _, id := range strings.Fields(string(out)) {
		if _, ok := known[id]; !ok {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// inspectEach runs `docker <inspect...> <id>` for each id and decodes the one-element array into v's element; an id
// that vanished between the listing and the inspection is skipped.
func inspectEach[T any](ids []string, inspect ...string) []T {
	var out []T
	for _, id := range ids {
		b, err := exec.Command("docker", append(append([]string{}, inspect...), id)...).Output()
		if err != nil {
			continue // removed since the listing
		}
		var v []T
		if json.Unmarshal(b, &v) == nil && len(v) == 1 {
			out = append(out, v[0])
		}
	}
	return out
}

// poll takes one sample.
func (w *watcher) poll() {
	cids, err := newIDs(w.before.Containers, "ps", "-a", "-q", "--no-trunc")
	if err != nil {
		w.mu.Lock()
		w.errs = append(w.errs, err.Error())
		w.mu.Unlock()
		return
	}
	nids, err := newIDs(w.before.Networks, "network", "ls", "-q", "--no-trunc")
	if err != nil {
		w.mu.Lock()
		w.errs = append(w.errs, err.Error())
		w.mu.Unlock()
		return
	}
	conts := inspectEach[inspectedContainer](cids, "inspect", "--type", "container")
	nets := inspectEach[inspectedNetwork](nids, "network", "inspect")
	w.mu.Lock()
	defer w.mu.Unlock()
	w.polls++
	for _, c := range conts {
		name := strings.TrimPrefix(c.Name, "/")
		s := w.conts[name]
		if s == nil {
			s = &containerSeen{first: c, states: map[string]bool{}, bindings: map[string]bool{}, networks: map[string]bool{}}
			w.conts[name] = s
		}
		s.samples++
		s.states[c.State.Status] = true
		for _, ports := range []map[string][]portBinding{c.HostConfig.PortBindings, c.NetworkSettings.Ports} {
			for port, bs := range ports {
				for _, b := range bs {
					s.bindings[port+" "+b.HostIp+":"+b.HostPort] = true
				}
			}
		}
		for n := range c.NetworkSettings.Networks {
			s.networks[n] = true
		}
	}
	for _, n := range nets {
		s := w.nets[n.Name]
		if s == nil {
			s = &networkSeen{first: n, attached: map[string]bool{}}
			w.nets[n.Name] = s
		}
		s.samples++
		for _, c := range n.Containers {
			s.attached[c.Name] = true
		}
	}
}

// keys returns m's keys, sorted.
func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// imageID is the local id of an image reference, `docker image inspect --format {{.Id}}`.
func imageID(t *testing.T, ref string) string {
	t.Helper()
	return dockerOut(t, "image", "inspect", "--format", "{{.Id}}", ref)
}

// friendbotGrant is what quickstart's local friendbot gives a new account: 10,000 lumens, in stroops (the balance of
// every freshly funded account in the captures of phases 2 and 3).
const friendbotGrant = "100000000000"

// TestIntegrationGenerateLocalNetwork: `ltk generate --shape cap-0040-signed-payload-signer --out <empty folder>`
// exits 0, and from outside, while it runs:
//   - its quickstart container ltk-<runID>-core runs the pinned current digest (the reference images.go holds, and
//     that image's local id) with exactly `--local --enable core,rpc`, attached only to ltk-<runID>-net, a bridge
//     network that carries both labels and holds no other container;
//   - every published port of it is bound to 127.0.0.1 (never 0.0.0.0 or ::), and only its port 8000 is published;
//   - the only other container, the Rust XDR tool ltk-<runID>-xdr, runs the same digest on no network and publishes
//     nothing; every new container and network carries ltk.managed=true and ltk.run=<runID>;
//
// and after it:
//   - the capture is the LedgerCloseMeta RPC returned (its record says so and names the digest), whose one transaction
//     is sent by the entry's fixed-seed source account, an account created in an earlier ledger with exactly the
//     local friendbot's grant before the fee;
//   - the command's own check passed: the Rust XDR tool round-tripped every byte of the file;
//   - the before/after difference of every container, network and volume, and the label-filtered listings, are empty.
func TestIntegrationGenerateLocalNetwork(t *testing.T) {
	bin := ltkProgram(t)
	for _, l := range managedListings(t) {
		if l != "" {
			t.Fatalf("ltk.managed resources exist before the test; clean them by hand (docker rm -f -v, docker network rm and docker volume rm, filtered by label=ltk.managed=true): %q", l)
		}
	}
	snap, err := orchestration.NewRunner("00000000")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	before, err := snap.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := snap.EnsureImages(ctx, []string{orchestration.QuickstartCurrent}); err != nil {
		t.Fatal(err)
	}
	pinnedID := imageID(t, orchestration.QuickstartCurrent)

	out := filepath.Join(t.TempDir(), "gen")
	id := model.SeedSignedPayloadSigner
	cmd := exec.Command(bin, "generate", "--shape", id, "--out", out)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	w := &watcher{before: before, conts: map[string]*containerSeen{}, nets: map[string]*networkSeen{}}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var runErr error
watch:
	for {
		w.poll()
		select {
		case runErr = <-done:
			break watch
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Logf("ltk generate --shape %s --out %s: %v after %s; %d samples of Docker taken", id, out, runErr, time.Since(start).Round(time.Second), w.polls)
	after, err := snap.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if left := orchestration.Leftovers(before, after); len(left) != 0 {
		t.Errorf("the run left %q (the before/after difference is not empty)", left)
	}
	for i, l := range managedListings(t) {
		if l != "" {
			t.Errorf("label-filtered listing %d is not empty after the run: %q", i, l)
		}
	}
	if runErr != nil || stderr.Len() != 0 {
		t.Fatalf("ltk generate: %v; stderr %s; want exit 0 and nothing\n%s", runErr, stderr.Bytes(), stdout.Bytes())
	}
	t.Logf("report:\n%s", stdout.Bytes())
	for _, e := range w.errs {
		t.Logf("a sample failed: %s", e)
	}

	// The report: one shape, generated at the current protocol, and the command's own check passed with the Rust
	// XDR tool's round trip over every byte.
	var rep contract.GenerateReport
	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rep); err != nil {
		t.Fatalf("stdout is not a GenerateReport: %v", err)
	}
	if len(rep.Shapes) != 1 || rep.Shapes[0].ID != id || !rep.Shapes[0].Generated || rep.Shapes[0].ProtocolFallback ||
		rep.Shapes[0].ProtocolVersion != orchestration.CurrentProtocol {
		t.Fatalf("shapes %+v; want %s generated at protocol %d without fallback", rep.Shapes, id, orchestration.CurrentProtocol)
	}
	if rep.Check == nil || !rep.Check.Passed || len(rep.Check.Entries) != 1 {
		t.Fatalf("the command's check did not pass: %+v", rep.Check)
	}
	ec := rep.Check.Entries[0]
	if !ec.FileIntegrity.Passed || ec.FileIntegrity.Length == 0 || !ec.RoundTrip.Passed || ec.RoundTrip.BytesCompared != ec.FileIntegrity.Length {
		t.Errorf("%s: file integrity %+v, round trip %+v; want the Rust XDR tool to round-trip every byte", id, ec.FileIntegrity, ec.RoundTrip)
	}
	t.Logf("%s: the Rust XDR tool round-tripped %d of %d bytes; expected values passed: %v", id, ec.RoundTrip.BytesCompared, ec.FileIntegrity.Length, ec.ExpectedValues.Passed)

	// What the watcher saw: one run id, the quickstart's and the Rust XDR tool's containers and
	// the quickstart's one network.
	w.mu.Lock()
	defer w.mu.Unlock()
	t.Logf("new containers seen: %v; new networks seen: %v", keys(w.conts), keys(w.nets))
	runID := ""
	for name, s := range w.conts {
		labels := s.first.Config.Labels
		if labels[orchestration.LabelManaged] != "true" || labels[orchestration.LabelRun] == "" {
			t.Errorf("container %s is new during the run and lacks the labels: %v", name, labels)
			continue
		}
		if runID == "" {
			runID = labels[orchestration.LabelRun]
		} else if labels[orchestration.LabelRun] != runID {
			t.Errorf("container %s carries ltk.run=%s; another carries %s", name, labels[orchestration.LabelRun], runID)
		}
	}
	if runID == "" {
		t.Fatal("the watcher saw no labelled container during the run")
	}
	core, xdr, netName := "ltk-"+runID+"-core", "ltk-"+runID+"-xdr", "ltk-"+runID+"-net"
	if got := keys(w.conts); !slices.Equal(got, []string{core, xdr}) {
		t.Errorf("containers %v; want exactly %s and %s", got, core, xdr)
	}
	for name, s := range w.nets {
		if s.first.Labels[orchestration.LabelManaged] != "true" || s.first.Labels[orchestration.LabelRun] != runID {
			t.Errorf("network %s is new during the run with labels %v; want ltk.managed=true and ltk.run=%s", name, s.first.Labels, runID)
		}
	}
	if got := keys(w.nets); !slices.Equal(got, []string{netName}) {
		t.Errorf("networks %v; want exactly %s", got, netName)
	}

	// The quickstart: the pinned digest, local mode with core and RPC only, on its own network, 127.0.0.1 only.
	if c := w.conts[core]; c == nil {
		t.Errorf("the quickstart container %s was never seen", core)
	} else {
		t.Logf("%s: image %s (%s), %s %q, network mode %s, networks %v, states %v, bindings %v, %d samples",
			core, c.first.Config.Image, c.first.Image, c.first.Path, c.first.Args, c.first.HostConfig.NetworkMode,
			keys(c.networks), keys(c.states), keys(c.bindings), c.samples)
		if c.first.Config.Image != orchestration.QuickstartCurrent || c.first.Image != pinnedID {
			t.Errorf("%s runs %s (%s); want %s (%s)", core, c.first.Config.Image, c.first.Image, orchestration.QuickstartCurrent, pinnedID)
		}
		if !slices.Equal(c.first.Args, []string{"--local", "--enable", "core,rpc"}) {
			t.Errorf("%s arguments %q; want exactly --local --enable core,rpc", core, c.first.Args)
		}
		if c.first.HostConfig.NetworkMode != netName || !slices.Equal(keys(c.networks), []string{netName}) {
			t.Errorf("%s network mode %s, networks %v; want only %s", core, c.first.HostConfig.NetworkMode, keys(c.networks), netName)
		}
		if !c.states["running"] {
			t.Errorf("%s was never seen running: %v", core, keys(c.states))
		}
		live := 0
		for b := range c.bindings {
			port, addr, _ := strings.Cut(b, " ")
			host := addr[:strings.LastIndex(addr, ":")]
			if port != "8000/tcp" || host != "127.0.0.1" {
				t.Errorf("%s publishes %s on %s; want only 8000/tcp on 127.0.0.1 (never 0.0.0.0 or ::)", core, port, addr)
			}
			if !strings.HasSuffix(addr, ":") {
				live++ // a binding with its host port assigned, from the running container
			}
		}
		if live == 0 {
			t.Errorf("%s was never seen with its port bound: %v", core, keys(c.bindings))
		}
	}
	if c := w.conts[xdr]; c == nil {
		t.Errorf("the Rust XDR tool's container %s was never seen", xdr)
	} else {
		t.Logf("%s: image %s, entrypoint %s %q, network mode %s, bindings %v", xdr, c.first.Config.Image, c.first.Path, c.first.Args, c.first.HostConfig.NetworkMode, keys(c.bindings))
		if c.first.Config.Image != orchestration.QuickstartCurrent || c.first.Image != pinnedID {
			t.Errorf("%s runs %s; want the pinned %s", xdr, c.first.Config.Image, orchestration.QuickstartCurrent)
		}
		if c.first.HostConfig.NetworkMode != "none" || len(c.bindings) != 0 {
			t.Errorf("%s network mode %s, bindings %v; want none and nothing published", xdr, c.first.HostConfig.NetworkMode, keys(c.bindings))
		}
	}
	if n := w.nets[netName]; n != nil {
		t.Logf("%s: driver %s, labels %v, containers attached %v, %d samples", netName, n.first.Driver, n.first.Labels, keys(n.attached), n.samples)
		if n.first.Driver != "bridge" || !slices.Equal(keys(n.attached), []string{core}) {
			t.Errorf("%s: driver %s, attached %v; want a bridge network holding only %s", netName, n.first.Driver, keys(n.attached), core)
		}
	}

	// The capture and its record.
	var cat struct {
		Entries []struct {
			ID, XDRType, Source string
		} `json:"entries"`
	}
	b, err := os.ReadFile(filepath.Join(out, "catalogue.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cat); err != nil || len(cat.Entries) != 1 {
		t.Fatalf("catalogue: %v: %s", err, b)
	}
	digest := orchestration.QuickstartCurrent[strings.Index(orchestration.QuickstartCurrent, "@")+1:]
	if r := cat.Entries[0]; r.ID != id || r.XDRType != "LedgerCloseMeta" || !strings.Contains(r.Source, digest) || !strings.Contains(r.Source, "getLedgers") {
		t.Errorf("record %+v; want %s, LedgerCloseMeta, a source naming %s and getLedgers", r, id, digest)
	}
	source, err := model.AccountKey(id, "source")
	if err != nil {
		t.Fatal(err)
	}
	checkFriendbotFunded(t, filepath.Join(out, id, "entry.json"), source.Address())
}

// checkFriendbotFunded reads the decoded capture and proves that its one transaction is sent by address, an account
// created in an earlier ledger and holding exactly the local friendbot's grant before the fee.
func checkFriendbotFunded(t *testing.T, path, address string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lcm struct {
		V2 struct {
			LedgerHeader struct {
				Header struct {
					LedgerSeq json.Number `json:"ledger_seq"`
				} `json:"header"`
			} `json:"ledger_header"`
			TxProcessing []struct {
				FeeProcessing []struct {
					State *struct {
						LastModified json.Number `json:"last_modified_ledger_seq"`
						Data         struct {
							Account *struct {
								AccountID string      `json:"account_id"`
								Balance   json.Number `json:"balance"`
								SeqNum    json.Number `json:"seq_num"`
							} `json:"account"`
						} `json:"data"`
					} `json:"state"`
				} `json:"fee_processing"`
			} `json:"tx_processing"`
		} `json:"v2"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&lcm); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	ledger, err := lcm.V2.LedgerHeader.Header.LedgerSeq.Int64()
	if err != nil || len(lcm.V2.TxProcessing) != 1 || len(lcm.V2.TxProcessing[0].FeeProcessing) == 0 {
		t.Fatalf("%s: ledger %v (%v), %d transactions; want one", path, lcm.V2.LedgerHeader.Header.LedgerSeq, err, len(lcm.V2.TxProcessing))
	}
	st := lcm.V2.TxProcessing[0].FeeProcessing[0].State
	if st == nil || st.Data.Account == nil {
		t.Fatalf("%s: the fee's first change is not the source account's state", path)
	}
	acct := st.Data.Account
	seq, errSeq := acct.SeqNum.Int64()
	modified, errMod := st.LastModified.Int64()
	created := seq >> 32
	t.Logf("capture of ledger %d: the source %s held %s stroops before the fee, sequence %d (created in ledger %d), last modified in ledger %d",
		ledger, acct.AccountID, acct.Balance, seq, created, modified)
	if acct.AccountID != address {
		t.Errorf("the transaction's source is %s; want the fixed-seed account %s", acct.AccountID, address)
	}
	if acct.Balance.String() != friendbotGrant {
		t.Errorf("the source held %s stroops before the fee; want the friendbot's grant %s", acct.Balance, friendbotGrant)
	}
	if errSeq != nil || errMod != nil || created == 0 || created >= ledger || seq != created<<32 {
		t.Errorf("sequence %s, last modified %s: want an account created in an earlier ledger than %d and unused since", acct.SeqNum, st.LastModified, ledger)
	}
}

// TestIntegrationGenerateWithoutDocker: on a host where the tool cannot use Docker
// with Linux containers, `ltk generate --all --out <folder>` exits 3 with nothing on standard output and one ErrorLine
// `environment_missing` stating that Docker with Linux containers is missing, creates no folder and nothing in Docker.
// Two ways to lack it on the build machine: no `docker` on PATH, and a `docker` whose daemon does not answer.
func TestIntegrationGenerateWithoutDocker(t *testing.T) {
	bin := ltkProgram(t)
	snap, err := orchestration.NewRunner("00000000")
	if err != nil {
		t.Fatal(err)
	}
	before, err := snap.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var base []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PATH=") && !strings.HasPrefix(kv, "DOCKER_HOST=") && !strings.HasPrefix(kv, "DOCKER_CONTEXT=") {
			base = append(base, kv)
		}
	}
	for _, c := range []struct {
		name, want string
		env        []string
	}{
		{"no docker CLI on PATH", "docker CLI not found on PATH", []string{"PATH=" + t.TempDir()}},
		// Port 1 of the loopback, where nothing listens. A unix socket under t.TempDir() is not used: its path is
		// longer than macOS's socket path limit, and docker CLI 29.2.1 then panics instead of reporting it (the first
		// native run of 2026-11-16).
		{"a daemon that does not answer", "Docker daemon not reachable", []string{"PATH=" + os.Getenv("PATH"), "DOCKER_HOST=tcp://127.0.0.1:1"}},
	} {
		out := filepath.Join(t.TempDir(), "gen")
		cmd := exec.Command(bin, "generate", "--all", "--out", out)
		cmd.Env = append(append([]string{}, base...), c.env...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		t.Logf("%s: exit %d; stderr %s", c.name, code, bytes.TrimSpace(stderr.Bytes()))
		if code != int(contract.EXIT_CODES.EnvironmentMissing) || stdout.Len() != 0 {
			t.Errorf("%s: exit %d, stdout %q; want 3 and nothing", c.name, code, stdout.Bytes())
		}
		if bytes.Count(stderr.Bytes(), []byte("\n")) != 1 || !bytes.HasSuffix(stderr.Bytes(), []byte("\n")) {
			t.Errorf("%s: stderr is not one line: %q", c.name, stderr.Bytes())
		}
		var line contract.ErrorLine
		dec := json.NewDecoder(bytes.NewReader(stderr.Bytes()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&line); err != nil {
			t.Errorf("%s: stderr is not an ErrorLine: %v", c.name, err)
		} else if line.Code != contract.ErrorCodeEnvironmentMissing ||
			!strings.HasPrefix(line.Message, "ltk generate: Docker with Linux containers is not available: ") || !strings.Contains(line.Message, c.want) {
			t.Errorf("%s: %+v; want environment_missing stating Docker with Linux containers is missing (%s)", c.name, line, c.want)
		}
		if _, err := os.Lstat(out); !os.IsNotExist(err) {
			t.Errorf("%s: the folder %s exists (%v)", c.name, out, err)
		}
	}
	after, err := snap.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if left := orchestration.Leftovers(before, after); len(left) != 0 {
		t.Errorf("Docker holds new resources: %q", left)
	}
}
