package jssdk

// The js-sdk reproduction verdict: one pure function of the report, unit-tested on recorded lines (testdata/),
// separate from the container work.

import (
	"fmt"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// ExpectedRejection is the message 17.2.0 throws at build for entry 2's valid pool-share trustline, as the feasibility
// run of Nov 12, made before this tool, recorded it (feasibility/out-17.2.0.txt). 17.2.0 counts as reproduced only
// when build fails with exactly this text.
const ExpectedRejection = "Assets are not in lexicographic order"

// Reasons returns why the report's verdict is not reproduced, one sentence each, for the ErrorLine of `ltk repro
// js-sdk-1766` (the report schema has no field for them); nil when rep.Passed is true.
func Reasons(rep contract.JsSdk1766Report) []string {
	if rep.Passed {
		return nil
	}
	reasons := verdict(rep)
	if len(reasons) == 0 {
		// passed false with every condition met: the report itself is inconsistent; say so rather than say nothing.
		reasons = []string{"the report says not reproduced, though 17.2.0 rejected the trustline at build and 17.2.1 round-tripped it"}
	}
	return reasons
}

// verdict returns the failed conditions of the reproduction; none means reproduced:
//   - the report is js-sdk-1766's, with 17.2.0 (before_fix) then 17.2.1 (fix);
//   - 17.2.0 failed at build with ExpectedRejection;
//   - 17.2.1 passed build, decode and re_encode, and its re-encoded bytes are the built bytes (roundTripIdentical).
func verdict(rep contract.JsSdk1766Report) []string {
	var reasons []string
	if rep.Target != contract.ReproTargetJsSdk1766 {
		reasons = append(reasons, fmt.Sprintf("the report's target is %q, not %s", rep.Target, contract.ReproTargetJsSdk1766))
	}
	if len(rep.Versions) != len(releases) {
		return append(reasons, fmt.Sprintf("the report holds %d releases, not 17.2.0 and 17.2.1", len(rep.Versions)))
	}
	for i, rel := range releases {
		if v := rep.Versions[i]; v.Version != rel.version || v.Role != rel.role {
			reasons = append(reasons, fmt.Sprintf("release %d is %s (%s), not %s (%s)", i, v.Version, v.Role, rel.version, rel.role))
		}
	}
	if len(reasons) > 0 {
		return reasons
	}
	if r := beforeFixReason(rep.Versions[0]); r != "" {
		reasons = append(reasons, r)
	}
	return append(reasons, fixReasons(rep.Versions[1])...)
}

// beforeFixReason is why 17.2.0 did not reproduce the bug, or "" when it rejected the trustline at build with
// ExpectedRejection.
func beforeFixReason(v contract.SdkVersionResult) string {
	failed := failedStep(v)
	switch {
	case failed == nil:
		return fmt.Sprintf("%s did not reject entry 2's pool-share trustline: every step it ran passed", v.Version)
	case failed.Step != contract.SdkReproStepBuild:
		return fmt.Sprintf("%s built entry 2's pool-share trustline and failed at %s with %q; the rejection is expected at build with %q",
			v.Version, failed.Step, msg(failed), ExpectedRejection)
	case msg(failed) != ExpectedRejection:
		return fmt.Sprintf("%s failed at build with %q, not %q", v.Version, msg(failed), ExpectedRejection)
	}
	return ""
}

// fixReasons is why 17.2.1 did not round-trip the trustline; none when its three steps passed and the bytes are the
// same.
func fixReasons(v contract.SdkVersionResult) []string {
	if failed := failedStep(v); failed != nil {
		return []string{fmt.Sprintf("%s failed at %s: %s", v.Version, failed.Step, msg(failed))}
	}
	var reasons []string
	if len(v.Steps) != len(stepOrder) {
		reasons = append(reasons, fmt.Sprintf("%s ran %d of the 3 steps", v.Version, len(v.Steps)))
	}
	if !v.RoundTripIdentical {
		reasons = append(reasons, fmt.Sprintf("%s re-encoded bytes differ from the built bytes", v.Version))
	}
	return reasons
}

// failedStep is the release's first failed step, or nil.
func failedStep(v contract.SdkVersionResult) *contract.SdkStepResult {
	for i := range v.Steps {
		if !v.Steps[i].Passed {
			return &v.Steps[i]
		}
	}
	return nil
}

// msg is a step's error text, "" when it has none.
func msg(s *contract.SdkStepResult) string {
	if s.Error == nil {
		return ""
	}
	return *s.Error
}
