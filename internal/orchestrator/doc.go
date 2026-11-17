// Package orchestrator holds the end-to-end verification of ltk: tests that run the built `ltk` from outside, as a
// user does, and hold its commands, their reports and files, the error line and the exit codes to the contract ltk
// documents: its `--help`, its report schemas and its README. It holds no production code: every file but this one is
// a test file. It is not internal/orchestration, which drives Docker.
//
// The program under test. The shared helper of harness_test.go takes the program from the environment variable
// LTK_BINARY when it is set and builds nothing then; otherwise it builds ../../cmd/ltk once per test process, with the
// go tool found on PATH or under runtime.GOROOT(), into a temporary folder. A test that cannot have the program fails,
// saying why; it never passes without running it.
//
// Running the tests.
//
//   - The unit tier (no Docker): `go test ./internal/orchestrator/`, part of `go test ./...` on every system.
//   - The integration tier (Docker): the tests carry the `integration` build tag and run serially, never in parallel,
//     because they compare snapshots of the whole Docker engine before and after a run. The one command, the
//     README's, is
//     `go test -tags integration -p 1 -timeout 120m ./cmd/ltk/ ./internal/orchestrator/ ./internal/orchestration/ ./internal/generate/... ./internal/repro/...`.
//   - On a machine whose Go toolchain runs in a container that cannot reach the host's loopback ports, the tier runs
//     natively, as shown below.
//
// The native run: cross-compile the program and this package's test binary for the host, then run the test binary
// from this package's folder with LTK_BINARY naming the program. Here the container's /work is the host's folder $OUT.
//
//	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o /work/ltk ./cmd/ltk
//	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go test -c [-tags integration] -o /work/orchestrator.test ./internal/orchestrator
//	cd internal/orchestrator && LTK_BINARY="$OUT/ltk" "$OUT/orchestrator.test" -test.v
package orchestrator
