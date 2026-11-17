package main

// The Container environment check, with the images each command needs. A container-dependent command runs it after
// its arguments are parsed (a usage error exits 2 first) and before any work: nothing has been created when it fails.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// EnvCheckTimeout bounds the daemon's answer to `docker version`. The value is the tool's own choice; a daemon that
// does not answer within it is "not reachable".
const EnvCheckTimeout = 20 * time.Second

// dockerMissing starts every environment error of the three conditions: what the command needs.
const dockerMissing = "Docker with Linux containers is not available"

// checkEnvironment confirms that the run runID can use Docker with Linux containers and the images it needs, and
// returns the run's Docker CLI runner. Each missing piece is an environment error (exit 3) whose
// message names it:
//
//  1. the `docker` CLI is not on PATH: "docker CLI not found on PATH";
//  2. the daemon does not answer `docker version --format {{.Server.Os}}`, or not within timeout: "Docker daemon not
//     reachable", then the first line docker wrote to standard error;
//  3. the daemon answers with anything but `linux`: "Docker is not running Linux containers";
//
// then each image of the command's set is inspected and an absent one pulled by its digest; a pull that fails names the
// image (orchestration's EnsureImages). Only `docker version`, `docker image inspect`
// and `docker pull` are called. An interrupt (ctx cancelled) is returned as it is.
func checkEnvironment(ctx context.Context, runID string, images []string, timeout time.Duration) (*orchestration.Runner, error) {
	r, err := orchestration.NewRunner(runID)
	if err != nil {
		var coded *model.CodedError
		if errors.As(err, &coded) && coded.Code == contract.ErrorCodeEnvironmentMissing {
			return nil, model.Environment("%s: %w", dockerMissing, err) // condition 1
		}
		return nil, err
	}
	vctx, cancel := context.WithTimeout(ctx, timeout)
	osName, err := r.ServerOS(vctx)
	timedOut := errors.Is(vctx.Err(), context.DeadlineExceeded)
	cancel()
	switch {
	case err != nil && ctx.Err() != nil:
		return nil, err
	case err != nil && timedOut: // condition 2, as a timeout
		return nil, model.Environment("%s: Docker daemon not reachable: `docker version` gave no answer within %s", dockerMissing, timeout)
	case err != nil: // condition 2
		return nil, model.Environment("%s: Docker daemon not reachable: %s", dockerMissing, dockerStderrLine(err))
	case osName != "linux": // condition 3
		return nil, model.Environment("%s: Docker is not running Linux containers (its daemon runs %q containers)", dockerMissing, osName)
	}
	if err := r.EnsureImages(ctx, images); err != nil {
		return nil, err
	}
	return r, nil
}

// dockerStderrLine is the first non-empty line docker wrote to standard error, or the error's text when it wrote none.
func dockerStderrLine(err error) string {
	var ce *orchestration.CommandError
	if errors.As(err, &ce) {
		for _, l := range strings.Split(ce.Stderr, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				return l
			}
		}
	}
	return fmt.Sprint(err)
}
