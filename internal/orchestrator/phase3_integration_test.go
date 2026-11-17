//go:build integration

package orchestrator

// The integration verification of `ltk repro` and `ltk generate`, Docker tier: each command run as a user runs it,
// through the built program, from outside:
//
//   - `ltk repro wallet-backend-729`: exit 0; the report on standard output byte-equal to
//     repro/wallet-backend-729.json, the only file under the working folder; the before_fix commit failing on ledger L
//     five times and exiting, twice (the run and its restart), the fix commit storing its SIGNER row at the same L;
//     the evidence files in the system's temporary directory; and, watched from outside while it runs, the build
//     container's Go environment (GOSUMDB, GOPROXY, GOFLAGS=-mod=readonly), two TimescaleDB containers with no
//     published port, and both ingesters reading the one RPC of the run's network, each writing its own database;
//   - `ltk repro js-sdk-1766`: exit 0; 17.2.0 rejected at build with "Assets are not in lexicographic order", 17.2.1
//     round-tripped;
//   - `ltk generate --all`, `ltk check --also-go`, a second `ltk generate --all`, `ltk compare` of the two folders, and
//     `ltk generate --shape` into an existing folder (only that entry's files and record change).
//
// Every run is held to the clean stop: no labelled resource before it, the difference of every
// container, network and volume before and after it empty, the three label-filtered listings empty after it. Every
// resource the run created, seen from outside while it ran, carries the labels ltk.managed=true and ltk.run=<runID>,
// and every container's image is a digest reference written in the repository's digest file.
//
// The deadline path of a reproduction's commit run cannot be waited for on the real program: the deadline is a
// parameter of the runner, and the path is shown on the unit tier with an injected deadline (phase3_test.go,
// TestPhase3DeadlinePathEvidence). Here the pre-fix commit shows its expected behaviour within the
// deadline the program gives it.
//
// Run natively, one test binary at a time, with nothing else using Docker and a PATH that holds no wrapper that runs
// the go tool in a container; see doc.go.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// imagesFile is the committed file that holds the image digests: every image is pinned by a digest written in the
// repository.
const imagesFile = "../orchestration/images.go"

// committedImages reads the digest references written in imagesFile, by the name of their constant.
func committedImages(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(imagesFile)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\s*([A-Za-z0-9]+)\s*=\s*"([a-z0-9./_-]+@sha256:[0-9a-f]{64})"`).FindAllStringSubmatch(string(b), -1) {
		out[m[1]] = m[2]
	}
	for _, name := range []string{"QuickstartCurrent", "QuickstartProtocol28", "GoBuildImage", "TimescaleDBImage", "NodeImage"} {
		if out[name] == "" {
			t.Fatalf("%s holds no digest reference for %s", imagesFile, name)
		}
	}
	return out
}

// currentProtocol is the protocol the current quickstart declares, as the digest file records it.
func currentProtocol(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(imagesFile)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\s*CurrentProtocol\s*=\s*(\d+)`).FindSubmatch(b)
	if m == nil {
		t.Fatalf("%s states no CurrentProtocol", imagesFile)
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

// integrationEnv is the program's environment in the integration tier: what the docker CLI needs (PATH, HOME and any
// DOCKER_* variable of the test's own) and extra; nothing else, so no key of any network reaches the program
// (ltk runs on a machine with Docker and no network keys).
func integrationEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "PATH" || k == "HOME" || strings.HasPrefix(k, "DOCKER_") {
			env = append(env, kv)
		}
	}
	return append(env, extra...)
}

