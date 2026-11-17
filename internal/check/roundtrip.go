package check

// The check round_trip: each entry's file decoded and re-encoded by Stellar's Rust XDR tool, the result compared with
// the file byte for byte, the count of bytes compared reported, and the decoding compared with the entry's decoded JSON
// file as JSON values.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
)

// Codec is the decoder the round trip and the expected values read through: Stellar's Rust XDR tool, run in the
// quickstart container by the adapter of internal/check/rustxdr. Decode turns raw XDR bytes of xdrType into the
// tool's JSON form; Encode turns that JSON form back into raw bytes. A unit test puts a double in its place.
type Codec interface {
	Decode(ctx context.Context, xdrType string, raw []byte) ([]byte, error)
	Encode(ctx context.Context, xdrType string, jsonForm []byte) ([]byte, error)
}

// UnavailableError is a codec that could not be had at all (the Rust XDR tool's container did not start). It stops the
// command, as an interrupt does, rather than failing one entry's round trip: no check result would be trustworthy.
type UnavailableError struct {
	// Err says why.
	Err error
}

// Error says that the decoder is unavailable and why.
func (e *UnavailableError) Error() string {
	return "Stellar's Rust XDR tool is not available: " + e.Err.Error()
}

// Unwrap returns the cause.
func (e *UnavailableError) Unwrap() error { return e.Err }

// RoundTripOutcome is one entry's round trip: its result, the decoded form the tool produced from the file (nil when
// the file could not be read or decoded), and why it failed ("" when it passed), for the check's error line.
type RoundTripOutcome struct {
	Result contract.RoundTripResult
	// Decoded is the Rust XDR tool's JSON form of the file: the decoded form the expected values are read from. It is
	// set whenever the decode succeeded, whether or not the re-encoding did.
	Decoded []byte
	// Reason names the entry and says why the check failed: the file is unreadable, it is not decodable as its declared
	// type, its re-encoding fails, or the bytes differ (with the first differing offset); and, whenever the file was
	// decoded, its decoded JSON file differs from the decoding, is missing or is not JSON. Two failures are joined by
	// "; ".
	Reason string
}

// RoundTrip decodes the file of entry e as e.XdrType, re-encodes the JSON form, and compares the result with the file
// byte for byte; it also holds the entry's decoded JSON file to the decoding:
//   - bytesCompared is the number of byte positions compared, the shorter of the two lengths; it equals the file's
//     length when the bytes are equal, and is 0 when nothing was re-encoded (the file is unreadable, it does not decode,
//     or its re-encoding fails);
//   - whenever the tool decoded the file, its decoding must equal the decoded JSON file beside it (entryJSON, read as
//     jsonErr says) as JSON values: key order and white space aside, numbers as their decimal text (`1.0` differs
//     from `1`), a difference in shape reported at its own pointer. A
//     difference (named by its first pointer and both values), a missing or unreadable file, or one that is not JSON
//     fails the check; bytesCompared is not changed by it;
//   - passed is true only when both lengths are equal, every byte matches and the decoded JSON file equals the
//     decoding;
//   - the first differing offset and pointer are stated in Reason only: RoundTripResult has no field for them.
//
// file and readErr are the entry's XDR file as the runner read it, entryJSON and jsonErr its decoded JSON file. Each
// failure is a failed check naming the entry. The error is returned only when the command must stop: its context was
// cancelled (an interrupt), or the codec is an *UnavailableError.
func RoundTrip(ctx context.Context, codec Codec, e contract.CatalogueEntry, file []byte, readErr error, entryJSON []byte, jsonErr error) (RoundTripOutcome, error) {
	out := RoundTripOutcome{Result: contract.RoundTripResult{Check: contract.CheckNameRoundTrip}}
	fail := func(problems ...string) (RoundTripOutcome, error) {
		var said []string
		for _, p := range problems {
			if p != "" {
				said = append(said, p)
			}
		}
		out.Reason = fmt.Sprintf("entry %s: round_trip: ", e.ID) + strings.Join(said, "; ")
		return out, nil
	}
	if readErr != nil {
		return fail(fmt.Sprintf("the XDR file %s cannot be read, so nothing was decoded: %v", e.FilePath, readErr))
	}
	decoded, err := codec.Decode(ctx, e.XdrType, file)
	if err != nil {
		if stop := stopError(ctx, err); stop != nil {
			return out, stop
		}
		return fail(fmt.Sprintf("the file %s is not decodable as %s: %v", e.FilePath, e.XdrType, err))
	}
	out.Decoded = decoded
	jsonProblem := decodedJSONProblem(e, decoded, entryJSON, jsonErr)
	reencoded, err := codec.Encode(ctx, e.XdrType, decoded)
	if err != nil {
		if stop := stopError(ctx, err); stop != nil {
			return out, stop
		}
		return fail(fmt.Sprintf("the decoded form of %s does not re-encode as %s: %v", e.FilePath, e.XdrType, err), jsonProblem)
	}
	n := min(len(file), len(reencoded))
	out.Result.BytesCompared = n
	bytesProblem := ""
	if !bytes.Equal(file, reencoded) {
		bytesProblem = fmt.Sprintf("the re-encoded bytes differ from the file %s at offset %d (the file is %d bytes, the re-encoding %d)",
			e.FilePath, firstDifference(file, reencoded), len(file), len(reencoded))
	}
	if bytesProblem == "" && jsonProblem == "" {
		out.Result.Passed = true
		return out, nil
	}
	return fail(bytesProblem, jsonProblem)
}

