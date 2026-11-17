package walletbackend

// The wallet-backend reproduction runner. Its steps follow the deciding test of Nov 14, the feasibility run made before
// this tool (its script run.sh), except that each commit gets a TimescaleDB container of its own where that run used
// one container with two databases. Inside the command's withContainers scope it:
//
//  1. builds both commits into the run's labelled volume (build.go);
//  2. starts the protocol-28 quickstart, then a fresh TimescaleDB for each commit;
//  3. starts both ingesters, `migrate up && exec … ingest`, with the deciding test's environment, and waits until
//     both ingest cursors are live: both are running before the transaction is sent;
//  4. runs entry 1's recipe against this network, which gives the ledger L of its transaction;
//  5. observes both commits at once, each run under the injected deadline: the fix commit until its cursor is at L
//     or later with its SIGNER row; the pre-fix commit until it exits, then `docker start` of the same container once,
//     then until it exits again; this loop is the runner's, not the command's;
//  6. saves each commit's log (both pre-fix runs in one file, a marker line between them) and its store state to
//     <WorkDir>/<commit>/, and returns the report with the verdict (verdict.go).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate"
	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// Options are the runner's parameters, set by `ltk repro wallet-backend-729` (cmd/ltk/repro_wb.go).
type Options struct {
	// Deadline is the safety deadline of each run of a commit: COMMIT_RUN_DEADLINE_MINUTES (15 minutes), supplied by
	// cmd/ltk/timelimit.go, since this package cannot import cmd/ltk. A test injects a short one; it is never waited
	// for in a unit test.
	Deadline time.Duration
	// WorkDir is the working folder outside the repository's tree, <os.TempDir()>/ltk-<runID>/wallet-backend-729
	// (repro/ holds the reports only); each commit's log and store state are written under WorkDir/<commit>/.
	WorkDir string
	// Entry1 is entry 1's shape recipe (cap-0040-signed-payload-signer), submitted again on this network.
	Entry1 recipes.Recipe
}

// The waits of the runner besides the deadline, sized for a loaded machine; the unit tier injects them through
// config, so that no unit test waits for them.
const (
	// LiveTimeout bounds the wait for both ingesters' cursors to be live before the transaction is sent. No value is
	// prescribed; the deciding test waited up to four minutes (run.sh lines 117-121); five cover a loaded machine.
	LiveTimeout = 5 * time.Minute
	// PollInterval is the pause between two polls of a container's state and its store.
	PollInterval = 2 * time.Second
	// stopGrace is what an ingester is given to stop when the observation is over (run.sh line 159).
	stopGrace = 5 * time.Second
	// stateQueryTimeout bounds the store queries made after the deadline, for the evidence.
	stateQueryTimeout = 2 * time.Minute
)

// File names in each commit's folder of the working folder: their contents have no structured format; only their
// paths are recorded in the report.
const (
	logFileName   = "wallet-backend.log"
	storeFileName = "store-state.txt"
)

// restartMarker is the line between the pre-fix commit's two runs in its log file, which holds both runs.
func restartMarker(c commit) string {
	return fmt.Sprintf("==== ltk: wallet-backend %s was restarted here (docker start of the same container, restart %d of %d); the lines below are its run after the restart ====",
		c.short, contract.PRE_FIX_RESTART_COUNT, contract.PRE_FIX_RESTART_COUNT)
}

// engine is the part of the Docker CLI runner the reproduction uses; *orchestration.Runner satisfies it, and the unit
// tests give a fake: the verdict and the runner are tested without containers.
type engine interface {
	StartQuickstart(ctx context.Context, image string, protocol int) (*orchestration.Network, error)
	CreateVolume(ctx context.Context, role string) (string, error)
	Run(ctx context.Context, spec orchestration.ContainerSpec) (string, error)
	Wait(ctx context.Context, container string) (int, error)
	State(ctx context.Context, container string) (orchestration.ContainerState, error)
	Start(ctx context.Context, container string) error
	Stop(ctx context.Context, container string, grace time.Duration) error
	Remove(ctx context.Context, container string) error
	Exec(ctx context.Context, container string, stdin []byte, args ...string) (stdout, stderr []byte, err error)
	TimestampedLogs(ctx context.Context, container string) ([]string, error)
}

