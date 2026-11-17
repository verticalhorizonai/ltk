// Package check runs the corpus checks of `ltk check` in CHECK_ORDER: the catalogue against its schema, each entry's
// file integrity, its round trip through Stellar's Rust XDR tool (through internal/check/rustxdr) and its expected
// values, then, with `--also-go`, the Go SDK round trip. Every check runs for every entry and one CheckReport gathers
// them.
package check
