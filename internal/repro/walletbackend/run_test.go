package walletbackend

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// runFake runs the reproduction on f with the 15-minute deadline on a fake clock, in a fresh working folder.
func runFake(t *testing.T, f *fakeEngine, rec *fakeRecipe) (contract.WalletBackend729Report, string, error) {
	t.Helper()
	work := filepath.Join(t.TempDir(), "ltk-0123abcd", "wallet-backend-729")
	if rec == nil {
		rec = &fakeRecipe{f: f}
	}
	rep, err := run(context.Background(), f, Options{Deadline: 15 * time.Minute, WorkDir: work, Entry1: rec}, testConfig(newFakeClock()))
	return rep, work, err
}

// specOf is the container spec of role.
func specOf(t *testing.T, f *fakeEngine, role string) orchestration.ContainerSpec {
	t.Helper()
	for _, s := range f.specs {
		if s.Role == role {
			return s
		}
	}
	t.Fatalf("no container %s among %+v", role, f.specs)
	return orchestration.ContainerSpec{}
}

// The unit tier, on the deciding test's recorded logs (testdata/README.txt): the pre-fix commit fails five times on
// ledger L and exits, is restarted once with `docker start` and does the same; the fix commit stores the SIGNER row and
// carries on; the report is the contract's, both logs and both store states are saved in the working folder, the
// pre-fix log holding both runs with the marker between them.
func TestRunReproducesOnTheRecordedLogs(t *testing.T) {
	pre, fix := expectedRuns(t)
	f := newFakeEngine(t, pre, fix)
	rep, work, err := runFake(t, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Passed || Reasons(rep) != nil {
		t.Fatalf("passed %v; reasons %q", rep.Passed, Reasons(rep))
	}
	if rep.Target != "wallet-backend-729" || rep.ProtocolVersion != 28 || len(rep.Commits) != 2 {
		t.Fatalf("report %+v", rep)
	}
	p, x := rep.Commits[0], rep.Commits[1]
	wantErr := "Error ingesting data for ledger 29 (attempt 1/5): persisting ledger data for ledger 29: running atomic function in RunInTransaction: inserting processed data into db for ledger 29: batch inserting state changes: pgx CopyFrom state_changes: ERROR: COPY from stdin failed: converting signer_account_id: converting address to bytes: stellar address PDUII3XUZ7DKZRPOVBDBE2PLIWHL26EXSEMNMNPNT5VI4UQYJKGGUAAAAAOX4GUACND5JH3IRHOCPMGLCWFCBOK74MUWYUSRPYZ4ZAIFOYAAAAG2RQ has a 68-byte key payload; the account_id column stores only 32-byte account/contract keys (SQLSTATE 57014)"
	if p.Commit != "2a0a84ea" || p.Role != "before_fix" || p.Ledger != testLedger || p.Ingested || p.Error == nil || *p.Error != wantErr ||
		p.StoredRow != nil || p.Retries != 5 || p.Restarts != 1 {
		t.Fatalf("before_fix %+v (error %v)", p, deref(p.Error))
	}
	if x.Commit != "2c873353" || x.Role != "fix" || x.Ledger != testLedger || !x.Ingested || x.Error != nil || x.StoredRow == nil ||
		string(*x.StoredRow) != testRow || x.Retries != 0 || x.Restarts != 0 {
		t.Fatalf("fix %+v", x)
	}
	for _, r := range rep.Commits {
		dir := filepath.Join(work, string(r.Commit))
		if r.LogPath != filepath.Join(dir, "wallet-backend.log") || r.StoreStatePath != filepath.Join(dir, "store-state.txt") {
			t.Fatalf("%s paths %q %q", r.Commit, r.LogPath, r.StoreStatePath)
		}
		for _, path := range []string{r.LogPath, r.StoreStatePath} {
			if b, err := os.ReadFile(path); err != nil || len(b) == 0 {
				t.Fatalf("%s: %v, %d bytes", path, err, len(b))
			}
		}
	}
	// One file holds both pre-fix runs, the marker line between them.
	b, _ := os.ReadFile(p.LogPath)
	want := strings.Join(fixture(t, "prefix-2a0a84ea-run1"), "\n") + "\n" + restartMarker(commits[0]) + "\n" + strings.Join(fixture(t, "prefix-2a0a84ea-run2"), "\n") + "\n"
	if string(b) != want {
		t.Fatalf("pre-fix log file:\n%s", b)
	}
	b, _ = os.ReadFile(x.LogPath)
	if string(b) != strings.Join(fixture(t, "fixed-2c873353"), "\n")+"\n" {
		t.Fatalf("fix log file:\n%s", b)
	}
	b, _ = os.ReadFile(x.StoreStatePath)
	for _, s := range []string{"-- select key, value from ingest_store order by key", "latest_ingest_ledger|28", testRow, "-- " + signerRowSQL(testLedger)} {
		if !strings.Contains(string(b), s) {
			t.Fatalf("fix store state lacks %q:\n%s", s, b)
		}
	}
	if b, err := os.ReadFile(filepath.Join(work, "build.log")); err != nil || !strings.Contains(string(b), stepMarker+" done") {
		t.Fatalf("build log: %v %q", err, b)
	}
	// The restart is one `docker start` of the pre-fix ingester, and only it.
	var starts []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "start ") {
			starts = append(starts, c)
		}
	}
	if !slices.Equal(starts, []string{"start ltk-0123abcd-wb-2a0a84ea"}) {
		t.Fatalf("starts %q", starts)
	}
	// The report is the contract's document, the wallet-backend report.
	doc, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`"target":"wallet-backend-729","protocolVersion":28,"commits":[{"commit":"2a0a84ea","role":"before_fix","ledger":29,"ingested":false,"error":"Error ingesting`,
		`"storedRow":null,"retries":5,"restarts":1,"logPath":`, `{"commit":"2c873353","role":"fix","ledger":29,"ingested":true,"error":null,"storedRow":{"ledger_number":29`, `"passed":true}`} {
		if !strings.Contains(string(doc), s) {
			t.Fatalf("report document lacks %s:\n%s", s, doc)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// Both commits are running against their own fresh databases and reading
// ledgers from the same RPC, both stores live, before entry 1's transaction is sent; the network is the protocol-28
// quickstart; each database is its own TimescaleDB container, on the run's network, with no published port and no
// password.
func TestBothCommitsRunBeforeTheTransactionEachOnItsOwnDatabase(t *testing.T) {
	pre, fix := expectedRuns(t)
	f := newFakeEngine(t, pre, fix)
	rec := &fakeRecipe{f: f}
	if _, _, err := runFake(t, f, rec); err != nil {
		t.Fatal(err)
	}
	if !rec.ran || rec.passphrase != orchestration.StandalonePassphrase {
		t.Fatalf("recipe ran %v on %q", rec.ran, rec.passphrase)
	}
	for _, want := range []string{"quickstart " + orchestration.QuickstartProtocol28 + " 28", "run wb-2a0a84ea", "run wb-2c873353"} {
		if !slices.Contains(rec.seen, want) {
			t.Fatalf("%q had not happened when the transaction was sent: %q", want, rec.seen)
		}
	}
	for _, c := range []string{"2a0a84ea", "2c873353"} {
		if rec.seenQuery[c] <= f.liveAfter[c] {
			t.Fatalf("%s's store was not live when the transaction was sent (%d cursor queries)", c, rec.seenQuery[c])
		}
	}
	urls := map[string]bool{}
	for _, c := range commits {
		db := specOf(t, f, c.databaseRole())
		if db.Image != orchestration.TimescaleDBImage || db.Network != "ltk-0123abcd-net" || len(db.Publish) != 0 ||
			!slices.Equal(db.Env, []string{"POSTGRES_HOST_AUTH_METHOD=trust"}) || len(db.Mounts) != 0 {
			t.Fatalf("database %+v", db)
		}
		ing := specOf(t, f, c.ingesterRole())
		env := map[string]string{}
		for _, kv := range ing.Env {
			k, v, _ := strings.Cut(kv, "=")
			env[k] = v
		}
		wantEnv := map[string]string{
			"RPC_URL":              "http://ltk-0123abcd-core:8000/rpc",
			"DATABASE_URL":         "postgres://postgres@ltk-0123abcd-db-" + string(c.short) + ":5432/wallet_backend?sslmode=disable",
			"NETWORK":              "standalone",
			"NETWORK_PASSPHRASE":   "Standalone Network ; February 2017",
			"ARCHIVE_URL":          "http://ltk-0123abcd-core:8000/archive",
			"CHECKPOINT_FREQUENCY": "8",
			"LEDGER_BACKEND_TYPE":  "rpc",
			"GET_LEDGERS_LIMIT":    "200",
			"LOG_LEVEL":            "DEBUG",
			"STELLAR_ENVIRONMENT":  "ltk-repro-wallet-backend-729",
		}
		if len(env) != len(wantEnv) {
			t.Fatalf("%s env %q", c.short, ing.Env)
		}
		for k, v := range wantEnv {
			if env[k] != v {
				t.Fatalf("%s env %s=%q; want %q", c.short, k, env[k], v)
			}
		}
		urls[env["DATABASE_URL"]] = true
		bin := "/app/wallet-backend-" + string(c.short)
		if ing.Image != orchestration.GoBuildImage || ing.Network != "ltk-0123abcd-net" || ing.Entrypoint != "sh" ||
			!slices.Equal(ing.Args, []string{"-c", bin + " migrate up && exec " + bin + " ingest"}) ||
			!slices.Equal(ing.Mounts, []orchestration.Mount{{Volume: "ltk-0123abcd-wb-bin", Target: "/app", ReadOnly: true}}) || len(ing.Publish) != 0 {
			t.Fatalf("ingester %+v", ing)
		}
	}
	if len(urls) != 2 {
		t.Fatalf("the two commits share a database: %v", urls)
	}
}

// One Go container builds both commits from source, at their full hashes, with the
// checksum database's environment and `go mod download && go mod verify`, into the labelled volume; it checks that
// each commit's migrations create the two tables the runner reads.
func TestBuildRunsTheVerifiedBuildOfBothCommits(t *testing.T) {
	pre, fix := expectedRuns(t)
	f := newFakeEngine(t, pre, fix)
	if _, _, err := runFake(t, f, nil); err != nil {
		t.Fatal(err)
	}
	b := specOf(t, f, buildRole)
	if b.Image != orchestration.GoBuildImage || b.Network != "" || b.Entrypoint != "sh" || len(b.Args) != 2 || b.Args[0] != "-c" ||
		!slices.Equal(b.Mounts, []orchestration.Mount{{Volume: "ltk-0123abcd-wb-bin", Target: "/out"}}) {
		t.Fatalf("build spec %+v", b)
	}
	if !slices.Equal(b.Env, []string{"GOSUMDB=sum.golang.org", "GOPROXY=https://proxy.golang.org", "GOFLAGS=-mod=readonly", "GONOSUMDB=", "GOPRIVATE=", "GOINSECURE="}) {
		t.Fatalf("build env %q", b.Env)
	}
	script := b.Args[1]
	order := []string{
		"set -eu",
		"git clone --quiet https://github.com/stellar/wallet-backend.git /src/wallet-backend",
		"git checkout --quiet --detach 2a0a84ea12523b6c92e278c0c84b443041240095",
		"test \"$(git rev-parse HEAD)\" = 2a0a84ea12523b6c92e278c0c84b443041240095",
		"test \"$(grep -l '^CREATE TABLE ingest_store (' internal/db/migrations/*.sql)\" = internal/db/migrations/2024-05-22.0-ingest.sql",
		"test \"$(grep -l '^CREATE TABLE state_changes (' internal/db/migrations/*.sql)\" = internal/db/migrations/2025-06-10.4-statechanges.sql",
		"go mod download\n", "go mod verify\n",
		"CGO_ENABLED=0 go build -o /out/wallet-backend-2a0a84ea -ldflags \"-X main.GitCommit=2a0a84ea12523b6c92e278c0c84b443041240095\" .",
		"git checkout --quiet --detach 2c873353c1f0f3c9b145e25a7d009023ad104a1c",
		"go mod download\n", "go mod verify\n",
		"CGO_ENABLED=0 go build -o /out/wallet-backend-2c873353 -ldflags \"-X main.GitCommit=2c873353c1f0f3c9b145e25a7d009023ad104a1c\" .",
		"step done",
	}
	if !strings.Contains(script, "step() { echo \""+stepMarker+" $*\"; }") {
		t.Fatalf("the build script does not mark its steps:\n%s", script)
	}
	at := 0
	for _, s := range order {
		i := strings.Index(script[at:], s)
		if i < 0 {
			t.Fatalf("the build script lacks %q after offset %d:\n%s", s, at, script)
		}
		at += i + len(s)
	}
	// The build container is removed once it is done; the ingesters' binaries stay in the volume.
	if !slices.Contains(f.calls, "rm ltk-0123abcd-wb-build") {
		t.Fatalf("the build container was not removed: %q", f.calls)
	}
}

// A pre-fix run that keeps retrying without exiting reaches the injected
// deadline: not reproduced, no restart, its log still saved; the fix commit is judged as usual. The deadline is
// measured on the fake clock: nothing waits.
func TestPreFixRunReachingTheDeadlineIsNotReproduced(t *testing.T) {
	_, fix := expectedRuns(t)
	pre := []fakeRun{{lines: withoutPanic(fixture(t, "prefix-2a0a84ea-run1")), exitAfter: -1, ingestAfter: -1}}
	f := newFakeEngine(t, pre, fix)
	rep, _, err := runFake(t, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Passed {
		t.Fatal("passed")
	}
	p := rep.Commits[0]
	if p.Ingested || p.Retries != 4 || p.Restarts != 0 || p.Error == nil ||
		!strings.HasPrefix(*p.Error, "its first run reached the 15m0s safety deadline after 4 failed attempts on ledger 29, without exiting; it was restarted 0 times, not 1; its log: Error ingesting data for ledger 29 (attempt 1/5)") {
		t.Fatalf("before_fix %+v (error %s)", p, deref(p.Error))
	}
	want := []string{
		"commit 2a0a84ea (before_fix) showed 4 failed attempts on ledger 29 in its last run, not 5",
		"commit 2a0a84ea (before_fix) was restarted 0 times, not 1",
		"commit 2a0a84ea (before_fix): its first run reached the 15m0s safety deadline after 4 failed attempts on ledger 29, without exiting; ",
	}
	if got := Reasons(rep); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || !strings.HasPrefix(got[2], want[2]) {
		t.Fatalf("reasons %q", got)
	}
	if f.ing["2a0a84ea"].polls < int(15*time.Minute/PollInterval) {
		t.Fatalf("the run ended after %d polls, before its deadline", f.ing["2a0a84ea"].polls)
	}
	if b, err := os.ReadFile(p.LogPath); err != nil || !strings.Contains(string(b), "(attempt 4/5)") {
		t.Fatalf("pre-fix log: %v", err)
	}
	if !slices.Contains(f.calls, "stop ltk-0123abcd-wb-2a0a84ea") {
		t.Fatalf("the pre-fix ingester was not stopped at its deadline: %q", f.calls)
	}
}

// The fix commit is held to the same deadline; one that does not ingest L within it is not reproduced, and
// the reason is the contract's "did not ingest ledger L".
func TestFixCommitReachingTheDeadlineIsNotReproduced(t *testing.T) {
	pre, _ := expectedRuns(t)
	fix := []fakeRun{{lines: fixture(t, "fixed-2c873353")[:6], exitAfter: -1, ingestAfter: -1}}
	f := newFakeEngine(t, pre, fix)
	rep, _, err := runFake(t, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	x := rep.Commits[1]
	if rep.Passed || x.Ingested || x.StoredRow != nil || x.Error == nil || *x.Error != "it did not ingest ledger 29 within the 15m0s safety deadline (ingest cursor 28)" {
		t.Fatalf("fix %+v (error %s)", x, deref(x.Error))
	}
	want := []string{"commit 2c873353 (fix): it did not ingest ledger 29 within the 15m0s safety deadline (ingest cursor 28)"}
	if got := Reasons(rep); !slices.Equal(got, want) {
		t.Fatalf("reasons %q", got)
	}
	// The pre-fix commit still showed its failure, unaffected.
	if p := rep.Commits[0]; p.Retries != 5 || p.Restarts != 1 || p.Ingested {
		t.Fatalf("before_fix %+v", p)
	}
}

// Each other failing condition of the pre-fix and fix commits gives passed false and its reason.
func TestRunNotReproducedNamesTheReason(t *testing.T) {
	run1, run2 := fixture(t, "prefix-2a0a84ea-run1"), fixture(t, "prefix-2a0a84ea-run2")
	for _, tc := range []struct {
		name      string
		pre, fix  []fakeRun
		liveAfter map[string]int
		reasons   []string
	}{
		{
			name:    "the pre-fix commit ingests the signer",
			pre:     []fakeRun{{lines: run1[:7], exitAfter: -1, ingestAfter: 2}},
			reasons: []string{"commit 2a0a84ea (before_fix) ingested ledger 29", "commit 2a0a84ea (before_fix) showed 0 failed attempts on ledger 29 in its last run, not 5", "commit 2a0a84ea (before_fix) was restarted 0 times, not 1"},
		},
		{
			name: "the first run exits after three failures",
			pre: []fakeRun{
				{lines: run1[:10], exitAfter: 2, exitCode: 2, ingestAfter: -1},
				{lines: run2, exitAfter: 2, exitCode: 2, ingestAfter: -1},
			},
			reasons: []string{"commit 2a0a84ea (before_fix): its first run exited (code 2) after 3 failed attempts on ledger 29, not 5; its log: Error ingesting data for ledger 29 (attempt 1/5)"},
		},
		{
			name: "the restart's run exits without the failure",
			pre: []fakeRun{
				{lines: run1, exitAfter: 3, exitCode: 2, ingestAfter: -1},
				{lines: run2[:2], exitAfter: 1, exitCode: 1, ingestAfter: -1},
			},
			reasons: []string{"commit 2a0a84ea (before_fix) showed 0 failed attempts on ledger 29 in its last run, not 5",
				"commit 2a0a84ea (before_fix): its run after the restart exited (code 1) after 0 failed attempts on ledger 29, not 5"},
		},
		{
			name:      "the pre-fix commit was not live before the transaction",
			liveAfter: map[string]int{"2a0a84ea": -1, "2c873353": 1},
			reasons:   []string{"commit 2a0a84ea (before_fix): it was not ingesting before the transaction was sent; its log: Error ingesting data for ledger 29 (attempt 1/5)"},
		},
		{
			name:    "the fix commit exits",
			fix:     []fakeRun{{lines: fixture(t, "fixed-2c873353")[:6], exitAfter: 2, exitCode: 1, ingestAfter: -1}},
			reasons: []string{"commit 2c873353 (fix): it exited with code 1 before ingesting ledger 29 (ingest cursor 28)"},
		},
		{
			name:    "the fix commit fails on the ledger too",
			fix:     []fakeRun{{lines: run1, exitAfter: 3, exitCode: 2, ingestAfter: -1}},
			reasons: []string{"commit 2c873353 (fix): it exited with code 2 before ingesting ledger 29 (ingest cursor 28); its log: Error ingesting data for ledger 29 (attempt 1/5)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pre, fix := expectedRuns(t)
			if tc.pre != nil {
				pre = tc.pre
			}
			if tc.fix != nil {
				fix = tc.fix
			}
			f := newFakeEngine(t, pre, fix)
			if tc.liveAfter != nil {
				f.liveAfter = tc.liveAfter
			}
			rep, _, err := runFake(t, f, nil)
			if err != nil {
				t.Fatal(err)
			}
			if rep.Passed {
				t.Fatal("passed")
			}
			got := Reasons(rep)
			if len(got) != len(tc.reasons) {
				t.Fatalf("reasons %q; want %q", got, tc.reasons)
			}
			for i := range got {
				if !strings.HasPrefix(got[i], tc.reasons[i]) {
					t.Fatalf("reason %d %q; want it to start with %q", i, got[i], tc.reasons[i])
				}
			}
			for _, r := range rep.Commits {
				if r.Ingested == (r.Error != nil) || r.Ingested != (r.StoredRow != nil) || (r.Error != nil && *r.Error == "") {
					t.Fatalf("commit %s breaks the error/storedRow rule: %+v", r.Commit, r)
				}
			}
		})
	}
}

// The runner's four errors (a build failure, a database never ready, entry 1's submission failing, evidence that could
// not be kept), the interrupt and the options: each a run that produced no report, its error naming it.
func TestRunErrorsProduceNoReport(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fakeEngine, rec *fakeRecipe, opt *Options)
		want  string
		as    any
	}{
		{"go mod verify fails", func(f *fakeEngine, _ *fakeRecipe, _ *Options) {
			f.buildExit = 1
			f.buildLines = []string{"2026-11-16T10:00:00Z " + stepMarker + " verify 2a0a84ea", "2026-11-16T10:00:01Z github.com/x/y v1.0.0: dir has been modified"}
		}, "building wallet-backend failed: go mod verify failed at 2a0a84ea (exit 1); last log lines: " + stepMarker + " verify 2a0a84ea | github.com/x/y v1.0.0: dir has been modified", new(*BuildError)},
		{"a migration is missing", func(f *fakeEngine, _ *fakeRecipe, _ *Options) {
			f.buildExit = 1
			f.buildLines = []string{"2026-11-16T10:00:00Z " + stepMarker + " migrations 2c873353 state_changes"}
		}, "the migrations of 2c873353 do not create state_changes where the runner reads it", new(*BuildError)},
		{"the build stops early", func(f *fakeEngine, _ *fakeRecipe, _ *Options) {
			f.buildLines = []string{"2026-11-16T10:00:00Z " + stepMarker + " build 2c873353"}
		}, "the build ended before its last step", new(*BuildError)},
		{"a database never becomes ready", func(f *fakeEngine, _ *fakeRecipe, _ *Options) { f.dbNeverUp = true },
			"the database ltk-0123abcd-db-2a0a84ea never became ready: exit status 2", new(*DatabaseError)},
		{"entry 1's submission fails", func(_ *fakeEngine, rec *fakeRecipe, _ *Options) { rec.err = errors.New("friendbot answers HTTP 502") },
			"wallet-backend-729: submitting entry 1's transaction: friendbot answers HTTP 502", nil},
		{"the log cannot be collected", func(f *fakeEngine, _ *fakeRecipe, _ *Options) {
			f.logErr = errors.New("docker logs: no such container")
		},
			"the log of wallet-backend 2a0a84ea could not be collected: docker logs: no such container", nil},
		{"the network does not start", func(f *fakeEngine, _ *fakeRecipe, _ *Options) { f.quickErr = errors.New("quickstart was not ready") },
			"wallet-backend-729: quickstart was not ready", nil},
		{"no deadline", func(_ *fakeEngine, _ *fakeRecipe, opt *Options) { opt.Deadline = 0 }, "no deadline", nil},
		{"no working folder", func(_ *fakeEngine, _ *fakeRecipe, opt *Options) { opt.WorkDir = "" }, "no working folder", nil},
		{"another recipe", func(_ *fakeEngine, _ *fakeRecipe, opt *Options) {
			opt.Entry1, _ = recipes.ByID("cap-0027-muxed-account")
		}, "entry 1's recipe (cap-0040-signed-payload-signer) is required", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pre, fix := expectedRuns(t)
			f := newFakeEngine(t, pre, fix)
			rec := &fakeRecipe{f: f}
			opt := Options{Deadline: 15 * time.Minute, WorkDir: filepath.Join(t.TempDir(), "w"), Entry1: rec}
			tc.setup(f, rec, &opt)
			cfg := testConfig(newFakeClock())
			cfg.deadline = opt.Deadline
			rep, err := run(context.Background(), f, opt, cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v; want it to contain %q", err, tc.want)
			}
			if tc.as != nil && !errors.As(err, tc.as) {
				t.Fatalf("error %T %v is not a %T", err, err, tc.as)
			}
			if rep.Commits != nil || rep.Target != "" {
				t.Fatalf("a report came with the error: %+v", rep)
			}
		})
	}
}

// Evidence that could not be kept: the working folder cannot be written (a file stands where the commit's folder goes,
// which root cannot override, so the test holds when run as root): an error naming the path, no report.
func TestRunSavingTheEvidenceFailsIsAnError(t *testing.T) {
	pre, fix := expectedRuns(t)
	f := newFakeEngine(t, pre, fix)
	work := filepath.Join(t.TempDir(), "w")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "2a0a84ea"), []byte("a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := run(context.Background(), f, Options{Deadline: 15 * time.Minute, WorkDir: work, Entry1: &fakeRecipe{f: f}}, testConfig(newFakeClock()))
	if err == nil || !strings.Contains(err.Error(), "saving "+filepath.Join(work, "2a0a84ea", "wallet-backend.log")) {
		t.Fatalf("error %v", err)
	}
	if rep.Commits != nil {
		t.Fatalf("report %+v", rep)
	}
}

// An interrupt is a cancelled context in the error chain, with no report.
func TestRunInterruptedIsCancellation(t *testing.T) {
	pre, fix := expectedRuns(t)
	f := newFakeEngine(t, pre, fix)
	ctx, cancel := context.WithCancel(context.Background())
	rec := &cancellingRecipe{fakeRecipe: fakeRecipe{f: f}, cancel: cancel}
	rep, err := run(ctx, f, Options{Deadline: 15 * time.Minute, WorkDir: t.TempDir(), Entry1: rec}, testConfig(newFakeClock()))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v; want context.Canceled in its chain", err)
	}
	if rep.Commits != nil {
		t.Fatalf("report %+v", rep)
	}
}

