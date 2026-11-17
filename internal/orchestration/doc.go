// Package orchestration is the tool's only door to Docker: every container, network and volume of `ltk generate`,
// `ltk check`'s round trip and the two reproductions is made, inspected and removed here, through the `docker` CLI. It
// is container-dependent and holds the module's only `net/http` client besides internal/generate's, for the readiness
// probe.
//
// It holds:
//   - docker.go, the Docker CLI runner: every create call labelled `ltk.managed=true` and
//     `ltk.run=<runID>`, names `ltk-<runID>-<role>`, published ports only as `127.0.0.1::<port>`;
//   - images.go, the image digests, the current-protocol constant of the pinned current quickstart and each
//     command's image set, with the image inspection, and the pull by digest of an absent image, before any work;
//   - quickstart.go, the Quickstart network launcher: local mode, core and RPC only, on the run's own network,
//     ready when RPC is healthy at the requested protocol on the standalone passphrase and the friendbot answers
//     (the current quickstart at its protocol, and the protocol-28 quickstart for the fallback and wallet-backend);
//   - reaper.go, the Resource reaper (`docker rm -f -v`, then networks, then volumes, by `ltk.run`) and the
//     clean-stop helpers `Snapshot` and `Leftovers`, a before/after difference of every container, network and volume;
//   - containers.go, the container life-cycle calls of the two reproductions: a labelled volume and
//     its mounts, `docker wait`, the state of a container, `docker start` (the pre-fix commit's restart), stop,
//     remove, the whole timestamped log, and `docker cp -` of a tar stream;
//   - xdrtool.go, the Rust XDR tool runner: `stellar-xdr` of the current quickstart image, run in one labelled
//     container that starts no network and publishes no port;
//   - the tests behind the `integration` build tag: the two quickstarts brought up and torn down, the Rust XDR
//     tool's round trip, and `ltk generate` run as the built program named by `LTK_BINARY`.
package orchestration
