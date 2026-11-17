// Package walletbackend reproduces wallet-backend 729 (pull request 729) before and after its fix: commit 2a0a84ea
// fails to store the signer of entry 1's transaction on its ledger and 2c873353 ingests it, both reading ledgers from
// one local network at protocol 28. It is container-dependent: it drives Docker through its command-line client.
//
// It holds:
//   - build.go, the wallet-backend builder: both commits cloned and built in the pinned Go image with their modules
//     verified against the Go checksum database, the tables the runner reads checked in each commit's migrations, and
//     a fresh TimescaleDB of its own for each commit;
//   - run.go, the reproduction runner: both commits migrated and ingesting from the same RPC, entry 1's recipe
//     submitted, the fix commit's ingest cursor and stored signer row read, the pre-fix commit's five failed attempts,
//     its exit and its one restart observed, each run under the deadline the caller passes;
//   - verdict.go, the pure verdict over the collected evidence and the reasons of a report, unit-tested on the
//     deciding test's recorded logs in testdata/ (the deciding test is the feasibility run made before this tool).
package walletbackend
