package check

// The Go SDK cross-check step: with `--also-go`, each entry's file is also decoded and re-encoded with the Go SDK's XDR
// code (internal/generate/xdrcodec), a second decoder alongside the Rust XDR tool, and the re-encoding is compared with
// the file byte for byte. It is reported in goSdkRoundTrip, apart from the Rust tool's round_trip, and its failures are
// labelled go_sdk_round_trip.

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate/xdrcodec"
)

// GoCodec is the Go SDK's round trip of one XDR value: decode raw as xdrType, re-encode it, return the bytes. The
// program's is xdrcodec.RoundTrip (GoSDK); a unit test may put a double in its place. A nil GoCodec given to Run means
// `--also-go` was not given: the cross-check does not run and every goSdkRoundTrip is null.
type GoCodec func(xdrType string, raw []byte) ([]byte, error)

// GoSDK is the Go SDK XDR codec's round trip, the GoCodec `ltk check --also-go` runs.
var GoSDK GoCodec = xdrcodec.RoundTrip

// GoSdkRoundTrip runs the Go SDK cross-check of entry e on its file. The check passes when the bytes the Go SDK
// re-encoded equal the file byte for byte:
//   - bytesCompared follows round_trip's rule: the number of byte positions compared, the shorter of the two lengths,
//     equal to the file's length when it passes, and 0 when nothing was re-encoded (the file is unreadable, the Go SDK
//     does not decode it or does not know its type);
//   - a type the codec does not map to a Go SDK type is a failure naming the type, never a panic.
//
// file and readErr are the entry's XDR file as the runner read it. The reason names the entry and the check
// go_sdk_round_trip, so that the error line tells it apart from the Rust tool's round_trip; it is "" when
// the check passed.
func GoSdkRoundTrip(goCodec GoCodec, e contract.CatalogueEntry, file []byte, readErr error) (contract.GoSdkRoundTripResult, string) {
	r := contract.GoSdkRoundTripResult{Check: contract.CheckNameGoSdkRoundTrip}
	prefix := fmt.Sprintf("entry %s: go_sdk_round_trip: ", e.ID)
	if readErr != nil {
		return r, prefix + fmt.Sprintf("the XDR file %s cannot be read, so the Go SDK decoded nothing: %v", e.FilePath, readErr)
	}
	reencoded, err := goCodec(e.XdrType, file)
	if err != nil {
		var unknown *xdrcodec.UnknownTypeError
		if errors.As(err, &unknown) {
			return r, prefix + fmt.Sprintf("the file %s declares the XDR type %q, which the Go SDK codec does not know: %v", e.FilePath, unknown.Type, err)
		}
		return r, prefix + fmt.Sprintf("the Go SDK does not decode and re-encode the file %s as %s: %v", e.FilePath, e.XdrType, err)
	}
	r.BytesCompared = min(len(file), len(reencoded))
	if bytes.Equal(file, reencoded) {
		r.Passed = true
		return r, ""
	}
	return r, prefix + fmt.Sprintf("the Go SDK's re-encoded bytes differ from the file %s at offset %d (the file is %d bytes, the re-encoding %d)",
		e.FilePath, firstDifference(file, reencoded), len(file), len(reencoded))
}