// clock is the time the runner measures its waits with; the unit tier's fake advances on sleep, so nothing waits.
type clock interface {
	now() time.Time
	sleep(ctx context.Context, d time.Duration) error
}

// wallClock is the real clock.
type wallClock struct{}

func (wallClock) now() time.Time { return time.Now() }

func (wallClock) sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// config is everything the runner waits by.
type config struct {
	deadline, live, dbReady, build, poll time.Duration
	clk                                  clock
	// recipeNetwork makes the recipes' view of the started network: RPC and friendbot clients on the host.
	recipeNetwork func(n *orchestration.Network) recipes.Network
}

// Run reproduces wallet-backend 729 on the run whose Docker CLI runner is r and returns the report: commits (2a0a84ea
// before_fix, 2c873353 fix), protocolVersion 28, and passed, true when the pre-fix commit fails five times on ledger L
// and exits, does the same after one restart, and the fix commit stores the signer row of L. `ltk repro
// wallet-backend-729` (cmd/ltk/repro_wb.go) calls it inside its withContainers scope with WalletBackendImages ensured,
// saves the report under repro/ and prints it, and exits 0 when passed, else 1 with Reasons in its ErrorLine: the
// report's schema has no field for them. A run that reaches the deadline without its expected behaviour is a report
// with passed false, never an error: the deadline is a safety limit, not the stopping rule.
//
// The error is non-nil only for a run that produced no report: an interrupt (the context's cancellation is in its
// chain); a build failure, including `go mod verify` failing (*BuildError); a database that never became ready
// (*DatabaseError); entry 1's submission failing; a log or store state that could not be collected or saved; or
// Options that are not set; or the network, a volume or a container that could not be started.
func Run(ctx context.Context, r *orchestration.Runner, opt Options) (contract.WalletBackend729Report, error) {
	return run(ctx, r, opt, runConfig(opt))
}

// runConfig is the config Run waits by: the deadline the command injects through Options, the named
// waits, the wall clock and the host's RPC and friendbot clients. The unit tier replaces only the clock and the
// recipes' network.
func runConfig(opt Options) config {
	return config{
		deadline: opt.Deadline, live: LiveTimeout, dbReady: DatabaseReadyTimeout, build: BuildTimeout, poll: PollInterval,
		clk: wallClock{},
		recipeNetwork: func(n *orchestration.Network) recipes.Network {
			return recipes.Network{RPC: generate.NewRPCClient(n.RPCURL), Friendbot: generate.NewFriendbot(n.FriendbotURL), Passphrase: n.Passphrase}
		},
	}
}

