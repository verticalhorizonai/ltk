package main

// `ltk check <folder> [--also-go]`: the corpus checks of CHECK_ORDER on a corpus folder. It is container-dependent: its
// round trip runs Stellar's Rust XDR tool in the quickstart container. It writes no file: decoded JSON and re-encoded
// bytes are intermediate.

import (
	"context"
	"flag"
	"fmt"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/check"
	"github.com/verticalhorizonai/ltk/internal/check/rustxdr"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// checkCommand is `ltk check`.
func checkCommand() *command {
	return &command{
		name:        "check",
		synopsis:    []string{"ltk check <folder> [--also-go]"},
		positionals: []string{"<folder>"},
		setup: func(fs *flag.FlagSet) func(context.Context, invocation) error {
			alsoGo := fs.Bool("also-go", false, "also decode and re-encode each entry with the Go SDK's XDR code")
			return func(ctx context.Context, inv invocation) error {
				return runCheck(ctx, inv, *alsoGo)
			}
		},
	}
}

// runCheck checks the corpus folder and exits by the report:
//   - 2, nothing on stdout: an unknown flag, a missing or extra argument, a folder that does not exist (all before the
//     environment check);
//   - 3, nothing on stdout: Docker with Linux containers is not available, or the digest-pinned quickstart image cannot
//     be pulled (the environment check, before any check);
//   - 0, the CheckReport on stdout and nothing on stderr: every check passed, with `--also-go` the Go SDK cross-check
//     of every entry included (go_sdk_round_trip);
//   - 1, the CheckReport on stdout and one ErrorLine naming each failed check and its entry; also a run whose teardown
//     left a resource in the engine, or an interrupted run (no report).
func runCheck(ctx context.Context, inv invocation, alsoGo bool) error {
	var goCodec check.GoCodec // nil: without --also-go every goSdkRoundTrip is null
	if alsoGo {
		goCodec = check.GoSDK
	}
	folder := inv.args[0]
	if err := catalogue.CheckFolder(folder); err != nil {
		return model.Usage("ltk check: %v", err)
	}
	err := withContainers(ctx, orchestration.CheckImages, func(ctx context.Context, r *orchestration.Runner) error {
		out, err := check.Run(ctx, folder, rustxdr.New(r), goCodec)
		if err != nil {
			return err
		}
		if err := check.WriteReport(inv.stdout, out); err != nil {
			return err
		}
		if out.Report.Passed {
			return nil
		}
		return model.Failure("%s", check.Summary(out))
	})
	if err != nil {
		return fmt.Errorf("ltk check: %w", err)
	}
	return nil
}
