package walletbackend

// The unit tier's fakes, so that the runner and the verdict are tested without containers: a Docker engine that plays
// each ingester's runs from recorded logs, a clock that advances when the runner sleeps, and entry 1's recipe
// returning a fixed ledger. Nothing waits.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// testLedger is L in the unit tier, the deciding test's ledger of the recorded logs.
const testLedger = 29

// testRow is the SIGNER row the fake's store holds once a commit ingested L.
const testRow = `{"ledger_number":29,"state_change_category":"SIGNER","state_change_reason":"ADD","signer_weight_new":1}`

// fakeRun is one run of an ingester: its log, and after how many state polls it exits or holds L (-1: never).
type fakeRun struct {
	lines       []string
	exitAfter   int
	exitCode    int
	ingestAfter int
}

// fakeIngester is one commit's ingester container.
type fakeIngester struct {
	runs     []fakeRun
	cur      int
	polls    int
	exited   bool
	stopped  bool
	ingested bool
	logs     []string
}

// fakeEngine plays the reproduction's containers.
type fakeEngine struct {
	mu sync.Mutex
	t  *testing.T

	buildExit  int
	buildLines []string
	quickErr   error
	dbNeverUp  bool
	logErr     error
	// liveAfter is how many cursor queries of a commit's store answer no cursor before it is live; -1: never.
	liveAfter map[string]int
	ing       map[string]*fakeIngester

	cursorQueries map[string]int
	// submitted is set when entry 1's transaction was sent: the ingesters' runs count their polls from then.
	submitted bool
	specs     []orchestration.ContainerSpec
	calls     []string
}

// newFakeEngine is the engine of a reproduction where the build passes and both stores go live at once.
func newFakeEngine(t *testing.T, pre, fix []fakeRun) *fakeEngine {
	return &fakeEngine{
		t:             t,
		buildLines:    []string{"2026-11-16T10:00:00Z " + stepMarker + " env", "2026-11-16T10:05:00Z " + stepMarker + " done"},
		liveAfter:     map[string]int{"2a0a84ea": 1, "2c873353": 1},
		ing:           map[string]*fakeIngester{"2a0a84ea": {runs: pre}, "2c873353": {runs: fix}},
		cursorQueries: map[string]int{},
	}
}

// commitOf is the commit a container name or role ends with.
func commitOf(name string) string {
	for _, c := range []string{"2a0a84ea", "2c873353"} {
		if strings.HasSuffix(name, c) {
			return c
		}
	}
	return ""
}

func (f *fakeEngine) log(format string, args ...any) {
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
}

func (f *fakeEngine) StartQuickstart(ctx context.Context, image string, protocol int) (*orchestration.Network, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("quickstart %s %d", image, protocol)
	if f.quickErr != nil {
		return nil, f.quickErr
	}
	return &orchestration.Network{
		Name: "ltk-0123abcd-net", Container: "ltk-0123abcd-core", Image: image, Protocol: protocol,
		Passphrase: orchestration.StandalonePassphrase, RPCURL: "http://127.0.0.1:55555/rpc", FriendbotURL: "http://127.0.0.1:55555/friendbot",
		InNetworkRPCURL: "http://ltk-0123abcd-core:8000/rpc", InNetworkFriendbotURL: "http://ltk-0123abcd-core:8000/friendbot",
	}, nil
}

func (f *fakeEngine) CreateVolume(ctx context.Context, role string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("volume %s", role)
	return "ltk-0123abcd-" + role, nil
}

func (f *fakeEngine) Run(ctx context.Context, spec orchestration.ContainerSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f.specs = append(f.specs, spec)
	f.log("run %s", spec.Role)
	name := "ltk-0123abcd-" + spec.Role
	if strings.HasPrefix(spec.Role, "wb-") && spec.Role != buildRole {
		in := f.ing[commitOf(name)]
		in.logs = append(in.logs, in.runs[0].lines...)
	}
	return name, nil
}

func (f *fakeEngine) Wait(ctx context.Context, container string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("wait %s", container)
	return f.buildExit, ctx.Err()
}

func (f *fakeEngine) State(ctx context.Context, container string) (orchestration.ContainerState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	in := f.ing[commitOf(container)]
	if in.exited || in.stopped {
		return orchestration.ContainerState{Status: "exited", ExitCode: in.runs[in.cur].exitCode}, nil
	}
	if !f.submitted {
		return orchestration.ContainerState{Status: "running"}, nil
	}
	in.polls++
	r := in.runs[in.cur]
	if r.ingestAfter >= 0 && in.polls >= r.ingestAfter {
		in.ingested = true
	}
	if r.exitAfter >= 0 && in.polls >= r.exitAfter {
		in.exited = true
		return orchestration.ContainerState{Status: "exited", ExitCode: r.exitCode}, nil
	}
	return orchestration.ContainerState{Status: "running"}, nil
}

func (f *fakeEngine) Start(ctx context.Context, container string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("start %s", container)
	in := f.ing[commitOf(container)]
	if !in.exited || in.cur+1 >= len(in.runs) {
		f.t.Errorf("docker start of %s: exited %v, run %d of %d", container, in.exited, in.cur, len(in.runs))
		return errors.New("fake: no run to start")
	}
	in.cur++
	in.polls, in.exited = 0, false
	in.logs = append(in.logs, in.runs[in.cur].lines...)
	return nil
}

