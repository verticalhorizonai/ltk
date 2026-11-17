package walletbackend

// The wallet-backend builder and the per-commit TimescaleDB. Both commits are built from source in one labelled
// container of the pinned Go image: `git clone`, then for each commit `git checkout` of its full hash, a check that
// its migrations create the two tables the runner reads, `go mod download && go mod verify` against the Go checksum
// database, and `CGO_ENABLED=0 go build` into the run's labelled volume at /out. Each commit then gets a fresh
// TimescaleDB container of its own on the run's network, with no published port and no password
// (`POSTGRES_HOST_AUTH_METHOD=trust`), so nothing of one commit can reach the other's store. The deciding test of
// Nov 14, the feasibility run made before this tool, built the commits the same way and used one TimescaleDB
// container with two databases.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// RepoURL is wallet-backend's public repository, cloned in the Go container: the build reaches GitHub, and the Go
// module proxy for the modules.
const RepoURL = "https://github.com/stellar/wallet-backend.git"

// The two commits under test, by their full hashes (the deciding test's run.sh, WB_PREFIX and WB_FIXED), and the
// tables the runner reads in each commit's store. The builder checks, at each commit, that its migrations create them
// in the files named here (found at both commits on 2026-11-16); a mismatch is a build error naming the commit and the
// table.
const (
	// CommitBeforeFixHash is 2a0a84ea, the parent of the fix of pull request 729 (before_fix).
	CommitBeforeFixHash = "2a0a84ea12523b6c92e278c0c84b443041240095"
	// CommitFixHash is 2c873353, "fix: handle CAP-40 signed-payload signers in ingestion (#729)" (fix).
	CommitFixHash = "2c873353c1f0f3c9b145e25a7d009023ad104a1c"

	// IngestStoreTable holds the ingest cursor, key latest_ingest_ledger; created by
	// internal/db/migrations/2024-05-22.0-ingest.sql at both commits.
	IngestStoreTable = "ingest_store"
	// StateChangesTable holds the state changes, the SIGNER row among them; created by
	// internal/db/migrations/2025-06-10.4-statechanges.sql at both commits.
	StateChangesTable = "state_changes"

	// migrationsDir is where wallet-backend keeps its migrations at both commits.
	migrationsDir = "internal/db/migrations"
	// ingestStoreMigration and stateChangesMigration are the migration files that create the two tables.
	ingestStoreMigration  = "2024-05-22.0-ingest.sql"
	stateChangesMigration = "2025-06-10.4-statechanges.sql"
)

// commit is one wallet-backend commit under test.
type commit struct {
	short contract.WalletBackendCommit
	full  string
	role  contract.FixRole
}

// commits are the two commits, in the report's order: before_fix, then fix.
var commits = [...]commit{
	{short: contract.WalletBackendCommit2a0a84ea, full: CommitBeforeFixHash, role: contract.FixRoleBeforeFix},
	{short: contract.WalletBackendCommit2c873353, full: CommitFixHash, role: contract.FixRoleFix},
}

// binary is the commit's binary in the volume: wallet-backend-<short>.
func (c commit) binary() string { return "wallet-backend-" + string(c.short) }

// ingesterRole and databaseRole name the commit's containers, ltk-<runID>-wb-<short> and ltk-<runID>-db-<short>.
func (c commit) ingesterRole() string { return "wb-" + string(c.short) }
func (c commit) databaseRole() string { return "db-" + string(c.short) }

const (
	// binariesRole names the run's volume of built binaries, ltk-<runID>-wb-bin.
	binariesRole = "wb-bin"
	// buildRole names the build container, ltk-<runID>-wb-build.
	buildRole = "wb-build"
	// BuildTimeout bounds the build of both commits: the clone, every module download and two builds. No value is
	// prescribed; an hour covers a slow network and an empty module cache with a wide margin.
	BuildTimeout = 60 * time.Minute
	// stepMarker starts the line the build script prints before each of its steps, so that a failure names its step.
	stepMarker = "ltk-build-step:"
)

// buildEnv is the Go environment of the build: modules from the public proxy, verified against the Go checksum
// database, read-only go.mod, and no module exempted from the checksum database (GONOSUMDB, GOPRIVATE and GOINSECURE
// empty).
func buildEnv() []string {
	return []string{
		"GOSUMDB=sum.golang.org",
		"GOPROXY=https://proxy.golang.org",
		"GOFLAGS=-mod=readonly",
		"GONOSUMDB=",
		"GOPRIVATE=",
		"GOINSECURE=",
	}
}

