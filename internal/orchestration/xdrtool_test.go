package orchestration

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// The tool's container is the current quickstart, labelled, named ltk-<runID>-xdr, on no network,
// with no published port, idling instead of starting core and RPC.
func TestStartXDRToolArguments(t *testing.T) {
	f := newFake(t)
	tool, err := f.runner("c0ffee00").StartXDRTool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := "run -d --label ltk.managed=true --label ltk.run=c0ffee00 --name ltk-c0ffee00-xdr --network none --entrypoint sleep " +
		QuickstartCurrent + " infinity"
	if got := f.argLines(); !slices.Equal(got, []string{want}) {
		t.Fatalf("calls %q; want %q", got, want)
	}
	if tool.container != "ltk-c0ffee00-xdr" {
		t.Fatalf("container %q", tool.container)
	}
}

// Decode and encode are `docker exec -i` of stellar-xdr with the flags the deciding test, the feasibility run made
// before this tool, used, the input on standard input and the output from standard output.
func TestXDRToolDecodeAndEncodeArguments(t *testing.T) {
	f := newFake(t, fakeRule{Prefix: []string{"exec"}, EchoStdin: true})
	tool, err := f.runner("c0ffee00").StartXDRTool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte{0, 0, 0, 1, 0xff, 0x10}
	gotJSON, err := tool.Decode(context.Background(), "LedgerCloseMeta", raw)
	if err != nil || !bytes.Equal(gotJSON, raw) {
		t.Fatalf("Decode = %v, %v", gotJSON, err)
	}
	jsonForm := []byte(`{"v0":{}}`)
	gotRaw, err := tool.Encode(context.Background(), "TransactionEnvelope", jsonForm)
	if err != nil || !bytes.Equal(gotRaw, jsonForm) {
		t.Fatalf("Encode = %q, %v", gotRaw, err)
	}
	calls := f.calls()[1:]
	want := []string{
		"exec -i ltk-c0ffee00-xdr stellar-xdr decode --type LedgerCloseMeta --input single --output json-formatted",
		"exec -i ltk-c0ffee00-xdr stellar-xdr encode --type TransactionEnvelope --input json --output single",
	}
	if len(calls) != 2 || strings.Join(calls[0].Args, " ") != want[0] || strings.Join(calls[1].Args, " ") != want[1] {
		t.Fatalf("calls %q; want %q", f.argLines()[1:], want)
	}
	if !bytes.Equal(calls[0].Stdin, raw) || !bytes.Equal(calls[1].Stdin, jsonForm) {
		t.Fatalf("stdin %v and %q", calls[0].Stdin, calls[1].Stdin)
	}
}

// A non-zero exit of stellar-xdr is a typed *XDRToolError carrying the tool's standard error.
func TestXDRToolErrorCarriesStderr(t *testing.T) {
	f := newFake(t, fakeRule{Prefix: []string{"exec"}, Stderr: "Error: Xdr(Invalid)\n", Exit: 1})
	tool, err := f.runner("c0ffee00").StartXDRTool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Decode(context.Background(), "LedgerCloseMeta", []byte("not xdr"))
	var te *XDRToolError
	if !errors.As(err, &te) {
		t.Fatalf("Decode: %v; want a *XDRToolError", err)
	}
	if te.Op != "decode" || te.Type != "LedgerCloseMeta" || te.ExitCode != 1 || te.Stderr != "Error: Xdr(Invalid)" {
		t.Fatalf("error %+v", te)
	}
	if !strings.Contains(err.Error(), "Error: Xdr(Invalid)") {
		t.Fatalf("text %q", err.Error())
	}
}

// The error's text is the first line of the tool's message, bounded; the whole message stays in Stderr.
func TestXDRToolErrorTextIsBounded(t *testing.T) {
	long := "error: unknown type NoSuchType, choose one of [" + strings.Repeat(`"Value", `, 200) + "]\nsecond line"
	e := &XDRToolError{Op: "decode", Type: "NoSuchType", ExitCode: 2, Stderr: long, Err: errors.New("exit status 2")}
	text := e.Error()
	if !strings.HasPrefix(text, "stellar-xdr decode --type NoSuchType failed: error: unknown type NoSuchType") ||
		len(text) > 400 || strings.Contains(text, "second line") {
		t.Fatalf("Error() = %q", text)
	}
	e.Stderr = ""
	if got := e.Error(); got != "stellar-xdr decode --type NoSuchType failed: exit status 2" {
		t.Fatalf("Error() with no message = %q", got)
	}
}
