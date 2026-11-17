// Package generate runs `ltk generate`: for each shape, in seed order, it submits the shape's transactions to the local
// network, captures the ledger that holds them and turns the capture into a corpus entry. It is container-dependent: it
// starts the network through internal/orchestration, and it holds the module's only `net/http` client besides
// internal/orchestration's, for RPC.
//
// It holds:
//   - rpc.go, the JSON-RPC 2.0 client with exactly the four RPC methods generation uses: `getLedgerEntries`,
//     `sendTransaction`, `getTransaction` and `getLedgers`, whose `metadataXdr` is the capture;
//   - friendbot.go, the client of the local friendbot that funds the recipes' fixed-seed accounts;
//   - pipeline.go, the Generation pipeline behind `ltk generate --all --out <folder>` (RunAll);
//   - single.go, the single-shape path behind `ltk generate --shape <entry id> --out <folder>` (RunShape);
//   - protocol.go, the Protocol selector: the current protocol first, protocol 28 on a second network when a shape
//     cannot work there, the shape not generated when it cannot work at 28 either.
//
// The shapes themselves are in internal/generate/recipes, with one recipe document per shape (recipes/<entry id>.md);
// the Go SDK XDR codec is in internal/generate/xdrcodec, which stays container-free.
package generate