// buildScript is the build container's shell script: the clone, then for each commit its checkout (the full hash,
// confirmed), the check of its migrations, `go mod download && go mod verify`, and the build into /out (the deciding
// test's run.sh lines 81-90).
func buildScript() string {
	var b strings.Builder
	b.WriteString("set -eu\n")
	fmt.Fprintf(&b, "step() { echo \"%s $*\"; }\n", stepMarker)
	b.WriteString("step env\ngo version\ngo env GOPROXY GOSUMDB GOFLAGS GONOSUMDB GOPRIVATE GOINSECURE\n")
	fmt.Fprintf(&b, "step clone\ngit clone --quiet %s /src/wallet-backend\ncd /src/wallet-backend\n", RepoURL)
	for _, c := range commits {
		fmt.Fprintf(&b, "step checkout %s\ngit checkout --quiet --detach %s\ntest \"$(git rev-parse HEAD)\" = %s\ngit log -1 --format='%%H %%s'\n", c.short, c.full, c.full)
		for _, t := range []struct{ table, file string }{{IngestStoreTable, ingestStoreMigration}, {StateChangesTable, stateChangesMigration}} {
			fmt.Fprintf(&b, "step migrations %s %s\ntest \"$(grep -l '^CREATE TABLE %s (' %s/*.sql)\" = %s/%s\necho \"%s is created by %s/%s\"\n",
				c.short, t.table, t.table, migrationsDir, migrationsDir, t.file, t.table, migrationsDir, t.file)
		}
		fmt.Fprintf(&b, "step download %s\ngo mod download\n", c.short)
		fmt.Fprintf(&b, "step verify %s\ngo mod verify\n", c.short)
		fmt.Fprintf(&b, "step build %s\nCGO_ENABLED=0 go build -o /out/%s -ldflags \"-X main.GitCommit=%s\" .\n", c.short, c.binary(), c.full)
	}
	b.WriteString("step done\n")
	return b.String()
}

// BuildError is a build of wallet-backend that failed, `go mod verify` failing included: the step it failed at and the
// end of its log.
type BuildError struct {
	// Step is the build script's step that failed, e.g. "verify 2a0a84ea"; "" when it failed before the first.
	Step string
	// ExitCode is the build container's exit code; -1 when it did not finish.
	ExitCode int
	// Tail holds the last lines of the build's log.
	Tail string
	// Err is the underlying error, when the container itself failed.
	Err error
}

// Error names the step, in the words of the step: `go mod verify` failing is said as such.
func (e *BuildError) Error() string {
	what := "building wallet-backend failed"
	if e.Step != "" {
		words := strings.Fields(e.Step)
		switch words[0] {
		case "verify":
			what = "building wallet-backend failed: go mod verify failed at " + strings.Join(words[1:], " ")
		case "download":
			what = "building wallet-backend failed: go mod download failed at " + strings.Join(words[1:], " ")
		case "migrations":
			what = fmt.Sprintf("building wallet-backend failed: the migrations of %s do not create %s where the runner reads it", words[1], strings.Join(words[2:], " "))
		default:
			what = "building wallet-backend failed at the step " + e.Step
		}
	}
	if e.Err != nil {
		what += ": " + e.Err.Error()
	} else if e.ExitCode >= 0 {
		what += fmt.Sprintf(" (exit %d)", e.ExitCode)
	}
	if e.Tail != "" {
		what += "; last log lines: " + e.Tail
	}
	return what
}

// Unwrap returns the underlying error.
func (e *BuildError) Unwrap() error { return e.Err }

// build builds both commits from source, at their full hashes, into the volume vol and returns the build's log,
// timestamped. The container is removed when it is done. A failure is a *BuildError; an interrupt returns the
// context's error.
func build(ctx context.Context, eng engine, vol string, timeout time.Duration) ([]string, error) {
	name, err := eng.Run(ctx, orchestration.ContainerSpec{
		Role:       buildRole,
		Image:      orchestration.GoBuildImage,
		Env:        buildEnv(),
		Mounts:     []orchestration.Mount{{Volume: vol, Target: "/out"}},
		Entrypoint: "sh",
		Args:       []string{"-c", buildScript()},
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		return nil, &BuildError{ExitCode: -1, Err: err}
	}
	defer removeQuietly(ctx, eng, name)
	wctx, cancel := context.WithTimeout(ctx, timeout)
	code, waitErr := eng.Wait(wctx, name)
	cancel()
	if waitErr != nil && ctx.Err() != nil {
		return nil, waitErr
	}
	lines, logErr := eng.TimestampedLogs(context.WithoutCancel(ctx), name)
	if waitErr != nil {
		if errors.Is(wctx.Err(), context.DeadlineExceeded) {
			waitErr = fmt.Errorf("the build did not finish within %s", timeout)
		}
		return lines, &BuildError{Step: lastStep(lines), ExitCode: -1, Tail: tailLines(lines, 8), Err: waitErr}
	}
	if code != 0 {
		return lines, &BuildError{Step: lastStep(lines), ExitCode: code, Tail: tailLines(lines, 8)}
	}
	if logErr != nil {
		return nil, fmt.Errorf("the build's log could not be read: %w", logErr)
	}
	if lastStep(lines) != "done" {
		return lines, &BuildError{Step: lastStep(lines), ExitCode: code, Tail: tailLines(lines, 8), Err: errors.New("the build ended before its last step")}
	}
	return lines, nil
}

// stepRE finds the build script's step marker in a log line (after Docker's timestamp).
var stepRE = regexp.MustCompile(regexp.QuoteMeta(stepMarker) + ` (.+)$`)

// lastStep is the last step the build's log names, "" when none.
func lastStep(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if m := stepRE.FindStringSubmatch(lines[i]); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

// tailLines joins the last n lines without their timestamps, " | " between them.
func tailLines(lines []string, n int) string {
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = stripStamp(l)
	}
	return strings.Join(out, " | ")
}

// stripStamp removes Docker's timestamp from a log line.
func stripStamp(line string) string {
	if stamp, rest, ok := strings.Cut(line, " "); ok {
		if _, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
			return rest
		}
	}
	return line
}