// dockerOut runs the docker CLI and returns its standard output; it never fails the test (the watcher calls it from
// its own goroutine).
func dockerOut(args ...string) (string, error) {
	var out, errOut bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// portBinding is one host binding of a published port.
type portBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

// containerSeen is a container of the run as `docker inspect` showed it the first time the watcher saw it.
type containerSeen struct {
	Name   string `json:"Name"`
	Config struct {
		Image      string            `json:"Image"`
		Env        []string          `json:"Env"`
		Cmd        []string          `json:"Cmd"`
		Entrypoint []string          `json:"Entrypoint"`
		Labels     map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		NetworkMode     string                   `json:"NetworkMode"`
		PortBindings    map[string][]portBinding `json:"PortBindings"`
		PublishAllPorts bool                     `json:"PublishAllPorts"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]json.RawMessage `json:"Networks"`
	} `json:"NetworkSettings"`
}

// env is the value of the container's environment variable k, and whether it is set.
func (c containerSeen) env(k string) (string, bool) {
	for _, kv := range c.Config.Env {
		if name, v, ok := strings.Cut(kv, "="); ok && name == k {
			return v, true
		}
	}
	return "", false
}

// networks are the names of the networks the container is attached to.
func (c containerSeen) networks() []string {
	var out []string
	for n := range c.NetworkSettings.Networks {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// published are the container's published ports with their host bindings, "<port> <ip>:<hostport>".
func (c containerSeen) published() []string {
	var out []string
	for port, bs := range c.HostConfig.PortBindings {
		for _, b := range bs {
			out = append(out, port+" "+b.HostIP+":"+b.HostPort)
		}
	}
	sort.Strings(out)
	return out
}

// watcher watches the engine from outside while a command runs: every resource named ltk-… is inspected once, and
// each poll records which ltk-… containers are running.
type watcher struct {
	mu         sync.Mutex
	containers map[string]containerSeen     // by name
	labels     map[string]map[string]string // "network <name>" / "volume <name>" → labels
	running    [][]string                   // per poll, the running ltk-… containers
	errs       []string
	stop, done chan struct{}
}

// watch starts a watcher polling every interval.
func watch(interval time.Duration) *watcher {
	w := &watcher{containers: map[string]containerSeen{}, labels: map[string]map[string]string{}, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		for {
			w.poll()
			select {
			case <-w.stop:
				return
			case <-time.After(interval):
			}
		}
	}()
	return w
}

// finish stops the watcher after one last poll.
func (w *watcher) finish() {
	close(w.stop)
	<-w.done
}

// poll records one look at the engine.
func (w *watcher) poll() {
	out, err := dockerOut("ps", "-a", "--format", "{{.Names}}\t{{.State}}")
	if err != nil {
		w.note(err)
		return
	}
	var running []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, state, _ := strings.Cut(line, "\t")
		if !strings.HasPrefix(name, "ltk-") {
			continue
		}
		if state == "running" {
			running = append(running, name)
		}
		w.mu.Lock()
		_, seen := w.containers[name]
		w.mu.Unlock()
		if seen {
			continue
		}
		js, err := dockerOut("inspect", "--type", "container", name)
		if err != nil {
			continue // removed between the listing and the inspection: the next poll will not list it
		}
		var cs []containerSeen
		if err := json.Unmarshal([]byte(js), &cs); err != nil || len(cs) != 1 {
			w.note(fmt.Errorf("docker inspect %s: %v", name, err))
			continue
		}
		cs[0].Name = strings.TrimPrefix(cs[0].Name, "/")
		w.mu.Lock()
		w.containers[name] = cs[0]
		w.mu.Unlock()
	}
	sort.Strings(running)
	for _, kind := range []string{"network", "volume"} {
		out, err := dockerOut(kind, "ls", "--format", "{{.Name}}")
		if err != nil {
			w.note(err)
			continue
		}
		for _, name := range strings.Fields(out) {
			if !strings.HasPrefix(name, "ltk-") {
				continue
			}
			w.mu.Lock()
			_, seen := w.labels[kind+" "+name]
			w.mu.Unlock()
			if seen {
				continue
			}
			js, err := dockerOut(kind, "inspect", "--format", "{{json .Labels}}", name)
			if err != nil {
				continue
			}
			var labels map[string]string
			if err := json.Unmarshal([]byte(js), &labels); err != nil {
				w.note(fmt.Errorf("docker %s inspect %s: %v", kind, name, err))
				continue
			}
			w.mu.Lock()
			w.labels[kind+" "+name] = labels
			w.mu.Unlock()
		}
	}
	w.mu.Lock()
	w.running = append(w.running, running)
	w.mu.Unlock()
}

// note records an error of the watcher.
func (w *watcher) note(err error) {
	w.mu.Lock()
	w.errs = append(w.errs, err.Error())
	w.mu.Unlock()
}

// reRunName is a resource name of a run: ltk-<runID>-<role>.
var reRunName = regexp.MustCompile(`^ltk-([0-9a-f]+)-(.+)$`)

// requireOneLabelledRun holds what the watcher saw to the labelling rule: every container, network and volume named
// ltk-… belongs to one run and carries ltk.managed=true and ltk.run=<that run>; every container's image is one of the
// committed digest references, and none publishes a port anywhere but 127.0.0.1. It returns the run id and the
// containers by role; "" and none when the run ended before the watcher saw anything (a check of three entries can end
// within a second); a caller that needs a resource requires it by role.
func requireOneLabelledRun(t *testing.T, w *watcher, images map[string]string) (string, map[string]containerSeen) {
	t.Helper()
	if len(w.errs) != 0 {
		t.Errorf("the watcher's errors: %q", w.errs)
	}
	runIDs := map[string]bool{}
	byRole := map[string]containerSeen{}
	check := func(what, name string, labels map[string]string) {
		m := reRunName.FindStringSubmatch(name)
		if m == nil {
			t.Errorf("%s %s is not named ltk-<runID>-<role>", what, name)
			return
		}
		runIDs[m[1]] = true
		if labels["ltk.managed"] != "true" || labels["ltk.run"] != m[1] {
			t.Errorf("%s %s carries the labels %v; want ltk.managed=true and ltk.run=%s", what, name, labels, m[1])
		}
	}
	digests := map[string]bool{}
	for _, d := range images {
		digests[d] = true
	}
	var names []string
	for name := range w.containers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := w.containers[name]
		check("container", name, c.Config.Labels)
		if m := reRunName.FindStringSubmatch(name); m != nil {
			byRole[m[2]] = c
		}
		if !digests[c.Config.Image] {
			t.Errorf("container %s runs %q, not one of the digest references of %s", name, c.Config.Image, imagesFile)
		}
		if c.HostConfig.PublishAllPorts {
			t.Errorf("container %s publishes all its ports", name)
		}
		for _, p := range c.published() {
			if _, rest, _ := strings.Cut(p, " "); !strings.HasPrefix(rest, "127.0.0.1:") {
				t.Errorf("container %s publishes %s; every published port is bound to 127.0.0.1", name, p)
			}
		}
		t.Logf("seen: container %s image %s network %s %v published %v", name, c.Config.Image, c.HostConfig.NetworkMode, c.networks(), c.published())
	}
	var other []string
	for k := range w.labels {
		other = append(other, k)
	}
	sort.Strings(other)
	for _, k := range other {
		kind, name, _ := strings.Cut(k, " ")
		check(kind, name, w.labels[k])
		t.Logf("seen: %s %s labels %v", kind, name, w.labels[k])
	}
	switch {
	case len(runIDs) == 0:
		t.Logf("no resource of the run was seen: the run ended between two polls (the clean stop above still holds it)")
		return "", byRole
	case len(runIDs) != 1:
		t.Fatalf("the resources seen belong to %d runs, want 1: %v", len(runIDs), runIDs)
	}
	for id := range runIDs {
		return id, byRole
	}
	return "", nil
}

// requireRunNetwork holds a container to the run's own network: attached to ltk-<runID>-net and to nothing else
// (each run starts its containers on a Docker network of its own).
func requireRunNetwork(t *testing.T, runID string, c containerSeen) {
	t.Helper()
	want := "ltk-" + runID + "-net"
	if c.HostConfig.NetworkMode != want || !slices.Equal(c.networks(), []string{want}) {
		t.Errorf("container %s: network mode %q, networks %v; want only %s", c.Name, c.HostConfig.NetworkMode, c.networks(), want)
	}
}

// requireQuickstart holds a quickstart container to how ltk starts it: one of the committed quickstart digests, local
// mode with core and RPC only, on the run's own network, its one published port bound to 127.0.0.1.
func requireQuickstart(t *testing.T, runID string, c containerSeen, image string, protocol int) {
	t.Helper()
	if c.Config.Image != image {
		t.Errorf("quickstart %s runs %s, want %s", c.Name, c.Config.Image, image)
	}
	args := strings.Join(c.Config.Cmd, " ")
	if !strings.Contains(" "+args+" ", " --local ") || !strings.Contains(" "+args+" ", " --enable core,rpc ") {
		t.Errorf("quickstart %s runs with %q; want local mode with core and RPC only (--local --enable core,rpc)", c.Name, args)
	}
	if i := slices.Index(c.Config.Cmd, "--protocol-version"); i >= 0 && (i+1 >= len(c.Config.Cmd) || c.Config.Cmd[i+1] != strconv.Itoa(protocol)) {
		t.Errorf("quickstart %s runs with %q; want protocol %d", c.Name, args, protocol)
	}
	requireRunNetwork(t, runID, c)
	if p := c.published(); len(p) != 1 || !strings.HasPrefix(p[0], "8000/tcp 127.0.0.1:") {
		t.Errorf("quickstart %s publishes %v; want only 8000/tcp on 127.0.0.1", c.Name, p)
	}
}

// observedRun is a run of the program held to the clean stop, with what was seen while it ran.
type observedRun struct {
	code           int
	stdout, stderr []byte
	w              *watcher
}

// runObserved runs the program with args in dir and env between two engine snapshots, watching the engine meanwhile,
// and holds the run to the clean stop: no labelled resource before; nothing new after (the before/after difference of
// every container, running or stopped, network and volume; what was there before is not counted); the three labelled
// listings empty.
func runObserved(t *testing.T, dir string, env []string, args ...string) observedRun {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("the integration tier needs the docker CLI on PATH: %v", err)
	}
	if rows := managedListings(t); len(rows) != 0 {
		t.Fatalf("resources labelled ltk.managed=true exist before the run: %q", rows)
	}
	before := engineSnapshot(t)
	cmd := ltkCommand(t, dir, env, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Start(); err != nil {
		t.Fatalf("the program did not start: %v", err)
	}
	w := watch(time.Second)
	runErr := cmd.Wait()
	w.finish()
	code := exitCodeOfRun(t, runErr)
	after := engineSnapshot(t)
	var left []string
	for _, r := range after {
		if !slices.Contains(before, r) {
			left = append(left, r)
		}
	}
	t.Logf("$ ltk %s\nexit %d after %d polls of the engine; stderr %q\nstdout:\n%s", strings.Join(args, " "), code, len(w.running), errOut.Bytes(), out.Bytes())
	if len(left) != 0 {
		t.Errorf("the run left %q (the clean stop: the before/after difference is empty)", left)
	}
	if rows := managedListings(t); len(rows) != 0 {
		t.Errorf("the labelled listings are not empty after the run: %q", rows)
	}
	t.Logf("snapshot: %d resources before, %d after, %d new", len(before), len(after), len(left))
	return observedRun{code: code, stdout: out.Bytes(), stderr: errOut.Bytes(), w: w}
}

// requireSuccess holds a run to exit 0 with nothing on standard error: ltk writes to standard error only its one
// error line, and nothing on exit 0.
func requireSuccess(t *testing.T, r observedRun) {
	t.Helper()
	if r.code != 0 || len(r.stderr) != 0 {
		t.Fatalf("exit %d stderr %s; want 0 and nothing", r.code, r.stderr)
	}
}

// requireOnlyReport holds the working folder of a repro run to where `ltk repro` writes: the per-commit logs and the
// store dump go to a working folder outside it, so it holds repro/ and nothing else, repro/ holds <target>.json and
// nothing else, and standard output is byte-identical to that file.
func requireOnlyReport(t *testing.T, cwd, target string, stdout []byte) {
	t.Helper()
	var files []string
	err := filepath.WalkDir(cwd, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(cwd, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"repro/" + target + ".json"}; !slices.Equal(files, want) {
		t.Errorf("the working folder holds %q; want only %q (the evidence files are written outside it)", files, want)
	}
	saved, err := os.ReadFile(filepath.Join(cwd, "repro", target+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, stdout) {
		t.Errorf("standard output is not byte-identical to repro/%s.json (the report is written to both)", target)
	}
}

// wbCommitRead is a WalletBackendCommitResult as a reader decodes it.
type wbCommitRead struct {
	Commit         string          `json:"commit"`
	Role           string          `json:"role"`
	Ledger         int             `json:"ledger"`
	Ingested       bool            `json:"ingested"`
	Error          *string         `json:"error"`
	StoredRow      json.RawMessage `json:"storedRow"`
	Retries        int             `json:"retries"`
	Restarts       int             `json:"restarts"`
	LogPath        string          `json:"logPath"`
	StoreStatePath string          `json:"storeStatePath"`
}

// reStamp is Docker's timestamp at the start of a log line.
var reStamp = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?Z `)

// preFixRunShowsFailure holds the lines of one pre-fix run to how wallet-backend logs five failed attempts: attempts
// 1 to 4 logged as "Error ingesting data for ledger L (attempt k/5)", the fifth reported in the exit line "processing
// ledger L: ingesting processed data failed after 5 attempts"; and its start from a ledger before L ("Starting
// ingestion from ledger: N", N < L, for the first run only: it was running before the transaction was sent).
func preFixRunShowsFailure(t *testing.T, run string, lines []string, ledger int, first bool) {
	t.Helper()
	text := strings.Join(lines, "\n")
	for k := 1; k <= 4; k++ {
		if s := fmt.Sprintf("Error ingesting data for ledger %d (attempt %d/5)", ledger, k); !strings.Contains(text, s) {
			t.Errorf("the pre-fix commit's %s run does not log %q", run, s)
		}
	}
	if s := fmt.Sprintf("processing ledger %d: ingesting processed data failed after 5 attempts", ledger); !strings.Contains(text, s) {
		t.Errorf("the pre-fix commit's %s run does not log %q", run, s)
	}
	if first {
		requireStartedBefore(t, "the pre-fix commit's first run", text, ledger)
	}
}

// reStartingFrom is wallet-backend's line naming the ledger an ingester starts from.
var reStartingFrom = regexp.MustCompile(`Starting ingestion from ledger: (\d+)`)

// requireStartedBefore requires the log to show the ingester starting from a ledger before L: it was running before
// entry 1's transaction was sent, since a fresh ingester starts from the latest checkpoint.
func requireStartedBefore(t *testing.T, who, text string, ledger int) {
	t.Helper()
	m := reStartingFrom.FindStringSubmatch(text)
	if m == nil {
		t.Errorf("%s: its log names no starting ledger", who)
		return
	}
	if n, _ := strconv.Atoi(m[1]); n >= ledger {
		t.Errorf("%s started from ledger %d, not before ledger %d", who, n, ledger)
	} else {
		t.Logf("%s started from ledger %d, before ledger %d", who, n, ledger)
	}
}

// excerpt is the lines of text that mention any of the needles, for the saved output.
func excerpt(text string, needles ...string) string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		for _, n := range needles {
			if strings.Contains(l, n) {
				if len(l) > 260 {
					l = l[:260] + "…"
				}
				out = append(out, "    "+l)
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

// TestPhase3IntegrationReproWalletBackend: `ltk repro wallet-backend-729` from a temporary working folder, with the
// system's temporary directory a folder of this test.
func TestPhase3IntegrationReproWalletBackend(t *testing.T) {
	images := committedImages(t)
	cwd, tmp := t.TempDir(), t.TempDir()
	r := runObserved(t, cwd, integrationEnv("TMPDIR="+tmp), "repro", "wallet-backend-729")
	requireSuccess(t, r)

	// The report: valid against its schema, one document, two-space indented, and the only file the run leaves in the
	// working folder.
	requireValid(t, "wallet-backend-729-report.schema.json", r.stdout)
	requireOneDocument(t, r.stdout)
	requireTwoSpaceIndent(t, r.stdout)
	requireOnlyReport(t, cwd, "wallet-backend-729", r.stdout)
	var rep struct {
		Target          string         `json:"target"`
		ProtocolVersion int            `json:"protocolVersion"`
		Commits         []wbCommitRead `json:"commits"`
		Passed          bool           `json:"passed"`
	}
	if err := json.Unmarshal(r.stdout, &rep); err != nil || len(rep.Commits) != 2 {
		t.Fatalf("the report: %v", err)
	}
	pre, fix := rep.Commits[0], rep.Commits[1]
	L := pre.Ledger
	if rep.Target != "wallet-backend-729" || rep.ProtocolVersion != 28 || !rep.Passed || fix.Ledger != L || L < 1 {
		t.Fatalf("target %q protocol %d passed %v ledgers %d/%d", rep.Target, rep.ProtocolVersion, rep.Passed, L, fix.Ledger)
	}
	if pre.Commit != "2a0a84ea" || pre.Role != "before_fix" || pre.Ingested || pre.StoredRow == nil || string(pre.StoredRow) != "null" ||
		pre.Retries != 5 || pre.Restarts != 1 || pre.Error == nil {
		t.Errorf("before_fix %+v; want not ingested, storedRow null, retries 5, restarts 1", pre)
	} else if e := *pre.Error; !strings.Contains(e, fmt.Sprintf("ledger %d", L)) || !strings.Contains(strings.ToLower(e), "signer") {
		t.Errorf("before_fix error %q; want the failure storing the signer at ledger %d", e, L)
	}
	var row map[string]any
	if fix.Commit != "2c873353" || fix.Role != "fix" || !fix.Ingested || fix.Error != nil || fix.Retries != 0 || fix.Restarts != 0 ||
		json.Unmarshal(fix.StoredRow, &row) != nil || row["state_change_category"] != "SIGNER" || row["ledger_number"] != float64(L) {
		t.Errorf("fix %+v; want ingested with its SIGNER row of state_changes at ledger %d", fix, L)
	}

	// The evidence files, outside the repository's tree: under <TMPDIR>/ltk-<runID>/wallet-backend-729/<commit>/, still
	// there after the run.
	runID, byRole := requireOneLabelledRun(t, r.w, images)
	work := filepath.Join(tmp, "ltk-"+runID, "wallet-backend-729")
	for _, c := range rep.Commits {
		for _, p := range []string{c.LogPath, c.StoreStatePath} {
			if filepath.Dir(p) != filepath.Join(work, c.Commit) {
				t.Errorf("%s: %s is not in %s", c.Commit, p, filepath.Join(work, c.Commit))
			}
			if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
				t.Errorf("%s: %s is not a saved file: %v", c.Commit, p, err)
			}
		}
	}
	if err := filepath.WalkDir(tmp, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(tmp, p)
			t.Logf("the system's temporary directory holds %s", filepath.ToSlash(rel))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// The pre-fix log: both runs in one file, a marker line between them, each run failing five times on L and
	// exiting; the second run is a `docker start` of the same container.
	b, err := os.ReadFile(pre.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	var markers []int
	for i, l := range lines {
		if !reStamp.MatchString(l) {
			markers = append(markers, i)
		}
	}
	if len(markers) != 1 {
		t.Fatalf("the pre-fix log holds %d lines that are not wallet-backend's own (want one marker between the two runs): %q", len(markers), markers)
	}
	t.Logf("the pre-fix log's marker line: %s", lines[markers[0]])
	preFixRunShowsFailure(t, "first", lines[:markers[0]], L, true)
	preFixRunShowsFailure(t, "second", lines[markers[0]+1:], L, false)
	t.Logf("the pre-fix log %s, %d bytes; its lines about ledger %d, its starts and the marker:\n%s", pre.LogPath, len(b), L,
		excerpt(string(b), fmt.Sprintf("ledger %d", L), "Starting ingestion", lines[markers[0]]))

	// The fix log and both stores.
	b, err = os.ReadFile(fix.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	requireStartedBefore(t, "the fix commit", string(b), L)
	if s := fmt.Sprintf("Ingested ledger %d ", L); !strings.Contains(string(b), s) {
		t.Errorf("the fix log does not show %q", s)
	}
	t.Logf("the fix log %s, %d bytes; its lines about ledger %d and its start:\n%s", fix.LogPath, len(b), L,
		excerpt(string(b), fmt.Sprintf("ledger %d", L), "Starting ingestion"))
	signerAt := func(text string) bool {
		for _, l := range strings.Split(text, "\n") {
			if strings.Contains(l, `"state_change_category":"SIGNER"`) && strings.Contains(l, fmt.Sprintf(`"ledger_number":%d,`, L)) {
				return true
			}
		}
		return false
	}
	for _, c := range rep.Commits {
		s, err := os.ReadFile(c.StoreStatePath)
		if err != nil {
			t.Fatal(err)
		}
		if got := signerAt(string(s)); got != c.Ingested {
			t.Errorf("%s: its store state holds a SIGNER row at ledger %d: %v; the report says ingested %v", c.Commit, L, got, c.Ingested)
		}
		t.Logf("%s (%s) store state %s:\n%s", c.Commit, c.Role, c.StoreStatePath, excerpt(string(s), "--", "|", "SIGNER"))
	}

	// Seen from outside while it ran: the build container's pinned image, Go environment and script; each ingester's
	// RPC and its own database; two TimescaleDB containers, one per commit, each with a fresh database.
	build, ok := byRole["wb-build"]
	if !ok {
		t.Fatalf("the build container ltk-%s-wb-build was not seen", runID)
	}
	if build.Config.Image != images["GoBuildImage"] {
		t.Errorf("the build runs %s, want the pinned Go image %s", build.Config.Image, images["GoBuildImage"])
	}
	for _, kv := range []string{"GOSUMDB=sum.golang.org", "GOPROXY=https://proxy.golang.org", "GOFLAGS=-mod=readonly", "GONOSUMDB=", "GOPRIVATE=", "GOINSECURE="} {
		if !slices.Contains(build.Config.Env, kv) {
			t.Errorf("the build container's environment lacks %s (a verified, read-only module download): %q", kv, build.Config.Env)
		}
	}
	script := strings.Join(build.Config.Cmd, " ")
	for _, s := range []string{"go mod download", "go mod verify", "2a0a84ea", "2c873353", "CGO_ENABLED=0 go build"} {
		if !strings.Contains(script, s) {
			t.Errorf("the build script lacks %q (both commits built from verified modules)", s)
		}
	}
	if p := build.published(); len(p) != 0 {
		t.Errorf("the build container publishes %v", p)
	}
	t.Logf("the build container's Go environment: %q", slices.DeleteFunc(slices.Clone(build.Config.Env), func(kv string) bool {
		return !strings.HasPrefix(kv, "GO")
	}))

	core, ok := byRole["core"]
	if !ok {
		t.Fatalf("the quickstart ltk-%s-core was not seen", runID)
	}
	requireQuickstart(t, runID, core, images["QuickstartProtocol28"], 28)
	wantRPC := "http://ltk-" + runID + "-core:8000/rpc"
	for _, c := range []string{"2a0a84ea", "2c873353"} {
		db, ok := byRole["db-"+c]
		if !ok {
			t.Errorf("the database ltk-%s-db-%s was not seen", runID, c)
			continue
		}
		if db.Config.Image != images["TimescaleDBImage"] {
			t.Errorf("%s runs %s, want TimescaleDB %s", db.Name, db.Config.Image, images["TimescaleDBImage"])
		}
		if p := db.published(); len(p) != 0 {
			t.Errorf("%s publishes %v; a database publishes no port", db.Name, p)
		}
		requireRunNetwork(t, runID, db)
		ing, ok := byRole["wb-"+c]
		if !ok {
			t.Errorf("the ingester ltk-%s-wb-%s was not seen", runID, c)
			continue
		}
		requireRunNetwork(t, runID, ing)
		if p := ing.published(); len(p) != 0 {
			t.Errorf("%s publishes %v", ing.Name, p)
		}
		for k, want := range map[string]string{"RPC_URL": wantRPC, "LEDGER_BACKEND_TYPE": "rpc", "NETWORK": "standalone"} {
			if v, _ := ing.env(k); v != want {
				t.Errorf("%s: %s=%q, want %q", ing.Name, k, v, want)
			}
		}
		dbURL, _ := ing.env("DATABASE_URL")
		if !strings.Contains(dbURL, "@ltk-"+runID+"-db-"+c+":") {
			t.Errorf("%s: DATABASE_URL=%q; want its own database ltk-%s-db-%s", ing.Name, dbURL, runID, c)
		}
		t.Logf("%s: RPC_URL=%s DATABASE_URL=%s; its database %s, no published port", ing.Name, wantRPC, dbURL, db.Name)
	}
	both, dbs := false, 0
	for _, running := range r.w.running {
		n := 0
		for _, name := range running {
			if strings.HasPrefix(name, "ltk-"+runID+"-db-") {
				n++
			}
		}
		dbs = max(dbs, n)
		both = both || (slices.Contains(running, "ltk-"+runID+"-wb-2a0a84ea") && slices.Contains(running, "ltk-"+runID+"-wb-2c873353"))
	}
	if dbs != 2 {
		t.Errorf("at most %d TimescaleDB containers were running at once; want two, one per commit, each with its fresh database", dbs)
	}
	if !both {
		t.Errorf("the two ingesters were never seen running at the same time")
	}
	t.Logf("run %s: %d polls; two TimescaleDB containers running at once: %v; both ingesters running at once: %v", runID, len(r.w.running), dbs == 2, both)
}

// TestPhase3IntegrationReproJsSdk: `ltk repro js-sdk-1766` from a temporary working folder, held to the clean stop:
// exit 0; 17.2.0 rejected at build with exactly "Assets are not in lexicographic order"; 17.2.1 every step passed and
// roundTripIdentical true; standard output byte-equal to repro/js-sdk-1766.json, the only file written.
func TestPhase3IntegrationReproJsSdk(t *testing.T) {
	images := committedImages(t)
	cwd, tmp := t.TempDir(), t.TempDir()
	r := runObserved(t, cwd, integrationEnv("TMPDIR="+tmp), "repro", "js-sdk-1766")
	requireSuccess(t, r)
	requireValid(t, "js-sdk-1766-report.schema.json", r.stdout)
	requireOneDocument(t, r.stdout)
	requireTwoSpaceIndent(t, r.stdout)
	requireOnlyReport(t, cwd, "js-sdk-1766", r.stdout)
	type step struct {
		Step   string  `json:"step"`
		Passed bool    `json:"passed"`
		Error  *string `json:"error"`
	}
	var rep struct {
		Target   string `json:"target"`
		Versions []struct {
			Version            string `json:"version"`
			Role               string `json:"role"`
			Steps              []step `json:"steps"`
			RoundTripIdentical bool   `json:"roundTripIdentical"`
		} `json:"versions"`
		Passed bool `json:"passed"`
	}
	if err := json.Unmarshal(r.stdout, &rep); err != nil || len(rep.Versions) != 2 {
		t.Fatalf("the report: %v", err)
	}
	before, fix := rep.Versions[0], rep.Versions[1]
	const text = "Assets are not in lexicographic order"
	if before.Version != "17.2.0" || before.Role != "before_fix" || len(before.Steps) != 1 || before.Steps[0].Step != "build" ||
		before.Steps[0].Passed || before.Steps[0].Error == nil || *before.Steps[0].Error != text || before.RoundTripIdentical {
		t.Errorf("17.2.0: %+v; want rejected at build with exactly %q", before, text)
	}
	var names []string
	for _, s := range fix.Steps {
		names = append(names, s.Step)
		if !s.Passed || s.Error != nil {
			t.Errorf("17.2.1 step %+v; want passed with error null", s)
		}
	}
	if fix.Version != "17.2.1" || fix.Role != "fix" || !slices.Equal(names, []string{"build", "decode", "re_encode"}) || !fix.RoundTripIdentical {
		t.Errorf("17.2.1: %+v; want build, decode and re_encode passed and roundTripIdentical true", fix)
	}
	if rep.Target != "js-sdk-1766" || !rep.Passed {
		t.Errorf("target %q passed %v", rep.Target, rep.Passed)
	}
	runID, byRole := requireOneLabelledRun(t, r.w, images)
	if len(byRole) == 0 {
		t.Errorf("no container of the run was seen")
	}
	for role, c := range byRole {
		if c.Config.Image != images["NodeImage"] {
			t.Errorf("container ltk-%s-%s runs %s, want the pinned Node image %s", runID, role, c.Config.Image, images["NodeImage"])
		}
		if p := c.published(); len(p) != 0 {
			t.Errorf("container ltk-%s-%s publishes %v", runID, role, p)
		}
	}
	if err := filepath.WalkDir(tmp, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			t.Errorf("the run left %s in the system's temporary directory", p)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// generateReportRead is a GenerateReport as a reader decodes it.
type generateReportRead struct {
	Out    string `json:"out"`
	Shapes []struct {
		ID               string  `json:"id"`
		ProtocolVersion  int     `json:"protocolVersion"`
		ProtocolFallback bool    `json:"protocolFallback"`
		Generated        bool    `json:"generated"`
		Error            *string `json:"error"`
	} `json:"shapes"`
	Check json.RawMessage `json:"check"`
}

// requireGenerated holds a successful `ltk generate` run's report to its format: valid against its schema, out as
// given, the selected shapes in seed order, each generated at the current protocol with protocolFallback false, and the
// embedded check report run on the folder as written (without --also-go), passed, one entry per catalogue record in
// order.
func requireGenerated(t *testing.T, r observedRun, out string, ids []string, catalogueIDs []string) {
	t.Helper()
	requireSuccess(t, r)
	requireValid(t, "generate-report.schema.json", r.stdout)
	requireOneDocument(t, r.stdout)
	requireTwoSpaceIndent(t, r.stdout)
	var rep generateReportRead
	if err := json.Unmarshal(r.stdout, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Out != out {
		t.Errorf("out %q, want the folder as given %q", rep.Out, out)
	}
	cur := currentProtocol(t)
	var got []string
	for _, s := range rep.Shapes {
		got = append(got, s.ID)
		if !s.Generated || s.Error != nil || s.ProtocolFallback || s.ProtocolVersion != cur {
			t.Errorf("shape %+v; want generated at the current protocol %d with protocolFallback false", s, cur)
		}
	}
	if !slices.Equal(got, ids) {
		t.Errorf("shapes %q, want %q", got, ids)
	}
	ck := readCheckReport(t, r.check())
	var entries []string
	for _, e := range ck.Entries {
		entries = append(entries, e.ID)
		if string(e.GoSdkRoundTrip) != "null" {
			t.Errorf("%s: goSdkRoundTrip %s; generate does not run it (step 9)", e.ID, e.GoSdkRoundTrip)
		}
	}
	if ck.Folder != out || !ck.Passed || !slices.Equal(entries, catalogueIDs) {
		t.Errorf("check: folder %q passed %v entries %q; want %q, true, %q", ck.Folder, ck.Passed, entries, out, catalogueIDs)
	}
}

// check is the embedded check report of a generate run, indented as ltk check writes it.
func (r observedRun) check() []byte {
	var rep generateReportRead
	if json.Unmarshal(r.stdout, &rep) != nil || string(rep.Check) == "null" {
		return []byte("null\n")
	}
	var b bytes.Buffer
	if json.Indent(&b, rep.Check, "", "  ") != nil {
		return []byte("null\n")
	}
	b.WriteByte('\n')
	return b.Bytes()
}

// folderFiles is every file of folder with its SHA-256, by its slash path relative to folder.
func folderFiles(t *testing.T, folder string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(folder, p)
		h := sha256.Sum256(b)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(h[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// requireCorpusFolder holds a generated folder to the corpus layout as a reader reads it, with the standard library and
// the JSON Schema library only: exactly the catalogue, its schema copy (byte-equal to schemas/catalogue.schema.json)
// and, per entry, <id>/entry.xdr and <id>/entry.json; the catalogue valid against the schema, its entries ids in seed
// order; each record's sha256 and length those of its file, its protocolVersion the decoded ledger header's, its source
// naming the current quickstart's digest (never the run, so two runs on one image record the same text), its expected
// values of both groups read again from the decoded JSON; entry 2 alone carrying the network's refusal with
// CHANGE_TRUST_MALFORMED.
func requireCorpusFolder(t *testing.T, folder string, ids []string, images map[string]string) {
	t.Helper()
	files := folderFiles(t, folder)
	want := []string{"catalogue.json", "catalogue.schema.json"}
	for _, id := range ids {
		want = append(want, id+"/entry.json", id+"/entry.xdr")
	}
	var got []string
	for f := range files {
		got = append(got, f)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("%s holds %q, want %q", folder, got, want)
	}
	src, err := os.ReadFile(catalogueSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	copyB, err := os.ReadFile(filepath.Join(folder, "catalogue.schema.json"))
	if err != nil || !bytes.Equal(copyB, src) {
		t.Errorf("the schema copy differs from schemas/catalogue.schema.json (%v)", err)
	}
	catB, err := os.ReadFile(filepath.Join(folder, "catalogue.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validationError(t, compileSchemaFile(t, catalogueSchemaPath), catB); err != nil {
		t.Fatalf("the catalogue does not validate against the catalogue schema: %v", err)
	}
	var cat struct {
		Entries []struct {
			readerEntry
			NetworkRefusalEvidence *struct {
				SubmittedEnvelopeXdr string `json:"submittedEnvelopeXdr"`
				ResultCode           string `json:"resultCode"`
			} `json:"networkRefusalEvidence"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(catB, &cat); err != nil {
		t.Fatal(err)
	}
	caps := map[string]string{entry1ID: "CAP-0040", entry2ID: "CAP-0038", entry3ID: "CAP-0027"}
	var order []string
	for _, e := range cat.Entries {
		order = append(order, e.ID)
		if e.FilePath != e.ID+"/entry.xdr" || e.XdrType != "LedgerCloseMeta" || e.Synthetic || e.Cap != caps[e.ID] {
			t.Errorf("%s: filePath %q xdrType %q synthetic %v cap %q", e.ID, e.FilePath, e.XdrType, e.Synthetic, e.Cap)
		}
		raw, err := os.ReadFile(filepath.Join(folder, filepath.FromSlash(e.FilePath)))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(raw)
		if hex.EncodeToString(h[:]) != e.Sha256 || len(raw) != e.Length {
			t.Errorf("%s: the file is %d bytes, sha256 %x; the record says %d, %s", e.ID, len(raw), h, e.Length, e.Sha256)
		}
		doc := decodeJSONFile(t, filepath.Join(folder, e.ID, "entry.json"))
		if v, ok := resolvePointer(doc, "/v2/ledger_header/header/ledger_version"); !ok || fmt.Sprint(v) != strconv.Itoa(e.ProtocolVersion) {
			t.Errorf("%s: protocolVersion %d, the decoded ledger header says %v", e.ID, e.ProtocolVersion, v)
		}
		if !strings.Contains(e.Source, images["QuickstartCurrent"]) || e.ProtocolFallback {
			t.Errorf("%s: source %q protocolFallback %v; want the current quickstart's digest and no fallback", e.ID, e.Source, e.ProtocolFallback)
		}
		n := 0
		for _, group := range [][]readerValue{e.ExpectedDecodedValues.ReproducedByEveryRun, e.ExpectedDecodedValues.LedgerDependent} {
			for _, v := range group {
				n++
				got, ok := resolvePointer(doc, v.Pointer)
				if !ok || !sameJSON(got, decodeJSON(t, v.Value)) {
					t.Errorf("%s: %s records %s; the decoded JSON reads %v (%v)", e.ID, v.Pointer, v.Value, got, ok)
				}
			}
		}
		if len(e.ExpectedDecodedValues.ReproducedByEveryRun) == 0 || len(e.ExpectedDecodedValues.LedgerDependent) == 0 {
			t.Errorf("%s: a group of expected values is empty", e.ID)
		}
		ev := e.NetworkRefusalEvidence
		switch {
		case e.ID == entry2ID && (ev == nil || ev.ResultCode != "CHANGE_TRUST_MALFORMED" || ev.SubmittedEnvelopeXdr == ""):
			t.Errorf("%s: networkRefusalEvidence %+v; want the refusal CHANGE_TRUST_MALFORMED", e.ID, ev)
		case e.ID == entry2ID:
			if _, err := base64.StdEncoding.DecodeString(ev.SubmittedEnvelopeXdr); err != nil {
				t.Errorf("%s: submittedEnvelopeXdr is not Base64: %v", e.ID, err)
			}
		case ev != nil:
			t.Errorf("%s carries networkRefusalEvidence; only entry 2 does", e.ID)
		}
		t.Logf("%s: %s, protocol %d, %d bytes, sha256 %s, %d expected values read again", folder, e.ID, e.ProtocolVersion, e.Length, e.Sha256, n)
	}
	if !slices.Equal(order, ids) {
		t.Errorf("catalogue order %q, want %q", order, ids)
	}
}

// requireGenerationSeen holds what was seen during a generate run to how ltk starts its containers: the quickstart in
// local mode with core and RPC only, from the committed current digest, on the run's own labelled network, its one port
// bound to 127.0.0.1; the Rust XDR tool from the same image on no network.
func requireGenerationSeen(t *testing.T, r observedRun, images map[string]string) {
	t.Helper()
	runID, byRole := requireOneLabelledRun(t, r.w, images)
	core, ok := byRole["core"]
	if !ok {
		t.Fatalf("the quickstart ltk-%s-core was not seen", runID)
	}
	requireQuickstart(t, runID, core, images["QuickstartCurrent"], currentProtocol(t))
	if _, ok := r.w.labels["network ltk-"+runID+"-net"]; !ok {
		t.Errorf("the network ltk-%s-net was not seen", runID)
	}
	for role, c := range byRole {
		if role == "core" {
			continue
		}
		if c.Config.Image != images["QuickstartCurrent"] || c.HostConfig.NetworkMode != "none" || len(c.published()) != 0 {
			t.Errorf("container %s: image %s network %s published %v; want the Rust XDR tool of the current quickstart on no network",
				c.Name, c.Config.Image, c.HostConfig.NetworkMode, c.published())
		}
	}
}

// TestPhase3IntegrationGenerateCheckCompare: in order, each run held to the clean stop:
//  1. `ltk generate --all --out A` into a folder that does not exist;
//  2. `ltk check --also-go A`: every entry's Go SDK round trip passes over its whole file;
//  3. `ltk generate --all --out B`;
//  4. `ltk compare A B`: exit 0, every difference attributed to a ledger-dependent field;
//  5. `ltk generate --shape cap-0027-muxed-account --out B`: only that entry's two files and its catalogue record
//     change; the other files and records stay byte for byte.
func TestPhase3IntegrationGenerateCheckCompare(t *testing.T) {
	images := committedImages(t)
	base := t.TempDir()
	a, b := filepath.Join(base, "gen-a"), filepath.Join(base, "gen-b")
	env := integrationEnv("TMPDIR=" + t.TempDir())

	if !t.Run("generate --all A", func(t *testing.T) {
		r := runObserved(t, t.TempDir(), env, "generate", "--all", "--out", a)
		requireGenerated(t, r, a, seedIDs, seedIDs)
		requireCorpusFolder(t, a, seedIDs, images)
		requireGenerationSeen(t, r, images)
	}) {
		t.FailNow() // the runs below need the folder
	}

	t.Run("check --also-go A", func(t *testing.T) {
		r := runObserved(t, t.TempDir(), env, "check", "--also-go", a)
		requireSuccess(t, r)
		ck := readCheckReport(t, r.stdout)
		requireOneDocument(t, r.stdout)
		if ck.Folder != a || !ck.Passed || len(ck.Entries) != len(seedIDs) {
			t.Fatalf("folder %q passed %v entries %d", ck.Folder, ck.Passed, len(ck.Entries))
		}
		for _, e := range ck.Entries {
			var g struct {
				Check         string `json:"check"`
				Passed        bool   `json:"passed"`
				BytesCompared int    `json:"bytesCompared"`
			}
			if err := json.Unmarshal(e.GoSdkRoundTrip, &g); err != nil || g.Check != "go_sdk_round_trip" || !g.Passed ||
				g.BytesCompared != e.FileIntegrity.Length || !e.RoundTrip.Passed || e.RoundTrip.BytesCompared != e.FileIntegrity.Length {
				t.Errorf("%s: goSdkRoundTrip %s roundTrip %+v; want both passed over all %d bytes", e.ID, e.GoSdkRoundTrip, e.RoundTrip, e.FileIntegrity.Length)
			}
		}
		requireOneLabelledRun(t, r.w, images)
	})

	if !t.Run("generate --all B", func(t *testing.T) {
		r := runObserved(t, t.TempDir(), env, "generate", "--all", "--out", b)
		requireGenerated(t, r, b, seedIDs, seedIDs)
		requireCorpusFolder(t, b, seedIDs, images)
		requireGenerationSeen(t, r, images)
	}) {
		t.FailNow() // the runs below need the folder
	}

	t.Run("compare A B", func(t *testing.T) {
		r := runObserved(t, t.TempDir(), env, "compare", a, b)
		requireSuccess(t, r)
		rep, unlisted := verifyCompareReport(t, r.stdout, a, b)
		if !rep.Passed || len(unlisted) != 0 {
			t.Errorf("passed %v; differences outside the ledger-dependent list: %q", rep.Passed, unlisted)
		}
		if len(r.w.containers) != 0 {
			t.Errorf("ltk compare started containers: it is container-free")
		}
		for _, e := range rep.Entries {
			t.Logf("compare: %s, %d equal leaves, %d ledger-dependent differences", e.ID, len(e.Equal), len(e.Differ))
		}
	})

	t.Run("generate --shape into B", func(t *testing.T) {
		before := folderFiles(t, b)
		catBefore := readCatalogueRecords(t, b)
		r := runObserved(t, t.TempDir(), env, "generate", "--shape", entry3ID, "--out", b)
		requireGenerated(t, r, b, []string{entry3ID}, seedIDs)
		requireCorpusFolder(t, b, seedIDs, images)
		requireGenerationSeen(t, r, images)
		after := folderFiles(t, b)
		for f, sum := range before {
			mine := strings.HasPrefix(f, entry3ID+"/") || f == "catalogue.json"
			if !mine && after[f] != sum {
				t.Errorf("%s changed; --shape leaves every other file byte for byte", f)
			}
			t.Logf("%s: %s → %s", f, sum, after[f])
		}
		catAfter := readCatalogueRecords(t, b)
		for _, id := range []string{entry1ID, entry2ID} {
			if !bytes.Equal(catBefore[id], catAfter[id]) {
				t.Errorf("the catalogue record of %s changed; --shape replaces only %s's", id, entry3ID)
			}
		}
		if bytes.Equal(catBefore[entry3ID], catAfter[entry3ID]) {
			t.Errorf("the catalogue record of %s did not change: it was not regenerated", entry3ID)
		}
	})
}

// readCatalogueRecords is each catalogue record of folder as its compact JSON, by id.
func readCatalogueRecords(t *testing.T, folder string) map[string][]byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(folder, "catalogue.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cat struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(b, &cat); err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, raw := range cat.Entries {
		var id struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &id); err != nil {
			t.Fatal(err)
		}
		var c bytes.Buffer
		if err := json.Compact(&c, raw); err != nil {
			t.Fatal(err)
		}
		out[id.ID] = c.Bytes()
	}
	return out
}
