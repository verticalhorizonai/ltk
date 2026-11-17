package main

// The Interrupt handler: every container-dependent command runs its work through withContainers, so whatever it started
// is removed on success, failure, panic and interrupt, and a teardown that leaves something is the command's failure.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// interruptSignals are the signals that interrupt a container-dependent command.
var interruptSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// withContainers runs work as a container-dependent command whose images are images:
//
//  1. The command's context is cancelled by SIGINT or SIGTERM (signal.NotifyContext). The signals stay caught until
//     withContainers returns, so further interrupts during the teardown are consumed and do not abort it.
//  2. The run id is made, then the environment check runs (EnvCheckTimeout); it creates nothing.
//  3. Every container, network and volume is recorded (the snapshot before the first create), and the teardown is
//     registered before work runs: it runs when work returns, fails, panics (the panic goes on after it) or is
//     interrupted.
//  4. The teardown is the reaper (label-driven, under its own TeardownTimeout on a context the interrupt cannot cancel),
//     then a second snapshot under the same bound; what the run left is a failure naming it.
//
// work's error is the primary error and keeps its code; a teardown failure is joined behind it. When work succeeded, a
// teardown failure makes the command fail (exit 1): a clean stop is part of its success.
func withContainers(parent context.Context, images []string, work func(context.Context, *orchestration.Runner) error) (err error) {
	return withRunContainers(parent, images, func(ctx context.Context, r *orchestration.Runner, _ string) error {
		return work(ctx, r)
	})
}

// runWork is a container-dependent command's work: it runs with the run's Docker CLI runner and the run's id, which
// names the run's resources (ltk-<runID>-…) and the wallet-backend reproduction's working folder.
type runWork func(ctx context.Context, r *orchestration.Runner, runID string) error

// withRunContainers is withContainers for work that needs the run's id (`ltk repro wallet-backend-729`'s working
// folder, <the system's temporary directory>/ltk-<runID>/wallet-backend-729).
func withRunContainers(parent context.Context, images []string, work runWork) (err error) {
	ctx, stop := signal.NotifyContext(parent, interruptSignals...)
	defer stop()
	return runWithTeardown(ctx, images, EnvCheckTimeout, work)
}

// runWithTeardown is withRunContainers after the signals are caught, with the environment check's deadline as a
// parameter (a test injects a short one).
func runWithTeardown(ctx context.Context, images []string, envTimeout time.Duration, work runWork) (err error) {
	runID, err := orchestration.NewRunID()
	if err != nil {
		return err
	}
	r, err := checkEnvironment(ctx, runID, images, envTimeout)
	if err != nil {
		return err
	}
	before, err := r.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("recording Docker's containers, networks and volumes before the run: %w", err)
	}
	defer func() {
		p := recover()
		if terr := teardown(ctx, r, runID, before); terr != nil {
			if err == nil {
				err = terr
			} else {
				err = fmt.Errorf("%w; %w", err, terr)
			}
		}
		if p != nil {
			panic(p)
		}
	}()
	return work(ctx, r, runID)
}

// teardown removes the run's resources and confirms the clean stop: the reaper, then a snapshot compared with the one
// taken before the run. The resources the run left are those that are in the second snapshot and not in the first, and
// are the run's: a container or network named ltk-<runID>-…, or a volume (an anonymous volume carries no name of the
// run). A resource another program created on the same engine meanwhile is not the run's and is not counted. Nil means
// a clean stop.
func teardown(ctx context.Context, r *orchestration.Runner, runID string, before orchestration.Snapshot) error {
	reapErr := r.Reap(ctx)
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), orchestration.TeardownTimeout)
	defer cancel()
	after, snapErr := r.Snapshot(sctx)
	if snapErr != nil {
		return model.Failure("the clean stop of run %s could not be confirmed: %w", runID, errors.Join(reapErr, snapErr))
	}
	var left []string
	for _, res := range orchestration.Leftovers(before, after) {
		if strings.Contains(res, " ltk-"+runID+"-") || strings.HasPrefix(res, "volume ") {
			left = append(left, res)
		}
	}
	switch {
	case len(left) > 0 && reapErr != nil:
		return model.Failure("the teardown of run %s left %s: %w", runID, strings.Join(left, ", "), reapErr)
	case len(left) > 0:
		return model.Failure("the teardown of run %s left %s", runID, strings.Join(left, ", "))
	case reapErr != nil:
		return model.Failure("the teardown of run %s reported a failure, though nothing of the run is left: %w", runID, reapErr)
	}
	return nil
}
