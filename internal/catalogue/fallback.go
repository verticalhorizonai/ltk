package catalogue

// The Protocol fallback recorder: the one place an entry's catalogue record gets its protocol fields
// (CatalogueEntry.protocolVersion and protocolFallback). Its caller is the generation pipeline (internal/generate),
// once per generated shape, after the recipe's capture and before the catalogue is written:
//
//	if err := catalogue.RecordProtocol(&entry, capture.Protocol, onFallbackNetwork); err != nil { /* the shape fails */ }
//
// capture.Protocol is the protocol read from the captured ledger header, never the protocol the
// network was asked for; onFallbackNetwork is true when the shape was generated on the protocol-28 network the
// Protocol selector (internal/generate/protocol.go) started after the shape could not work at the current
// protocol.

import (
	"fmt"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// RecordProtocol records on e the protocol its capture ran at and whether it was generated on the fallback network:
//   - protocolVersion is capturedProtocol, the version read from the captured ledger header;
//   - protocolFallback is onFallbackNetwork: true only when the recipe failed at the current stable protocol and the
//     entry was generated at FALLBACK_PROTOCOL_VERSION (28).
//
// The record is held to the catalogue schema's rule, so a catalogue it writes always validates: an entry with
// protocolFallback true carries protocolVersion 28. A capture taken on the fallback network whose header states another
// protocol, or a captured protocol below 1, is an error, e is left unchanged, and the shape is not generated: the
// catalogue never states a protocol the ledger did not run.
func RecordProtocol(e *contract.CatalogueEntry, capturedProtocol int, onFallbackNetwork bool) error {
	if capturedProtocol < 1 {
		return fmt.Errorf("entry %s: the captured ledger header states protocol %d; a protocol version is at least 1", e.ID, capturedProtocol)
	}
	if onFallbackNetwork && capturedProtocol != contract.FALLBACK_PROTOCOL_VERSION {
		return fmt.Errorf("entry %s: generated on the protocol-%d fallback network, but the captured ledger header states protocol %d",
			e.ID, contract.FALLBACK_PROTOCOL_VERSION, capturedProtocol)
	}
	e.ProtocolVersion, e.ProtocolFallback = capturedProtocol, onFallbackNetwork
	return nil
}