// run is Run on any engine and clock.
func run(ctx context.Context, eng engine, opt Options, cfg config) (contract.WalletBackend729Report, error) {
	var none contract.WalletBackend729Report
	switch {
	case cfg.deadline <= 0:
		return none, errors.New("wallet-backend-729: no deadline for the runs of a commit (COMMIT_RUN_DEADLINE_MINUTES)")
	case opt.WorkDir == "":
		return none, errors.New("wallet-backend-729: no working folder")
	case opt.Entry1 == nil || opt.Entry1.ID() != model.SeedSignedPayloadSigner:
		return none, fmt.Errorf("wallet-backend-729: entry 1's recipe (%s) is required", model.SeedSignedPayloadSigner)
	}
	fail := func(err error) (contract.WalletBackend729Report, error) {
		return none, fmt.Errorf("wallet-backend-729: %w", err)
	}

	// 1. The build.
	vol, err := eng.CreateVolume(ctx, binariesRole)
	if err != nil {
		return fail(err)
	}
	buildLog, err := build(ctx, eng, vol, cfg.build)
	if saveErr := saveLines(filepath.Join(opt.WorkDir, "build.log"), buildLog); saveErr != nil && err == nil {
		err = saveErr
	}
	if err != nil {
		return fail(err)
	}

	// 2. The protocol-28 network and the databases.
	n, err := eng.StartQuickstart(ctx, orchestration.QuickstartProtocol28, contract.WALLET_BACKEND_729_PROTOCOL_VERSION)
	if err != nil {
		return fail(err)
	}
	dbs := map[contract.WalletBackendCommit]string{}
	for _, c := range commits {
		if dbs[c.short], err = startDatabase(ctx, eng, n.Name, c); err != nil {
			return fail(err)
		}
	}
	for _, c := range commits {
		if err := awaitDatabase(ctx, eng, cfg.clk, dbs[c.short], cfg.dbReady, cfg.poll); err != nil {
			return fail(err)
		}
	}

	// 3. Both ingesters, live before the transaction.
	ingesters := map[contract.WalletBackendCommit]string{}
	for _, c := range commits {
		if ingesters[c.short], err = eng.Run(ctx, ingesterSpec(n, vol, dbs[c.short], c)); err != nil {
			return fail(err)
		}
	}
	baselines, err := awaitLive(ctx, eng, cfg, dbs, ingesters)
	if err != nil {
		return fail(err)
	}

	// 4. Entry 1's transaction.
	capture, err := opt.Entry1.Run(ctx, cfg.recipeNetwork(n))
	if err != nil {
		return fail(fmt.Errorf("submitting entry 1's transaction: %w", err))
	}
	ledger := int(capture.Ledger)
	if ledger < 1 {
		return fail(fmt.Errorf("submitting entry 1's transaction: its ledger is %d", ledger))
	}

	// 5. Both commits observed at once, each run under the deadline.
	var wg sync.WaitGroup
	evs := make([]CommitEvidence, len(commits))
	errs := make([]error, len(commits))
	for i, c := range commits {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ob := observer{eng: eng, cfg: cfg, c: c, ingester: ingesters[c.short], db: dbs[c.short], ledger: ledger}
			if c.role == contract.FixRoleBeforeFix {
				evs[i], errs[i] = ob.observePreFix(ctx)
			} else {
				evs[i], errs[i] = ob.observeFix(ctx)
			}
			b := baselines[c.short]
			evs[i].Live, evs[i].Baseline = b > 0, b
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return fail(err)
	}

	// 6. The store state, the saved files and the report.
	ev := Evidence{Ledger: ledger, Deadline: cfg.deadline, PreFix: evs[0], Fix: evs[1]}
	rep := contract.WalletBackend729Report{
		Target:          contract.ReproTargetWalletBackend729,
		ProtocolVersion: n.Protocol,
	}
	for i, c := range commits {
		dir := filepath.Join(opt.WorkDir, string(c.short))
		store, err := storeState(ctx, eng, c, dbs[c.short], ledger)
		if err != nil {
			return fail(err)
		}
		res := contract.WalletBackendCommitResult{
			Commit:         c.short,
			Role:           c.role,
			Ledger:         ledger,
			LogPath:        filepath.Join(dir, logFileName),
			StoreStatePath: filepath.Join(dir, storeFileName),
		}
		if err := saveLines(res.LogPath, evs[i].logLines(c)); err != nil {
			return fail(err)
		}
		if err := saveLines(res.StoreStatePath, store); err != nil {
			return fail(err)
		}
		fillResult(&res, ev, c)
		rep.Commits = append(rep.Commits, res)
	}
	rep.Passed = len(ev.reasons()) == 0
	return rep, nil
}