func (f *fakeEngine) Stop(ctx context.Context, container string, grace time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("stop %s", container)
	if in := f.ing[commitOf(container)]; in != nil {
		in.stopped = true
	}
	return nil
}

func (f *fakeEngine) Remove(ctx context.Context, container string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("rm %s", container)
	return nil
}

func (f *fakeEngine) TimestampedLogs(ctx context.Context, container string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasSuffix(container, buildRole) {
		return append([]string(nil), f.buildLines...), nil
	}
	if f.logErr != nil {
		return nil, f.logErr
	}
	return append([]string(nil), f.ing[commitOf(container)].logs...), nil
}

func (f *fakeEngine) Exec(ctx context.Context, container string, stdin []byte, args ...string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	c := commitOf(container)
	switch args[0] {
	case "pg_isready":
		f.log("pg_isready %s", container)
		if f.dbNeverUp {
			return nil, []byte("127.0.0.1:5432 - no response"), errors.New("exit status 2")
		}
		return []byte("127.0.0.1:5432 - accepting connections"), nil, nil
	case "psql":
	default:
		return nil, nil, fmt.Errorf("fake: exec %q", args)
	}
	sql := args[len(args)-1]
	in := f.ing[c]
	switch {
	case strings.HasPrefix(sql, "create "):
		f.log("psql %s %s", container, sql)
		return nil, nil, nil
	case sql == cursorSQL:
		f.cursorQueries[c]++
		switch {
		case in.ingested:
			return []byte(fmt.Sprint(testLedger + 3)), nil, nil
		case f.liveAfter[c] >= 0 && f.cursorQueries[c] > f.liveAfter[c]:
			return []byte(fmt.Sprint(testLedger - 1)), nil, nil
		}
		return nil, []byte(`ERROR:  relation "ingest_store" does not exist`), errors.New("exit status 1")
	case sql == signerRowSQL(testLedger):
		if in.ingested {
			return []byte(testRow + "\n"), nil, nil
		}
		return nil, nil, nil
	case strings.HasPrefix(sql, "select key, value"):
		return []byte(fmt.Sprintf("latest_ingest_ledger|%d\noldest_ingest_ledger|23", testLedger-1)), nil, nil
	}
	return nil, nil, nil
}

// fakeClock advances when the runner sleeps: the deadline is injected, never waited for.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
	return nil
}

// fakeRecipe is entry 1's recipe: it records what the engine showed when it ran, and returns ledger testLedger.
type fakeRecipe struct {
	f   *fakeEngine
	err error
	// seen is the engine's calls when the transaction was sent, and the cursor queries of each store then.
	seen       []string
	seenQuery  map[string]int
	ran        bool
	passphrase string
}

func (r *fakeRecipe) ID() string                           { return model.SeedSignedPayloadSigner }
func (r *fakeRecipe) Intended() map[string]json.RawMessage { return nil }
func (r *fakeRecipe) Run(ctx context.Context, net recipes.Network) (recipes.Capture, error) {
	r.f.mu.Lock()
	r.f.submitted = r.err == nil
	r.seen = append([]string(nil), r.f.calls...)
	r.seenQuery = map[string]int{}
	for k, v := range r.f.cursorQueries {
		r.seenQuery[k] = v
	}
	r.f.mu.Unlock()
	r.ran, r.passphrase = true, net.Passphrase
	if r.err != nil {
		return recipes.Capture{}, r.err
	}
	return recipes.Capture{EntryID: model.SeedSignedPayloadSigner, Ledger: testLedger}, nil
}

// testConfig is the unit tier's config: the 15-minute deadline and the real waits, on the fake clock.
func testConfig(clk clock) config {
	return config{
		deadline: 15 * time.Minute, live: LiveTimeout, dbReady: DatabaseReadyTimeout, build: BuildTimeout, poll: PollInterval,
		clk: clk,
		recipeNetwork: func(n *orchestration.Network) recipes.Network {
			return recipes.Network{Passphrase: n.Passphrase}
		},
	}
}

// fixture reads a recorded log of testdata/ as lines.
func fixture(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".log"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// withoutPanic is a run's lines without the line it exits with: a run that keeps retrying.
func withoutPanic(lines []string) []string {
	var out []string
	for _, l := range lines {
		if !strings.Contains(l, "panic: ") {
			out = append(out, l)
		}
	}
	return out
}

// expectedRuns are the deciding test's runs: the pre-fix commit fails five times and exits (code 2), and again after
// its restart; the fix commit ingests L and carries on.
func expectedRuns(t *testing.T) (pre, fix []fakeRun) {
	pre = []fakeRun{
		{lines: fixture(t, "prefix-2a0a84ea-run1"), exitAfter: 3, exitCode: 2, ingestAfter: -1},
		{lines: fixture(t, "prefix-2a0a84ea-run2"), exitAfter: 2, exitCode: 2, ingestAfter: -1},
	}
	fix = []fakeRun{{lines: fixture(t, "fixed-2c873353"), exitAfter: -1, ingestAfter: 2}}
	return pre, fix
}
