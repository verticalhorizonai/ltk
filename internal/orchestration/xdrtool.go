package orchestration

// The Rust XDR tool runner. Stellar's Rust XDR tool is the `stellar-xdr` copy shipped inside the digest-pinned current
// quickstart image; nothing is built from the crate. One labelled container of that image runs per command run, its
// entrypoint replaced so that it only idles, on no network and with no published port; each decode and encode is a
// `docker exec -i` in it, with the flags the deciding test, the feasibility run made before this tool, used. The
// reaper removes the container before the command exits.

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// xdrToolRole names the tool's container: ltk-<runID>-xdr.
const xdrToolRole = "xdr"

// XDRTool is the running container of Stellar's Rust XDR tool for one command run.
type XDRTool struct {
	r         *Runner
	container string
}

// StartXDRTool starts the run's Rust XDR tool container from QuickstartCurrent: labelled, named ltk-<runID>-xdr, with
// `--network none`, no published port, and `sleep infinity` as its entrypoint so that no stellar-core or RPC starts
// The round-trip adapter of internal/check/rustxdr calls it once per `ltk check` or `ltk generate`.
func (r *Runner) StartXDRTool(ctx context.Context) (*XDRTool, error) {
	name, err := r.Run(ctx, ContainerSpec{
		Role:       xdrToolRole,
		Image:      QuickstartCurrent,
		Network:    "none",
		Entrypoint: "sleep",
		Args:       []string{"infinity"},
	})
	if err != nil {
		return nil, err
	}
	return &XDRTool{r: r, container: name}, nil
}

// XDRToolError is a `stellar-xdr` call that failed: the bytes are not the declared type, the JSON does not encode, or
// the type is unknown to the tool. It carries the tool's standard error.
type XDRToolError struct {
	// Op is "decode" or "encode".
	Op string
	// Type is the XDR type the call declared.
	Type string
	// ExitCode is the exit code of `docker exec` (the tool's own, or Docker's when the exec itself failed).
	ExitCode int
	// Stderr is the tool's standard error, trimmed.
	Stderr string
	// Err is the runner's error.
	Err error
}

// maxToolMessage bounds the tool's message in Error(): for an unknown type stellar-xdr lists every type it knows, and
// the message ends up in the one ErrorLine. The whole of it stays in Stderr.
const maxToolMessage = 300

// Error names the operation, the type and the first line of the tool's message, at most maxToolMessage bytes.
func (e *XDRToolError) Error() string {
	msg := fmt.Sprintf("stellar-xdr %s --type %s failed", e.Op, e.Type)
	line := firstLine(e.Stderr)
	if line == "" {
		return msg + ": " + e.Err.Error()
	}
	return msg + ": " + capLine(line, maxToolMessage)
}

// Unwrap returns the runner's error.
func (e *XDRToolError) Unwrap() error { return e.Err }

// Decode decodes raw XDR bytes of xdrType to the tool's JSON form:
// `stellar-xdr decode --type <T> --input single --output json-formatted`, the bytes on standard input.
// The JSON is returned as the tool wrote it, so its field order is kept.
func (t *XDRTool) Decode(ctx context.Context, xdrType string, raw []byte) ([]byte, error) {
	return t.run(ctx, "decode", xdrType, raw, "--input", "single", "--output", "json-formatted")
}

// Encode encodes the tool's JSON form of xdrType back to raw XDR bytes:
// `stellar-xdr encode --type <T> --input json --output single`, the JSON on standard input.
func (t *XDRTool) Encode(ctx context.Context, xdrType string, jsonForm []byte) ([]byte, error) {
	return t.run(ctx, "encode", xdrType, jsonForm, "--input", "json", "--output", "single")
}

// run executes one `stellar-xdr` call in the tool's container.
func (t *XDRTool) run(ctx context.Context, op, xdrType string, in []byte, flags ...string) ([]byte, error) {
	args := append([]string{"stellar-xdr", op, "--type", xdrType}, flags...)
	out, _, err := t.r.Exec(ctx, t.container, in, args...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		te := &XDRToolError{Op: op, Type: xdrType, ExitCode: -1, Err: err}
		var ce *CommandError
		if errors.As(err, &ce) {
			te.ExitCode, te.Stderr = ce.ExitCode, strings.TrimSpace(ce.Stderr)
		}
		return nil, te
	}
	return out, nil
}
