package orchestration

// The image digests: images are pinned by digest, and the digests are in the repository. Every image the tool runs is
// one of these constants; none carries a tag. Bumping the current quickstart and CurrentProtocol is one reviewed
// change.

import (
	"context"
	"errors"

	"github.com/verticalhorizonai/ltk/internal/model"
)

const (
	// QuickstartCurrent is the current quickstart (revision 35a3f46e, built 2026-11-12): core, RPC and stellar-xdr
	// v30.0.0, protocol 30 by default. It runs the generation network at the current protocol and the Rust XDR tool of
	// `ltk check` and `ltk generate`.
	QuickstartCurrent = "stellar/quickstart@sha256:a61ce821add98adb4f3e3d6e757b2615dd169a2b39c7e30cd47cfa95074f97d9"
	// CurrentProtocol is the protocol QuickstartCurrent declares as its default in its /image.json
	// (`protocol_version_default`), the most recent stable protocol generation targets first.
	CurrentProtocol = 30

	// QuickstartProtocol28 is the quickstart the deciding test, the feasibility run made before this tool, used (core
	// and RPC 28.0.1). It runs at protocol 28 with no flag: the wallet-backend 729 network and the fallback network.
	QuickstartProtocol28 = "stellar/quickstart@sha256:057b31d5c23d90d58c4637c2888163f1c78afd6c08941339121f4feb9d78b087"
	// FallbackProtocol is the protocol a shape that cannot work at the current protocol is generated at.
	FallbackProtocol = 28

	// GoBuildImage is golang:1.25.9-bookworm, the Go container that builds wallet-backend at both commits.
	GoBuildImage = "golang@sha256:298734aec230b5f3e8cee450ce6d7eccc39f1797ba548ee90d57e9803030c6c3"
	// TimescaleDBImage is timescale/timescaledb:2.28.2-pg17, the database of each wallet-backend run.
	TimescaleDBImage = "timescale/timescaledb@sha256:909e2c7577c074517c8936b6d8799294b33f25fb5bad978a2199250665f1ce1a"
	// NodeImage is node:22-slim, the Node container of the js-sdk reproduction script.
	NodeImage = "node@sha256:e9f565d32859f0cbfb52125eafe8be39390f24dac302dc3cdf566e8ba3ab21ee"
)

// quickstartDefaultProtocol is the protocol each quickstart image reaches with no --protocol-version flag, from its
// /image.json (`protocol_version_default`, with `protocol_version_default_autoupdate`). The launcher adds the flag
// only for another protocol.
var quickstartDefaultProtocol = map[string]int{
	QuickstartCurrent:    CurrentProtocol,
	QuickstartProtocol28: FallbackProtocol,
}

// The image set of each container-dependent command: what its environment check inspects, and pulls when absent,
// before any work.
var (
	// GenerateImages are `ltk generate`'s: the current quickstart (the network and the Rust XDR tool) and the
	// protocol-28 quickstart of the fallback, so that a fallback never stops for a pull halfway.
	GenerateImages = []string{QuickstartCurrent, QuickstartProtocol28}
	// CheckImages are `ltk check`'s: the current quickstart, for the Rust XDR tool of its round trip.
	CheckImages = []string{QuickstartCurrent}
	// WalletBackendImages are `ltk repro wallet-backend-729`'s: the protocol-28 network, the Go build container and
	// TimescaleDB.
	WalletBackendImages = []string{QuickstartProtocol28, GoBuildImage, TimescaleDBImage}
	// JsSdkImages are `ltk repro js-sdk-1766`'s: the Node container.
	JsSdkImages = []string{NodeImage}
)

// EnsureImages inspects each image (`docker image inspect`) and pulls an absent one by its digest (`docker pull`). A
// pull that fails is an environment error (exit 3) naming the image and docker's reason: "digest-pinned image cannot
// be pulled: <image> (<reason>)". The command's environment check calls it with the command's image set before any
// work.
func (r *Runner) EnsureImages(ctx context.Context, images []string) error {
	for _, ref := range images {
		if _, _, err := r.call(ctx, nil, "image", "inspect", "--format", "{{.Id}}", ref); err == nil {
			continue
		} else if ctx.Err() != nil {
			return err
		}
		if _, _, err := r.call(ctx, nil, "pull", ref); err != nil {
			if ctx.Err() != nil {
				return err
			}
			detail := err.Error()
			var ce *CommandError
			if errors.As(err, &ce) && firstLine(ce.Stderr) != "" {
				detail = firstLine(ce.Stderr)
			}
			return model.Environment("digest-pinned image cannot be pulled: %s (%s)", ref, detail)
		}
	}
	return nil
}