// ingesterSpec is the commit's ingester: the pinned Go image running the built binary from the volume, `migrate up`
// then `ingest`, with the deciding test's environment (run.sh lines 101-114), reading ledgers from the
// network's RPC and writing to its own database.
func ingesterSpec(n *orchestration.Network, vol, db string, c commit) orchestration.ContainerSpec {
	base := strings.TrimSuffix(n.InNetworkRPCURL, "/rpc")
	bin := "/app/" + c.binary()
	return orchestration.ContainerSpec{
		Role:    c.ingesterRole(),
		Image:   orchestration.GoBuildImage,
		Network: n.Name,
		Mounts:  []orchestration.Mount{{Volume: vol, Target: "/app", ReadOnly: true}},
		Env: []string{
			"RPC_URL=" + n.InNetworkRPCURL,
			"DATABASE_URL=postgres://postgres@" + db + ":5432/" + databaseName + "?sslmode=disable",
			"NETWORK=standalone",
			"NETWORK_PASSPHRASE=" + n.Passphrase,
			"ARCHIVE_URL=" + base + "/archive",
			"CHECKPOINT_FREQUENCY=8",
			"LEDGER_BACKEND_TYPE=rpc",
			"GET_LEDGERS_LIMIT=200",
			"LOG_LEVEL=DEBUG",
			"STELLAR_ENVIRONMENT=ltk-repro-wallet-backend-729",
		},
		Entrypoint: "sh",
		Args:       []string{"-c", bin + " migrate up && exec " + bin + " ingest"},
	}
}

// cursorSQL reads the ingest cursor: the ingest store's key latest_ingest_ledger.
const cursorSQL = "select value from " + IngestStoreTable + " where key = 'latest_ingest_ledger'"

// signerRowSQL reads the SIGNER row of the state changes at ledger L as JSON, through row_to_json.
func signerRowSQL(ledger int) string {
	return fmt.Sprintf("select row_to_json(s)::text from %s s where s.state_change_category = 'SIGNER' and s.ledger_number = %d order by 1 limit 1",
		StateChangesTable, ledger)
}

// cursor is the commit's ingest cursor, 0 when there is none yet (the table does not exist before the migrations, or
// the key is not written before the first ledger).
func cursor(ctx context.Context, eng engine, db string) int {
	out, err := psql(ctx, eng, db, databaseName, cursorSQL)
	if err != nil {
		return 0
	}
	v, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0
	}
	return v
}

// signerRow is the SIGNER row at ledger L, nil when there is none.
func signerRow(ctx context.Context, eng engine, db string, ledger int) json.RawMessage {
	out, err := psql(ctx, eng, db, databaseName, signerRowSQL(ledger))
	if err != nil || out == "" || !json.Valid([]byte(out)) {
		return nil
	}
	return json.RawMessage(out)
}

// awaitLive waits until both ingesters' cursors are above 0 (run.sh lines 117-121), or LiveTimeout, and returns each
// commit's cursor then: its baseline, 0 for one that never went live. An ingester that never goes live is evidence for
// the verdict (the commit was not ingesting before the transaction was sent), not an error; an interrupt is.
func awaitLive(ctx context.Context, eng engine, cfg config, dbs, ingesters map[contract.WalletBackendCommit]string) (map[contract.WalletBackendCommit]int, error) {
	start := cfg.clk.now()
	base := map[contract.WalletBackendCommit]int{}
	for {
		live := true
		for _, c := range commits {
			if base[c.short] > 0 {
				continue
			}
			if base[c.short] = cursor(ctx, eng, dbs[c.short]); base[c.short] > 0 {
				continue
			}
			if st, err := eng.State(ctx, ingesters[c.short]); err == nil && st.Exited() {
				continue // it will not go live; the verdict says so
			}
			live = false
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if live || cfg.clk.now().Sub(start) >= cfg.live {
			return base, nil
		}
		if err := cfg.clk.sleep(ctx, cfg.poll); err != nil {
			return nil, err
		}
	}
}

// observer watches one commit's ingester after the transaction was sent.
type observer struct {
	eng      engine
	cfg      config
	c        commit
	ingester string
	db       string
	ledger   int
}

// watch polls the ingester until it exits, its store holds ledger L (cursor at L or later and the SIGNER row), or
// the deadline passes, and returns what ended the run.
func (o observer) watch(ctx context.Context) (RunEvidence, error) {
	start := o.cfg.clk.now()
	for {
		if cursor(ctx, o.eng, o.db) >= o.ledger && signerRow(ctx, o.eng, o.db, o.ledger) != nil {
			return RunEvidence{Ingested: true}, nil
		}
		st, err := o.eng.State(ctx, o.ingester)
		if err != nil && ctx.Err() == nil {
			return RunEvidence{}, err
		}
		if err == nil && st.Exited() {
			return RunEvidence{Exited: true, ExitCode: st.ExitCode}, nil
		}
		if ctx.Err() != nil {
			return RunEvidence{}, ctx.Err()
		}
		if o.cfg.clk.now().Sub(start) >= o.cfg.deadline {
			return RunEvidence{DeadlineReached: true}, nil
		}
		if err := o.cfg.clk.sleep(ctx, o.cfg.poll); err != nil {
			return RunEvidence{}, err
		}
	}
}

// logs reads the ingester's whole log; a log that cannot be read is an error: the evidence could not be kept.
func (o observer) logs(ctx context.Context) ([]string, error) {
	lines, err := o.eng.TimestampedLogs(ctx, o.ingester)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("the log of wallet-backend %s could not be collected: %w", o.c.short, err)
	}
	return lines, nil
}