// cancellingRecipe interrupts the run right after the transaction is sent, as SIGINT would while both commits are
// being observed.
type cancellingRecipe struct {
	fakeRecipe
	cancel context.CancelFunc
}

func (r *cancellingRecipe) Run(ctx context.Context, net recipes.Network) (recipes.Capture, error) {
	c, err := r.fakeRecipe.Run(ctx, net)
	r.cancel()
	return c, err
}

// Attempts 1 to 4 are logged as attempt lines and the fifth as the line wallet-backend exits with; each
// recorded run counts 5; the fix commit's log none; a failure on another ledger is not counted; the exit line alone is
// one attempt, not five.
func TestFailuresCountsTheFiveAttemptsOfARecordedRun(t *testing.T) {
	for _, tc := range []struct {
		name   string
		lines  []string
		ledger int
		want   int
	}{
		{"run 1", fixture(t, "prefix-2a0a84ea-run1"), 29, 5},
		{"run 2", fixture(t, "prefix-2a0a84ea-run2"), 29, 5},
		{"run 1 without its exit", withoutPanic(fixture(t, "prefix-2a0a84ea-run1")), 29, 4},
		{"another ledger", fixture(t, "prefix-2a0a84ea-run1"), 30, 0},
		{"the fix commit", fixture(t, "fixed-2c873353"), 29, 0},
		{"the exit line alone", []string{fixture(t, "prefix-2a0a84ea-run1")[11]}, 29, 1},
		{"an attempt line repeated", []string{
			"x Error ingesting data for ledger 7 (attempt 1/5)", "x Error ingesting data for ledger 7 (attempt 1/5)",
			"panic: processing ledger 7: ingesting processed data failed after 5 attempts: y"}, 7, 2},
	} {
		if got := failures(tc.lines, tc.ledger); got != tc.want {
			t.Errorf("%s: failures = %d; want %d", tc.name, got, tc.want)
		}
	}
	if got := ingestError(withoutPanic(fixture(t, "prefix-2a0a84ea-run2"))[2:], 29); !strings.HasPrefix(got, "Error ingesting data for ledger 29 (attempt 1/5): persisting") || strings.Contains(got, "retrying in") {
		t.Fatalf("ingestError %q", got)
	}
	panicOnly := []string{fixture(t, "prefix-2a0a84ea-run1")[11]}
	if got := ingestError(panicOnly, 29); !strings.HasPrefix(got, "executing root command: running ingest") {
		t.Fatalf("ingestError of the exit line %q", got)
	}
	if got := ingestError(fixture(t, "fixed-2c873353"), 29); got != "" {
		t.Fatalf("ingestError of the fix commit %q", got)
	}
}

