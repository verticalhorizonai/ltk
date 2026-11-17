// Package jssdk reproduces js-stellar-sdk 1766 before and after its fix: one script builds the pool-share
// trustline of entry 2's issuer pair, decodes it and re-encodes it, against `@stellar/stellar-sdk` 17.2.0, which
// rejects it, and 17.2.1, which round-trips it to the same bytes, in the pinned Node container. It is
// container-dependent, driving Docker through its command-line client, and reads no corpus file: the issuer pair and
// asset codes come from internal/model's seed definitions.
//
// It holds:
//   - repro.mjs, the one script, embedded in the program and run, never imported;
//   - before/ and after/, each release's package.json and its package-lock.json, generated once in the pinned
//     node:22-slim container with `npm install --package-lock-only --ignore-scripts`;
//   - run.go, the runner: each release's labelled container, the files copied in with `docker cp`,
//     `npm ci --ignore-scripts`, the script, its one JSON line parsed;
//   - verdict.go, the pure verdict over the report and its reasons, unit-tested on the recorded lines of testdata/.
package jssdk