// observePreFix observes the pre-fix commit: its first run until it
// exits; when it exited, `docker start` of the same container once and its second run until it exits again. Each run
// has the deadline; a run that reaches it, or that ingests the ledger, ends the observation without a restart.
func (o observer) observePreFix(ctx context.Context) (CommitEvidence, error) {
	var ev CommitEvidence
	seen := 0
	for run := 0; run <= contract.PRE_FIX_RESTART_COUNT; run++ {
		if run > 0 {
			if err := o.eng.Start(ctx, o.ingester); err != nil {
				return ev, err
			}
			ev.Restarts++
		}
		re, err := o.watch(ctx)
		if err != nil {
			return ev, err
		}
		if !re.Exited {
			o.stop(ctx)
		}
		lines, err := o.logs(ctx)
		if err != nil {
			return ev, err
		}
		if len(lines) < seen {
			seen = len(lines)
		}
		re.Lines = lines[seen:]
		seen = len(lines)
		re.Failures = failures(re.Lines, o.ledger)
		ev.Runs = append(ev.Runs, re)
		if !re.Exited {
			break
		}
	}
	return o.finish(ctx, ev)
}

// observeFix observes the fix commit: one run until its store holds ledger L with the SIGNER row, it exits, or the
// deadline passes; it is then stopped.
func (o observer) observeFix(ctx context.Context) (CommitEvidence, error) {
	re, err := o.watch(ctx)
	if err != nil {
		return CommitEvidence{}, err
	}
	o.stop(ctx)
	lines, err := o.logs(ctx)
	if err != nil {
		return CommitEvidence{}, err
	}
	re.Lines = lines
	re.Failures = failures(lines, o.ledger)
	return o.finish(ctx, CommitEvidence{Runs: []RunEvidence{re}})
}

// stop stops the ingester at the end of its observation; it may already have exited.
func (o observer) stop(ctx context.Context) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stateQueryTimeout)
	defer cancel()
	_ = o.eng.Stop(sctx, o.ingester, stopGrace)
}

// finish reads the commit's store after its runs: the cursor and the SIGNER row at L.
func (o observer) finish(ctx context.Context, ev CommitEvidence) (CommitEvidence, error) {
	if ctx.Err() != nil {
		return ev, ctx.Err()
	}
	ev.Cursor = cursor(ctx, o.eng, o.db)
	ev.Row = signerRow(ctx, o.eng, o.db, o.ledger)
	return ev, nil
}

