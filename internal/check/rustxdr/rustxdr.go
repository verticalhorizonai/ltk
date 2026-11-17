// Package rustxdr is the Rust XDR decoder adapter: the round trip's and the expected values' decoder, Stellar's Rust
// XDR tool (`stellar-xdr`) shipped in the digest-pinned current quickstart image, reached through the Rust XDR tool
// runner of internal/orchestration. Nothing is built from the crate and no build of the tool is in the program.
package rustxdr

import (
	"context"

	"github.com/verticalhorizonai/ltk/internal/check"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// Adapter is the decoder of one command run. It starts the run's one Rust XDR tool container on its first call, so a
// run that decodes nothing (a catalogue that failed its schema) starts none; the run's reaper removes it before the
// command exits. It satisfies check.Codec. It is used by one goroutine.
type Adapter struct {
	runner *orchestration.Runner
	tool   *orchestration.XDRTool
}

// New returns the adapter of the run whose Docker CLI runner is r. The command handler calls it after the environment
// check, inside the interrupt handler's reaper scope (cmd/ltk/interrupt.go).
func New(r *orchestration.Runner) *Adapter {
	return &Adapter{runner: r}
}

// Decode decodes raw XDR bytes of xdrType to the tool's JSON form (`stellar-xdr decode`). A failure of the tool
// is an *orchestration.XDRToolError carrying its standard error.
func (a *Adapter) Decode(ctx context.Context, xdrType string, raw []byte) ([]byte, error) {
	t, err := a.started(ctx)
	if err != nil {
		return nil, err
	}
	return t.Decode(ctx, xdrType, raw)
}

// Encode encodes the tool's JSON form of xdrType back to raw XDR bytes (`stellar-xdr encode`).
func (a *Adapter) Encode(ctx context.Context, xdrType string, jsonForm []byte) ([]byte, error) {
	t, err := a.started(ctx)
	if err != nil {
		return nil, err
	}
	return t.Encode(ctx, xdrType, jsonForm)
}

// started returns the run's tool container, starting it on the first call. A container that does not start is a
// *check.UnavailableError, which stops the command: no entry's round trip could be trusted (an interrupt while it
// starts is returned as it is).
func (a *Adapter) started(ctx context.Context) (*orchestration.XDRTool, error) {
	if a.tool != nil {
		return a.tool, nil
	}
	t, err := a.runner.StartXDRTool(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		return nil, &check.UnavailableError{Err: err}
	}
	a.tool = t
	return t, nil
}
