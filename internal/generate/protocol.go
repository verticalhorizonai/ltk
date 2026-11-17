package generate

// The Protocol selector: every selected shape is generated first at the most recent stable protocol of the pinned
// current quickstart image, CurrentProtocol, on that quickstart. A shape that cannot work there is set aside; once that
// network is removed, the protocol-28 quickstart (which runs 28 natively) is started with the same run id and names,
// and the set-aside shapes are run again on it. A shape that cannot work at 28 either is not generated, with the reason
// of each attempt.
//
// "Cannot work" is these three conditions and nothing else:
//  1. a transaction of the shape is refused (sendTransaction ERROR): recipes.NotAppliedError;
//  2. its getTransaction is not a success: recipes.NotAppliedError;
//  3. its intended reproduced values are not found in the capture: an IntendedMismatchError, or, for entry 2, the
//     refusal it records not produced (recipes.NotRefusedError, or catalogue.RefusalEvidence's error).
//
// Any other failure (the friendbot, a timeout, the Rust XDR tool, a capture of another ledger) is not the protocol's
// and is not retried: the shape is not generated, and the error says why. A network that does not start fails every
// shape that was to run on it, without a fallback.

import (
	"context"
	"fmt"

	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// protocolPlan is one network of the selector: the image, the protocol asked of it, and whether it is the fallback.
type protocolPlan struct {
	image    string
	protocol int
	fallback bool
}

// protocolPlans are the selector's networks in order: the current quickstart at CurrentProtocol, then the tested
// quickstart at FallbackProtocol (28), the fallback, an image that runs protocol 28 natively.
func protocolPlans() []protocolPlan {
	return []protocolPlan{
		{image: orchestration.QuickstartCurrent, protocol: orchestration.CurrentProtocol},
		{image: orchestration.QuickstartProtocol28, protocol: orchestration.FallbackProtocol, fallback: true},
	}
}

// selectProtocols runs the selected recipes on the selector's networks, one network at a time, and returns each
// shape's outcome in the order of selected. Each network is removed before the next starts and before it returns.
// The error is an interrupt, or a network that could not be removed.
func (p pipeline) selectProtocols(ctx context.Context, selected []recipes.Recipe) ([]shape, error) {
	shapes := make([]shape, len(selected))
	pending := make([]int, len(selected))
	for i, rec := range selected {
		shapes[i] = shape{recipe: rec}
		pending[i] = i
	}
	for k, plan := range p.plans {
		if len(pending) == 0 {
			break
		}
		last := k == len(p.plans)-1
		n, err := p.launch.StartQuickstart(ctx, plan.image, plan.protocol)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("starting the protocol-%d network: %w", plan.protocol, ctx.Err())
			}
			for _, i := range pending {
				shapes[i].plan = plan
				shapes[i].attempts = append(shapes[i].attempts, fmt.Sprintf("at protocol %d: the network did not start: %v", plan.protocol, err))
			}
			return shapes, nil
		}
		net := p.connect(n)
		var setAside []int
		for _, i := range pending {
			s, fail, err := p.produce(ctx, selected[i], net, plan)
			if err != nil {
				return nil, err
			}
			if fail == nil {
				s.attempts = shapes[i].attempts
				shapes[i] = s
				continue
			}
			shapes[i].plan = plan
			reason := fail.err.Error()
			if fail.cannotWork && !last {
				reason += fmt.Sprintf(" (it cannot work at protocol %d; retried at protocol %d)", plan.protocol, p.plans[k+1].protocol)
				setAside = append(setAside, i)
			}
			shapes[i].attempts = append(shapes[i].attempts, fmt.Sprintf("at protocol %d: %s", plan.protocol, reason))
		}
		if err := p.launch.StopQuickstart(ctx, n); err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("removing the protocol-%d network: %w", plan.protocol, ctx.Err())
			}
			return nil, fmt.Errorf("removing the protocol-%d generation network before the checks: %w", plan.protocol, err)
		}
		pending = setAside
	}
	for i := range shapes {
		if shapes[i].generated {
			shapes[i].attempts = nil
		}
	}
	return shapes, nil
}