// The pre-fix commit's failure in its log, as the deciding test's logs/prefix-2a0a84ea-halts.log shows it: attempts 1
// to 4 are logged as `Error ingesting data for ledger L (attempt k/5): …, retrying in …`, and the fifth as the panic
// `… processing ledger L: ingesting processed data failed after 5 attempts: …`, after which the process exits.
var (
	attemptRE = regexp.MustCompile(`Error ingesting data for ledger (\d+) \(attempt (\d+)/(\d+)\)`)
	finalRE   = regexp.MustCompile(`processing ledger (\d+): ingesting processed data failed after (\d+) attempts`)
	retryRE   = regexp.MustCompile(`, retrying in [^ ]*\.\.\.$`)
)

// failures counts a run's failed attempts on ledger L as wallet-backend logs them: each attempt k of an `Error
// ingesting data for ledger L (attempt k/5)` line, and attempt N of the exit line `processing ledger L: ingesting
// processed data failed after N attempts`, counted once each. A run that logs attempts 1 to 4 and the exit line after 5
// attempts counts 5; the exit line alone counts 1.
func failures(lines []string, ledger int) int {
	l := strconv.Itoa(ledger)
	seen := map[int]bool{}
	for _, line := range lines {
		if m := attemptRE.FindStringSubmatch(line); m != nil && m[1] == l {
			k, _ := strconv.Atoi(m[2])
			seen[k] = true
		}
		if m := finalRE.FindStringSubmatch(line); m != nil && m[1] == l {
			k, _ := strconv.Atoi(m[2])
			seen[k] = true
		}
	}
	return len(seen)
}

// ingestError is the error wallet-backend logged for its first failed attempt on ledger L in the lines, without the
// retry notice; else its final line's text; "" when the lines hold neither.
func ingestError(lines []string, ledger int) string {
	l := strconv.Itoa(ledger)
	for _, line := range lines {
		if m := attemptRE.FindStringSubmatch(line); m != nil && m[1] == l {
			return retryRE.ReplaceAllString(logMessage(line), "")
		}
	}
	for _, line := range lines {
		if m := finalRE.FindStringSubmatch(line); m != nil && m[1] == l {
			text := stripStamp(line)
			if _, rest, ok := strings.Cut(text, "panic: "); ok {
				return rest
			}
			return text
		}
	}
	return ""
}

// logMessage is the msg="…" field of a logrus text line, unquoted; the whole line without its timestamp when it has
// none.
func logMessage(line string) string {
	text := stripStamp(line)
	i := strings.Index(text, `msg="`)
	if i < 0 {
		return text
	}
	quoted, err := strconv.QuotedPrefix(text[i+len("msg="):])
	if err != nil {
		return text
	}
	msg, err := strconv.Unquote(quoted)
	if err != nil {
		return text
	}
	return msg
}

// storeState reads the commit's store for its saved state: the ingest cursor rows, the state changes at ledger L and
// the SIGNER row among them, the store's evidence. A query that fails is part of the record, not an error: the
// state of a store that was never migrated is that it holds no table.
func storeState(ctx context.Context, eng engine, c commit, db string, ledger int) ([]string, error) {
	qctx, cancel := context.WithTimeout(ctx, stateQueryTimeout)
	defer cancel()
	out := []string{fmt.Sprintf("-- ltk repro wallet-backend-729: the store of wallet-backend %s (%s, %s), database %s of the container %s, after its runs",
		c.short, c.role, c.full, databaseName, db)}
	for _, q := range []string{
		"select key, value from " + IngestStoreTable + " order by key",
		fmt.Sprintf("select row_to_json(s)::text from %s s where s.ledger_number = %d order by 1", StateChangesTable, ledger),
		signerRowSQL(ledger),
	} {
		out = append(out, "", "-- "+q)
		res, err := psql(qctx, eng, db, databaseName, q)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			out = append(out, "-- the query failed: "+err.Error())
			continue
		}
		if res == "" {
			out = append(out, "-- (no rows)")
			continue
		}
		out = append(out, strings.Split(res, "\n")...)
	}
	return out, nil
}

// saveLines writes lines, each ending in a newline, to path whole, creating its folder; a failure is an error naming
// the path (the evidence could not be kept).
func saveLines(path string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("saving %s: %w", path, err)
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("saving %s: %w", path, err)
	}
	return nil
}