// Reasons reads the report alone; a passed report has none; a report of the wrong shape names it.
func TestReasonsOfReports(t *testing.T) {
	pre, fix := expectedRuns(t)
	rep, _, err := runFake(t, newFakeEngine(t, pre, fix), nil)
	if err != nil {
		t.Fatal(err)
	}
	if Reasons(rep) != nil {
		t.Fatal("reasons for a passed report")
	}
	bad := rep
	bad.Passed = false
	bad.Commits = append([]contract.WalletBackendCommitResult(nil), rep.Commits...)
	bad.Commits[1].Ledger = 30
	if got := Reasons(bad); len(got) != 1 || got[0] != "the two commits name ledgers 29 and 30, not entry 1's one ledger" {
		t.Fatalf("reasons %q", got)
	}
	swapped := rep
	swapped.Passed = false
	swapped.ProtocolVersion = 30
	swapped.Commits = []contract.WalletBackendCommitResult{rep.Commits[1], rep.Commits[0]}
	if got := Reasons(swapped); len(got) != 3 || got[0] != "the network ran protocol 30, not 28" {
		t.Fatalf("reasons %q", got)
	}
	inconsistent := rep
	inconsistent.Passed = false
	inconsistent.Commits = append([]contract.WalletBackendCommitResult(nil), rep.Commits...)
	inconsistent.Commits[0].Error = nil
	if got := Reasons(inconsistent); len(got) != 1 || !strings.Contains(got[0], "their logs hold the evidence") {
		t.Fatalf("reasons %q", got)
	}
	if got := Reasons(contract.WalletBackend729Report{}); len(got) != 3 {
		t.Fatalf("empty report reasons %q", got)
	}
}
