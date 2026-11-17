package jssdk

// The committed report against the verdict, the runner's side: repro/js-sdk-1766.json, written by
// `ltk repro js-sdk-1766` when the repository's reports were generated and never by a test, is read here and nothing
// is written beside it. The process-level test of the command is cmd/ltk/accept_jssdk_test.go.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
)

// committedReportPath is the committed report, from this package's folder: the module root's repro/.
var committedReportPath = filepath.Join("..", "..", "..", "repro", "js-sdk-1766.json")

// committedReport reads the committed report's bytes and decodes them strictly; it skips, saying why, when the file
// is absent.
func committedReport(t *testing.T) ([]byte, contract.JsSdk1766Report) {
	t.Helper()
	b, err := os.ReadFile(committedReportPath)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("skipped: repro/js-sdk-1766.json is absent from the module root; it is written by `ltk repro js-sdk-1766` and committed with the repository")
	}
	if err != nil {
		t.Fatal(err)
	}
	return b, decodeReport(t, b)
}

// decodeReport decodes a report strictly (no field outside the mirror).
func decodeReport(t *testing.T, b []byte) contract.JsSdk1766Report {
	t.Helper()
	var rep contract.JsSdk1766Report
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rep); err != nil {
		t.Fatalf("the report is not the js-sdk report document: %v", err)
	}
	return rep
}

// validateReport validates doc against the report's schema, a test fixture, whose if/then holds the report's pass
// rule: passed true only with a 17.2.0 step failed and 17.2.1's three steps passed with roundTripIdentical true.
func validateReport(t *testing.T, doc []byte) error {
	t.Helper()
	const name = "js-sdk-1766-report.schema.json"
	sb, err := os.ReadFile(filepath.Join("..", "..", "contract", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	sdoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(sb))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("file:///fixtures/"+name, sdoc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("file:///fixtures/" + name)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return err
	}
	return sch.Validate(inst)
}

// TestCommittedReportIsReproduced: the committed report is the writer's own document for its value; it validates
// against the report's schema, pass rule included; its passed is true and the verdict that sets it finds no failed
// condition (Reasons nil, verdict empty); and it shows the reproduced outcome: 17.2.0 (before_fix) failed at build with
// exactly "Assets are not in lexicographic order", its one step; 17.2.1 (fix) passed build, decode and re_encode, in
// that order, with roundTripIdentical true.
func TestCommittedReportIsReproduced(t *testing.T) {
	b, rep := committedReport(t)

	written, err := catalogue.WriteJsSdkReport(t.TempDir(), rep)
	if err != nil {
		t.Fatalf("the report writer refuses the committed report: %v", err)
	}
	if !bytes.Equal(written, b) {
		t.Errorf("the committed report is not the writer's document for its value:\n%s\nwant\n%s", b, written)
	}
	if err := validateReport(t, b); err != nil {
		t.Errorf("the committed report does not validate against the report's schema: %v", err)
	}

	if !rep.Passed {
		t.Fatalf("the committed report says passed false; reasons %q", Reasons(rep))
	}
	if got := Reasons(rep); got != nil {
		t.Errorf("Reasons of the committed report: %q", got)
	}
	if got := verdict(rep); got != nil {
		t.Errorf("the committed report fails the verdict: %q", got)
	}

	if rep.Target != contract.ReproTargetJsSdk1766 || len(rep.Versions) != 2 {
		t.Fatalf("report %+v", rep)
	}
	before, fix := rep.Versions[0], rep.Versions[1]
	if before.Version != contract.SdkVersion17_2_0 || before.Role != contract.FixRoleBeforeFix || len(before.Steps) != 1 ||
		before.Steps[0].Step != contract.SdkReproStepBuild || before.Steps[0].Passed || msg(&before.Steps[0]) != ExpectedRejection ||
		before.RoundTripIdentical {
		t.Errorf("before_fix %+v", before)
	}
	if fix.Version != contract.SdkVersion17_2_1 || fix.Role != contract.FixRoleFix || len(fix.Steps) != len(stepOrder) || !fix.RoundTripIdentical {
		t.Errorf("fix %+v", fix)
	}
	for i, s := range fix.Steps {
		if i < len(stepOrder) && (s.Step != stepOrder[i] || !s.Passed || s.Error != nil) {
			t.Errorf("fix step %d %+v", i, s)
		}
	}
}

// TestCommittedReportChangedToAFailingShapeIsNamed: the committed report, changed to each failing shape of the
// verdict and judged as the runner judges it (passed is the verdict's), has passed false and is named by Reasons; with
// passed left true, the report schema's pass rule refuses each shape it can express (a shape it cannot, 17.2.0 failing
// with another message, only the verdict names). Each changed report is still one the writer accepts, so
// the command writes it and exits 1.
func TestCommittedReportChangedToAFailingShapeIsNamed(t *testing.T) {
	b, _ := committedReport(t)
	str := func(s string) *string { return &s }
	for _, tc := range []struct {
		name          string
		change        func(r *contract.JsSdk1766Report)
		schemaRejects bool
		want          string
	}{
		{
			name: "17.2.0 builds the trustline",
			change: func(r *contract.JsSdk1766Report) {
				r.Versions[0].Steps = []contract.SdkStepResult{{Step: contract.SdkReproStepBuild, Passed: true}}
			},
			schemaRejects: true,
			want:          "17.2.0 did not reject entry 2's pool-share trustline",
		},
		{
			name: "17.2.0 fails at build with another message",
			change: func(r *contract.JsSdk1766Report) {
				r.Versions[0].Steps[0].Error = str("bad asset")
			},
			want: `17.2.0 failed at build with "bad asset", not "Assets are not in lexicographic order"`,
		},
		{
			name: "17.2.1's re-encoded bytes differ",
			change: func(r *contract.JsSdk1766Report) {
				r.Versions[1].RoundTripIdentical = false
			},
			schemaRejects: true,
			want:          "17.2.1 re-encoded bytes differ from the built bytes",
		},
		{
			name: "17.2.1 fails at decode",
			change: func(r *contract.JsSdk1766Report) {
				r.Versions[1].Steps[1] = contract.SdkStepResult{Step: contract.SdkReproStepDecode, Passed: false, Error: str("XDR Read Error")}
				r.Versions[1].Steps = r.Versions[1].Steps[:2]
				r.Versions[1].RoundTripIdentical = false
			},
			schemaRejects: true,
			want:          "17.2.1 failed at decode: XDR Read Error",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := decodeReport(t, b) // a fresh copy of the committed report
			tc.change(&rep)
			if tc.schemaRejects {
				doc, err := catalogue.EncodeReproReport(rep)
				if err != nil {
					t.Fatal(err)
				}
				if validateReport(t, doc) == nil {
					t.Error("the report's schema accepts the changed report with passed true")
				}
			}
			rep.Passed = len(verdict(rep)) == 0 // as run sets it
			if rep.Passed {
				t.Fatal("the verdict passes the changed report")
			}
			reasons := Reasons(rep)
			if !strings.Contains(strings.Join(reasons, "; "), tc.want) {
				t.Errorf("Reasons %q do not name %q", reasons, tc.want)
			}
			doc, err := catalogue.WriteJsSdkReport(t.TempDir(), rep)
			if err != nil {
				t.Fatalf("the writer refuses the changed report: %v", err)
			}
			if err := validateReport(t, doc); err != nil {
				t.Errorf("the changed report with passed false does not validate against the report's schema: %v", err)
			}
		})
	}
}
