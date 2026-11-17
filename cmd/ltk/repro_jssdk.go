package main

// `ltk repro js-sdk-1766`. It is container-dependent: inside the interrupt handler's scope the js-sdk reproduction
// runner (internal/repro/jssdk) runs the one script against 17.2.0 and 17.2.1 in the pinned Node container; this
// command writes the report under repro/ and prints it.

import (
	"context"
	"flag"
	"fmt"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
	"github.com/verticalhorizonai/ltk/internal/repro/jssdk"
)

// jsSdkRunner is the js-sdk reproduction the command runs: internal/repro/jssdk's runner and the reasons of its verdict
// (a seam for tests). A test of this package replaces it in its helper process only.
var jsSdkRunner = struct {
	run     func(context.Context, *orchestration.Runner) (contract.JsSdk1766Report, error)
	reasons func(contract.JsSdk1766Report) []string
}{jssdk.Run, jssdk.Reasons}

// reproJsSdkCommand is `ltk repro js-sdk-1766`: no flag and no argument besides its target (the synopsis).
func reproJsSdkCommand() *command {
	return &command{
		name:     "repro " + string(contract.ReproTargetJsSdk1766),
		synopsis: []string{"ltk repro js-sdk-1766"},
		setup: func(*flag.FlagSet) func(context.Context, invocation) error {
			return runReproJsSdk
		},
	}
}

// runReproJsSdk runs the reproduction and exits by its verdict:
//   - 2 before anything: an extra argument or a flag (the router; nothing on stdout);
//   - 3, nothing on stdout, nothing written: the environment check with JsSdkImages (the pinned Node image);
//   - inside withContainers, the runner; an error it returns (an interrupt, a container that could not start, npm ci
//     failing, a script line missing or not JSON) ends the run with exit 1, one ErrorLine and no report, and nothing is
//     written under repro/;
//   - the report, its verdict held to the report's rule for `passed` (jsSdkVerdict), written whole to
//     repro/js-sdk-1766.json under the working directory, then the same bytes on stdout; a write that fails is exit 1,
//     one ErrorLine naming the path, and no report on stdout, as for `ltk generate`;
//   - exit 0 when passed; otherwise exit 1 with the report and one ErrorLine carrying the verdict's reasons, joined by
//     "; ";
//   - exit 1 when the teardown left a resource of the run, the report already printed.
func runReproJsSdk(ctx context.Context, inv invocation) error {
	err := withContainers(ctx, orchestration.JsSdkImages, func(ctx context.Context, r *orchestration.Runner) error {
		rep, err := jsSdkRunner.run(ctx, r)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil { // interrupted as the runner returned: no report, exit 1
			return err
		}
		reasons := jsSdkVerdict(&rep, jsSdkRunner.reasons(rep))
		if err := printReproReport(inv, func() ([]byte, error) { return catalogue.WriteJsSdkReport(reproWorkingDir, rep) }); err != nil {
			return err
		}
		if !rep.Passed {
			return model.Failure("not reproduced: %s", joinReasons(reasons))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("ltk repro %s: %w", contract.ReproTargetJsSdk1766, err)
	}
	return nil
}

// jsSdkVerdict holds the runner's verdict to the report's rule for `passed`: 17.2.0 has a step with passed false, and
// 17.2.1 has its three steps passed with roundTripIdentical true. A report the runner marked passed that breaks the
// rule is marked not passed, so the report and the exit agree. It returns the reasons for a report not passed: the
// runner's when it gave any, otherwise the rule's.
func jsSdkVerdict(rep *contract.JsSdk1766Report, runnerReasons []string) []string {
	if len(rep.Versions) != 2 {
		return runnerReasons // not the report's shape: the writer refuses it
	}
	var rule []string
	before, fix := rep.Versions[0], rep.Versions[1]
	rejected := false
	for _, s := range before.Steps {
		rejected = rejected || !s.Passed
	}
	if !rejected {
		rule = append(rule, fmt.Sprintf("%s (%s) did not reject entry 2's pool-share trustline", before.Version, before.Role))
	}
	allPassed := len(fix.Steps) == 3
	for _, s := range fix.Steps {
		allPassed = allPassed && s.Passed
	}
	switch {
	case !allPassed:
		rule = append(rule, fmt.Sprintf("%s (%s) did not pass its three steps", fix.Version, fix.Role))
	case !fix.RoundTripIdentical:
		rule = append(rule, fmt.Sprintf("%s (%s) re-encoded bytes differ from the built bytes", fix.Version, fix.Role))
	}
	switch {
	case rep.Passed && len(rule) == 0:
		return nil
	case !rep.Passed && len(runnerReasons) > 0: // the runner's verdict, with its own reasons
		return mergeReasons(runnerReasons, nil)
	}
	rep.Passed = false
	return mergeReasons(runnerReasons, rule)
}