// The database of each commit.
const (
	// databaseName is the database created in each commit's TimescaleDB, with the timescaledb extension (run.sh lines
	// 93-96).
	databaseName = "wallet_backend"
	// DatabaseReadyTimeout bounds the wait for a TimescaleDB container to accept connections on TCP and take its
	// database. No value is prescribed; the deciding test waited up to two minutes; three cover a loaded machine.
	DatabaseReadyTimeout = 3 * time.Minute
)

// startDatabase starts the commit's fresh TimescaleDB on the run's network, with no published port and trust
// authentication, as the deciding test ran it (run.sh line 61).
func startDatabase(ctx context.Context, eng engine, network string, c commit) (string, error) {
	return eng.Run(ctx, orchestration.ContainerSpec{
		Role:    c.databaseRole(),
		Image:   orchestration.TimescaleDBImage,
		Network: network,
		Env:     []string{"POSTGRES_HOST_AUTH_METHOD=trust"},
		Args:    []string{"postgres", "-c", "timescaledb.enable_chunk_skipping=on", "-c", "timescaledb.enable_sparse_index_bloom=on"},
	})
}

// DatabaseError is a TimescaleDB that never became ready.
type DatabaseError struct {
	// Container is the database container.
	Container string
	// Last is the last probe's failure.
	Last error
}

// Error names the container and why it was not ready.
func (e *DatabaseError) Error() string {
	return fmt.Sprintf("the database %s never became ready: %v", e.Container, e.Last)
}

// Unwrap returns the last probe's failure.
func (e *DatabaseError) Unwrap() error { return e.Last }

// awaitDatabase waits until the database container accepts connections on TCP (the image's first start runs a
// temporary server on its socket only, then restarts), then creates the commit's database and the timescaledb
// extension in it; a *DatabaseError after timeout.
func awaitDatabase(ctx context.Context, eng engine, clk clock, db string, timeout, poll time.Duration) error {
	start := clk.now()
	var last error
	for {
		if _, _, err := eng.Exec(ctx, db, nil, "pg_isready", "-h", "127.0.0.1", "-U", "postgres"); err != nil {
			last = err
		} else if _, err := psql(ctx, eng, db, "postgres", "create database "+databaseName); err != nil && !strings.Contains(err.Error(), "already exists") {
			last = err
		} else if _, err := psql(ctx, eng, db, databaseName, "create extension if not exists timescaledb"); err != nil {
			last = err
		} else {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if clk.now().Sub(start) >= timeout {
			return &DatabaseError{Container: db, Last: last}
		}
		if err := clk.sleep(ctx, poll); err != nil {
			return err
		}
	}
}

// psql runs one statement in the database container (`psql -At`, through `docker exec`: the tool has no database
// driver: every container is driven through the Docker command-line client) and returns its output trimmed.
func psql(ctx context.Context, eng engine, db, database, sql string) (string, error) {
	out, stderr, err := eng.Exec(ctx, db, nil, "psql", "-U", "postgres", "-h", "127.0.0.1", "-d", database, "-v", "ON_ERROR_STOP=1", "-At", "-c", sql)
	if err != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return "", fmt.Errorf("psql %q: %s", sql, msg)
		}
		return "", fmt.Errorf("psql %q: %w", sql, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// removeQuietly removes a container the run is done with, on a context the interrupt does not cancel; the reaper
// removes it if this fails.
func removeQuietly(ctx context.Context, eng engine, name string) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), orchestration.TeardownTimeout)
	defer cancel()
	_ = eng.Remove(rctx, name)
}
