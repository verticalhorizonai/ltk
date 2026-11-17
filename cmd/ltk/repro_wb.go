package main

// `ltk repro wallet-backend-729`. It is container-dependent: inside the interrupt handler's scope the wallet-backend
// reproduction runner (internal/repro/walletbackend) builds both commits, starts the protocol-28 network, both
// ingesters and their databases, submits entry 1's shape and observes both commits; this command gives it the deadline
// and the working folder, holds its report to the verdict (timelimit.go), writes the report under repro/ and prints it.
// The teardown (withRunContainers) removes every container, network and volume of the run, on any end.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
	"github.com/verticalhorizonai/ltk/internal/repro/walletbackend"
)

// walletBackendRunner is the wallet-backend reproduction the command runs: internal/repro/walletbackend's runner and
// the reasons of its verdict (a seam for tests). A test of this package replaces it in its helper process
// only.
var walletBackendRunner = struct {
	run     func(context.Context, *orchestration.Runner, walletbackend.Options) (contract.WalletBackend729Report, error)
	reasons func(contract.WalletBackend729Report) []string
}{walletbackend.Run, walletbackend.Reasons}

// reproWalletBackendCommand is `ltk repro wallet-backend-729`: no flag and no argument besides its target
// (the synopsis).
func reproWalletBackendCommand() *command {
	return &command{
		name:     "repro " + string(contract.ReproTargetWalletBackend729),
		synopsis: []string{"ltk repro wallet-backend-729"},
		setup: func(*flag.FlagSet) func(context.Context, invocation) error {
			return runReproWalletBackend
		},
	}
}

// walletBackendWorkDir is the run's working folder for the per-commit logs and store dumps:
// <the system's temporary directory>/ltk-<runID>/wallet-backend-729, outside the repository's tree; the runner writes
// each commit's files under <commit>/ in it, and the command leaves it in place for the reviewer; nothing of it is
// written under the working directory.
func walletBackendWorkDir(runID string) string {
	return filepath.Join(os.TempDir(), "ltk-"+runID, string(contract.ReproTargetWalletBackend729))
}

// walletBackendOptions are the runner's options for the run runID: the safety deadline of each commit run (the guard's
// constant), the working folder, and entry 1's recipe, whose Run submits entry 1's shape on the reproduction's
// network.
func walletBackendOptions(runID string) (walletbackend.Options, error) {
	entry1, ok := recipes.ByID(model.SeedSignedPayloadSigner)
	if !ok {
		return walletbackend.Options{}, errors.New("entry 1's shape recipe is not in the registry")
	}
	return walletbackend.Options{Deadline: commitRunDeadline, WorkDir: walletBackendWorkDir(runID), Entry1: entry1}, nil
}

// runReproWalletBackend runs the reproduction and exits by its verdict:
//   - 2 before anything: an extra argument or a flag (the router; nothing on stdout);
//   - 3, nothing on stdout, nothing written: the environment check with WalletBackendImages (the protocol-28
//     quickstart, the Go image, TimescaleDB);
//   - inside withRunContainers, the runner with walletBackendOptions; an error it returns (an interrupt, a build
//     failure including `go mod verify`, a database never ready, entry 1's submission failing, the log uncollectable)
//     ends the run with exit 1, one ErrorLine and no report, and nothing is written under repro/;
//   - the report, held to the verdict by the Pre-fix time-limit guard (guardWalletBackend: a run that reached the
//     deadline without its failure is not reproduced), written whole to repro/wallet-backend-729.json under the working
//     directory, then the same bytes on stdout; a write that fails is exit 1, one ErrorLine naming the path, and no
//     report on stdout;
//   - exit 0 when passed; otherwise exit 1 with the report and one ErrorLine carrying the reasons, joined by "; ", and
//     the saved logs' paths;
//   - exit 1 when the teardown left a container, network or volume of the run, the report already printed.
func runReproWalletBackend(ctx context.Context, inv invocation) error {
	err := withRunContainers(ctx, orchestration.WalletBackendImages, func(ctx context.Context, r *orchestration.Runner, runID string) error {
		opt, err := walletBackendOptions(runID)
		if err != nil {
			return err
		}
		rep, err := walletBackendRunner.run(ctx, r, opt)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil { // interrupted as the runner returned: no report, exit 1
			return err
		}
		reasons := guardWalletBackend(&rep, walletBackendRunner.reasons(rep))
		if err := printReproReport(inv, func() ([]byte, error) { return catalogue.WriteWalletBackendReport(reproWorkingDir, rep) }); err != nil {
			return err
		}
		if !rep.Passed {
			return model.Failure("reproduction failed: %s", joinReasons(reasons))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("ltk repro %s: %w", contract.ReproTargetWalletBackend729, err)
	}
	return nil
}