// decodedJSONProblem holds the entry's decoded JSON file (entryJSON, read as jsonErr says) to the tool's decoding of
// its XDR file as JSON values, and says why it does not hold; "" when it does.
func decodedJSONProblem(e contract.CatalogueEntry, decoded, entryJSON []byte, jsonErr error) string {
	name := catalogue.DecodedJSONPath(e.FilePath)
	if jsonErr != nil {
		return fmt.Sprintf("the decoded JSON file %s cannot be read: %v", name, jsonErr)
	}
	committed, err := catalogue.Parse(entryJSON)
	if err != nil {
		return fmt.Sprintf("the decoded JSON file %s is not JSON: %v", name, err)
	}
	fresh, err := catalogue.Parse(decoded)
	if err != nil {
		return fmt.Sprintf("the Rust XDR tool's decoding of %s is not JSON: %v", e.FilePath, err)
	}
	ptr, differs := catalogue.FirstDifference(committed, fresh)
	if !differs {
		return ""
	}
	inFile, _ := committed.Resolve(ptr)
	inDecoding, _ := fresh.Resolve(ptr)
	return fmt.Sprintf("the decoded JSON file %s differs from the Rust XDR tool's decoding of %s at %q: the file holds %s, the decoding %s",
		name, e.FilePath, ptr, shortJSON(inFile.Raw), shortJSON(inDecoding.Raw))
}

// shortJSONLimit bounds a value quoted in a reason: a difference in shape carries a whole object or array.
const shortJSONLimit = 200

// shortJSON is raw JSON text with its insignificant white space removed, cut to at most shortJSONLimit bytes on a
// character boundary, with "…".
func shortJSON(raw []byte) string {
	s := string(compactJSON(raw))
	if len(s) <= shortJSONLimit {
		return s
	}
	n := shortJSONLimit
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// firstDifference is the offset of the first byte at which a and b differ; when one is a prefix of the other, the
// length of the shorter.
func firstDifference(a, b []byte) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// stopError returns the error that stops the command, or nil when err is the codec's answer about this entry.
func stopError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w", ctxErr, err)
	}
	var unavailable *UnavailableError
	if errors.As(err, &unavailable) {
		return err
	}
	return nil
}
