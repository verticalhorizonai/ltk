package main

// `ltk generate --all --out <folder>` and `ltk generate --shape <entry id> --out <folder>`. It is container-dependent:
// it starts the local network, runs the generation pipeline of internal/generate inside the interrupt handler's scope,
// then runs the corpus checks on the folder it wrote.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/check"
	"github.com/verticalhorizonai/ltk/internal/check/rustxdr"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate"
	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// generateCommand is `ltk generate`.
func generateCommand() *command {
	return &command{
		name:     "generate",
		synopsis: []string{"ltk generate --all --out <folder>", "ltk generate --shape <entry id> --out <folder>"},
		setup: func(fs *flag.FlagSet) func(context.Context, invocation) error {
			all := fs.Bool("all", false, "generate every shape in catalogue order")
			shape := fs.String("shape", "", "generate only this entry and write only its catalogue record")
			out := fs.String("out", "", "the output corpus folder, created if absent")
			return func(ctx context.Context, inv invocation) error {
				in := contract.GenerateInput{All: *all, Out: *out}
				given := map[string]bool{}
				fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
				if given["shape"] {
					in.Shape = shape
				}
				return runGenerate(ctx, inv, in, given["out"])
			}
		},
	}
}

// generateUsage checks the arguments, the command's first step: exactly one of --all and --shape; --out given and not
// empty; a --shape id that names a shape recipe of the registry (recipes.ByID, never a nearby id); and an --out that,
// when it exists, is a folder (an --out that is a file is an invalid argument). Each violation is a usage error (exit
// 2) found before the environment check: nothing is started and nothing is written.
func generateUsage(in contract.GenerateInput, outGiven bool) error {
	switch {
	case in.All && in.Shape != nil:
		return model.Usage("ltk generate: give either --all or --shape <entry id>, not both")
	case !in.All && in.Shape == nil:
		return model.Usage("ltk generate: give --all or --shape <entry id>")
	case !outGiven || in.Out == "":
		return model.Usage("ltk generate: --out <folder> is required")
	}
	if in.Shape != nil {
		if _, ok := recipes.ByID(*in.Shape); !ok {
			var ids []string
			for _, r := range recipes.Registry() {
				ids = append(ids, r.ID())
			}
			return model.Usage("ltk generate: unknown --shape entry id: %s (the shapes are %s)", *in.Shape, strings.Join(ids, ", "))
		}
	}
	if st, err := os.Stat(in.Out); err == nil && !st.IsDir() {
		return model.Usage("ltk generate: the --out folder %s is a file", in.Out)
	}
	return nil
}

// runGenerate generates the selected shapes into the --out folder and exits by the outcome:
//   - 2, nothing on stdout, nothing started or written: the arguments (generateUsage);
//   - 3, nothing on stdout, nothing started or written: the environment check with GenerateImages (the current and
//     the protocol-28 quickstart);
//   - inside withContainers, the pipeline: generate.RunAll for --all, generate.RunShape for --shape, given the run's
//     one Rust XDR tool adapter as its decoder, which the checks then reuse; the pipeline removes its networks before
//     it returns;
//   - when every shape was generated, check.Run on the folder as written, without the Go SDK cross-check
//     (goSdkRoundTrip null); otherwise no check runs and check is null;
//   - the GenerateReport on stdout, then exit 0 when every shape was generated and the checks passed; otherwise exit 1
//     with one ErrorLine: the shapes not generated, each with its error, or the checks' summary naming each failed
//     entry and check;
//   - exit 1, one ErrorLine and no report when the run must stop: an interrupt (the files already written stay as
//     they are), a write of the folder that failed (the message names the path), or a Rust XDR tool that could not
//     start;
//   - exit 1 when the teardown left a resource of the run, the report already printed.
func runGenerate(ctx context.Context, inv invocation, in contract.GenerateInput, outGiven bool) error {
	if err := generateUsage(in, outGiven); err != nil {
		return err
	}
	err := withContainers(ctx, orchestration.GenerateImages, func(ctx context.Context, r *orchestration.Runner) error {
		codec := rustxdr.New(r) // the run's one Rust XDR tool container, started on its first decode
		var shapes []contract.ShapeGenerationResult
		var err error
		if in.All {
			shapes, err = generate.RunAll(ctx, r, codec, recipes.Registry(), in.Out)
		} else {
			shapes, err = generate.RunShape(ctx, r, codec, recipes.Registry(), *in.Shape, in.Out)
		}
		if err != nil {
			return err
		}
		report := contract.GenerateReport{Out: in.Out, Shapes: shapes}
		failedShapes := notGenerated(shapes)
		var checked check.Outcome
		if len(failedShapes) == 0 {
			if checked, err = check.Run(ctx, in.Out, codec, nil); err != nil {
				return err
			}
			report.Check = &checked.Report
		}
		if err := writeGenerateReport(inv, report); err != nil {
			return err
		}
		switch {
		case len(failedShapes) > 0:
			return model.Failure("generation failed: %d of %d shapes not generated; %s", len(failedShapes), len(shapes), strings.Join(failedShapes, "; "))
		case !checked.Report.Passed:
			return model.Failure("check failed: %s", check.Summary(checked))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("ltk generate: %w", err)
	}
	return nil
}

// notGenerated returns, for each shape with generated false, "<id>: <error>", in the report's order.
func notGenerated(shapes []contract.ShapeGenerationResult) []string {
	var out []string
	for _, s := range shapes {
		if s.Generated {
			continue
		}
		msg := "not generated"
		if s.Error != nil {
			msg = *s.Error
		}
		out = append(out, s.ID+": "+msg)
	}
	return out
}

// writeGenerateReport prints the GenerateReport: one JSON document, two-space indentation, one trailing newline,
// written in one write, as the check report is.
func writeGenerateReport(inv invocation, report contract.GenerateReport) error {
	if report.Shapes == nil {
		return errors.New("the generation returned no shape result")
	}
	return catalogue.WriteJSON(inv.stdout, report)
}
