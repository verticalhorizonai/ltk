// Package repro groups the two reproductions of listed upstream bugs that `ltk repro` runs. Each is its own package:
// internal/repro/walletbackend (`ltk repro wallet-backend-729`) and internal/repro/jssdk (`ltk repro js-sdk-1766`).
// Their reports are written by the Repro report writer (internal/catalogue); their working folders, which hold the
// logs and other files with no structured format, are outside the repository's tree: within it, `ltk repro` writes
// only under repro/. This package itself holds no code.
package repro
