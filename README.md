# ltk: the seed of a Stellar ledger test corpus

`ltk` (a working name) is a command-line program and three corpus entries, each the close metadata
(`LedgerCloseMeta`) of one ledger, captured from stellar-core. It is the seed of a proposed test kit for Stellar's XDR: a catalogue with its JSON Schema, three real corpus entries, the checks a
corpus entry needs, and two known bugs reproduced on demand. It has no server, no database and no web interface.

It does four things:

1. **Generates** corpus entries from a local stellar-core (`ltk generate`).
2. **Checks** a corpus: the catalogue against its schema, each file against its recorded hash and length, each entry
   through a decode and re-encode in Stellar's Rust XDR library, and the expected values read again from the decoded
   form (`ltk check`).
3. **Compares** two generations, to show which values reproduce (`ltk compare`).
4. **Reproduces** two known bugs, each before and after its fix (`ltk repro`).

The three entries are committed in `corpus/`, so the checks run without generating anything.

## What the seed is, and what it is not

**Three entries are a seed, not a corpus.** The request this seed answers asks a proposal for "a draft manifest
schema, two or three sample corpus entries, and evidence of reproducing one sighting". The seed is those things, built
as a working tool: the catalogue and its schema, three entries, and two reproductions. What a first release of the
kit would add is listed below.

### The three entries

| # | Entry id | CAP | Protocol version | XDR type | Length (bytes) | Synthetic | Captured in the deciding test |
|---|---|---|---|---|---|---|---|
| 1 | `cap-0040-signed-payload-signer` | CAP-0040 | 30 | LedgerCloseMeta | 1928 | false | yes |
| 2 | `cap-0038-pool-share-trustline` | CAP-0038 | 30 | LedgerCloseMeta | 2824 | false | yes |
| 3 | `cap-0027-muxed-account` | CAP-0027 | 30 | LedgerCloseMeta | 2244 | false | no: new, and unverified before this tool |

Each entry is the `LedgerCloseMeta` of the one ledger that holds the shape's transaction, as a local stellar-core
emitted it: the `metadataXdr` that its RPC service's `getLedgers` returns. None is synthetic. The table is the
catalogue's (`corpus/catalogue.json`), and `ltk list corpus/` prints the same values.

"The deciding test" is the feasibility run made before this tool was written. It built the shapes of entries 1 and 2
with a small Go program against a local stellar-core at protocol 28, captured them and round-tripped them through Stellar's
Rust XDR tool. Entry 3 was not part of it: its shape was unverified until this tool's own runs generated and checked
it.

### What each entry demonstrates

- **Entry 1, `cap-0040-signed-payload-signer` (CAP-0040, signed-payload signers).** A `SetOptions` that adds a
  signed-payload signer whose payload is 29 bytes long. 29 is not a multiple of four, so the XDR carries padding inside
  the signer key. It is tied to wallet-backend
  [pull request 729](https://github.com/stellar/wallet-backend/pull/729), "fix: handle CAP-40 signed-payload signers
  in ingestion": before that fix, wallet-backend failed to store such a signer, whose 68-byte key payload does not fit
  a column made for 32-byte keys. `ltk repro wallet-backend-729` reproduces it.
- **Entry 2, `cap-0038-pool-share-trustline` (CAP-0038, liquidity pools).** A pool-share `ChangeTrust` whose two
  issuers sort one way as strkey text and the other way as key bytes. The protocol orders a pool's two assets by asset
  type, then asset code, then issuer, and compares issuers by their key bytes; both of entry 2's assets are `USD`
  credit assets, so the issuers decide. It is tied to js-stellar-sdk pull request 1766 (<https://github.com/stellar/js-stellar-sdk/pull/1766>), "fix: order
  asset issuers and ScVal map keys the way the network does": release
  [17.2.0](https://github.com/stellar/js-stellar-sdk/releases/tag/v17.2.0) refused the valid order, and
  [17.2.1](https://github.com/stellar/js-stellar-sdk/releases/tag/v17.2.1) fixed it.
  `ltk repro js-sdk-1766` reproduces it. The entry also carries the network's own verdict on the other order (below).
- **Entry 3, `cap-0027-muxed-account` (CAP-0027, muxed accounts).** A payment from a muxed source account to a muxed
  destination. The source's 64-bit muxed id is 2^63 + 716, with its top bit set, so a decoder that reads the id as
  signed, or truncates it, shows another value; the destination's id is 716. It is tied to wallet-backend issue 716
  (muxed addresses truncated, <https://github.com/stellar/wallet-backend/issues/716>). Issue 716 was closed with no
  fixing commit linked, so the seed does not reproduce it: entry 3 is the shape only.

How each shape is produced (the transactions it submits, the CAP and protocol it declares, and how its expected
values are read back out of the capture) is written in its recipe, beside the code that runs it:
[`internal/generate/recipes/`](internal/generate/recipes/), one `<entry id>.md` per entry.

### Protocol, lengths and the fallback

The committed corpus was captured at protocol 30, the most recent stable protocol as the pinned quickstart image
defines it: the default of the image's `latest` tag, which carries stellar-core v30.0.0. That is the image's default,
not a statement about the protocol Mainnet runs. Every catalogue record states `protocolVersion: 30` and `protocolFallback: false`: no entry fell back
to protocol 28. If a recipe cannot be made to work at the current protocol, `ltk generate` generates that entry at
protocol 28, on the quickstart image the deciding test used, and its catalogue record states `protocolFallback: true`;
this page then names each such entry here.

The deciding test measured entry 1 at 1,920 bytes and entry 2 at 2,816 bytes, at protocol 28. Those lengths hold at
protocol 28 only. The recipes' integration test (`internal/generate/recipes`), run at protocol 28, gives the same 1,920
and 2,816 bytes, and 2,236 for entry 3. At protocol 30 the committed captures are 1,928, 2,824 and 2,244 bytes. An
entry's length is the one its catalogue record states, and `ltk check` holds each file to it.

### Entry 2: the network's refusal, and the order of its changes

The same pool submitted with its two assets in strkey-text order is refused by the network with
`CHANGE_TRUST_MALFORMED`. Entry 2's catalogue record carries that evidence in `networkRefusalEvidence`: the submitted
envelope, as base64 XDR, and the result code `CHANGE_TRUST_MALFORMED`, as stellar-core reports it. Any XDR tool can
decode the envelope.

An observation, not a defect: entry 2's operation produces eight ledger-entry changes, and stellar-core emitted them in
different orders across runs of the same transactions. In 13 recorded runs (one at protocol 28 and 12 at protocol 30)
there were seven different orders: one order in seven runs, and each of the other six in one run. The committed entry
is one of the single-run orders: in it the account's changes come between the two `USD` trustlines'. Entries 1 and 3
kept one order in every run. The counts are in entry 2's recipe,
[`internal/generate/recipes/cap-0038-pool-share-trustline.md`](internal/generate/recipes/cap-0038-pool-share-trustline.md).
No expected value of entry 2 points into those changes, and `ltk compare` matches the elements of each change list by
ledger key and change kind, so a reordering alone is not a difference.

### The two reproductions

Both reports are committed in `repro/`, as `ltk repro` wrote them; their full text is shown with the commands below.
`ltk repro <target>` writes `repro/<target>.json` in the folder it is run from, so a run in a clone overwrites the
committed report, and the README's tests then fail until the file is restored with `git checkout repro/`.

- **wallet-backend 729.** The tool built wallet-backend from source at `2a0a84ea` (the commit before the fix) and at
  `2c873353` (the fix), each against a fresh TimescaleDB database of its own, on one local network at protocol 28, and
  submitted entry 1's transaction, which landed in ledger 21. The earlier commit did not ingest ledger 21: it failed on
  that ledger five times (`retries: 5`) and exited, and failed the same way after one restart (`restarts: 1`), with the
  error that the signer's 68-byte key payload does not fit the column for 32-byte keys. The fixed commit ingested ledger
  21 and stored the `SIGNER` row. The report's `passed` is `true`. In full, the commit before the fix is
  [`2a0a84ea12523b6c92e278c0c84b443041240095`](https://github.com/stellar/wallet-backend/commit/2a0a84ea12523b6c92e278c0c84b443041240095)
  and the fix, pull request 729's commit, is
  [`2c873353c1f0f3c9b145e25a7d009023ad104a1c`](https://github.com/stellar/wallet-backend/commit/2c873353c1f0f3c9b145e25a7d009023ad104a1c).
  The logs and the store dumps stay in a working folder outside the repository, `ltk-<run id>/` under the system's
  temporary folder; the report records their paths, which for the committed run were under `/tmp`.
- **js-stellar-sdk pull request 1766.** One script, in a Node container, builds entry 2's pool-share trustline with
  entry 2's issuer pair, decodes it and re-encodes it, against `@stellar/stellar-sdk` 17.2.0 and 17.2.1. 17.2.0 failed
  at the build step with "Assets are not in lexicographic order". 17.2.1 passed the build, decode and re-encode steps,
  and the bytes round-tripped identically. The report's `passed` is `true`. The script is
  [`internal/repro/jssdk/repro.mjs`](internal/repro/jssdk/repro.mjs).

### What it does not do

- **It does not replay stored entries into an indexer.** The wallet-backend reproduction runs against a live local
  network, as the deciding test did. Serving stored entries to an indexer is the kit's second milestone.
- **It makes no comparison with stellar-core's golden files.** No command compares anything with a golden file. What
  agreement with them would mean is an open question for Stellar. `ltk compare` compares two generations of this tool
  with each other.
- **It does not claim the bytes reproduce.** Two generations give captures of the same shape but not the same bytes:
  the ledger number, the close time, the hashes, the consensus signature, the fee pool, the entries' last-modified
  ledger and, because each recipe creates its source account in a ledger that differs from run to run, the source's
  sequence number and so the envelope's signature and hash, can all differ. The corpus stores the captured bytes;
  `ltk compare` passes when two generations agree on every value outside that list.
- **It has no server, no database and no web interface.** It is one command-line program. The only database involved
  is wallet-backend's own, inside its reproduction.
- **It generates nothing under a public network's passphrase, and contacts no Stellar network but the local one it
  starts.** Its keys come from fixed seeds, are funded by the local network's friendbot and are worthless outside it.
  It is not offline: the commands that start containers pull their pinned images on first use, and the two
  reproductions fetch source and packages (see [Platforms](#platforms)).

### What the kit's first release would add

The kit's first milestone is the corpus. Beyond this seed, its first release would add:

- a survey of the CAPs, from the protocol of the oldest shape below to the current one, written into a coverage
  manifest that lists every shape and marks the ones not yet covered;
- an entry for each shape the request names: asset pairs whose issuers sort differently by strkey and by XDR bytes
  (entry 2 here); muxed source and recipient accounts (entry 3 here); muxed account and offer ids that decode to a
  different id than they encode; vectors at their maximum length; nested authorization entries; maps whose keys
  collide after conversion; signed-payload signers (entry 1 here); unbounded authorization trees; and trustlines
  created by the Stellar Asset Contract that produce no trustline state change (CAP-0073);
- the contract-based shapes, which need a contract deployed and invoked on the local network;
- synthetic entries, marked as synthetic and carrying the network's refusal as their evidence, for the shapes
  stellar-core will not emit as valid ledger data;
- generation under the test network's and the main network's passphrases, on a local network;
- entries captured, read-only, from the test or main network's history, for shapes that exist there;
- the agreement with stellar-core's golden files, in the reading Stellar chooses.

The kit's later milestones (a replay harness and its adapter interface, a conformance runner with Go, Rust and
JavaScript drivers, a linter for indexer source, a guide for indexer authors, and the findings filed upstream) are
outside the seed.

### How this was made

This repository was written by AI coding agents (Anthropic's Claude models, working in Claude Code) under the direction
of a human spec owner. The agents wrote the program, its tests, the shape recipes, the reproduction script and this
README. The specification was drafted with the same tools under the spec owner's direction. The spec owner decided
what was to be built and the questions the specification left open, and reviewed the results before they were
accepted; other agent sessions, separate from the ones that wrote the code, checked it against the specification.
The committed corpus and the two reports in `repro/` were written by the tool's own commands and were not edited by
hand. After the first commit, a separate agent session, given only this README, ran every `ltk` command here in a
fresh clone; what it did, what it found and what changed because of it are in
[`docs/acceptance-run.md`](docs/acceptance-run.md).

### Licence

Apache License 2.0: see [`LICENSE`](LICENSE).

## Contents

- [Building](#building)
- [Platforms](#platforms)
- [Conventions](#conventions)
- [Synopses](#synopses)
- Commands: [`ltk list`](#ltk-list), [`ltk check`](#ltk-check), [`ltk compare`](#ltk-compare),
  [`ltk generate`](#ltk-generate), [`ltk repro wallet-backend-729`](#ltk-repro-wallet-backend-729),
  [`ltk repro js-sdk-1766`](#ltk-repro-js-sdk-1766)
- [The corpus without the tool](#the-corpus-without-the-tool)
- [Development](#development)
- [What is in this repository](#what-is-in-this-repository)

## Building

You need Go 1.25. From the repository's root:

```sh
go build -o ltk ./cmd/ltk
```

This writes the program `ltk`. On Windows, name it `ltk.exe`:

```sh
go build -o ltk.exe ./cmd/ltk
```

The examples below call it `ltk`: put it on your `PATH`, or call it by its path. The examples are run from the
repository's root.

## Platforms

`ltk list` and `ltk compare` need no container. `ltk generate`, `ltk check` (for its round trip) and `ltk repro` start
containers and need Docker with Linux containers; where that is missing they exit `3` and say what is missing.
Every container, network and volume the tool starts carries the labels `ltk.managed=true` and `ltk.run=<run id>`
and is named `ltk-<run id>-…`, and the tool removes them before it exits. Should a run be killed before it can,
`docker ps -a --filter label=ltk.managed=true` lists the containers left behind.

**What each command downloads.** Each command that starts containers first pulls, from Docker Hub, any of its images
that is not already present. The images are pinned by digest in
[`internal/orchestration/images.go`](internal/orchestration/images.go); the sizes are as `docker images` showed them on
the macOS (arm64) machine that made the runs below:

| Image | Pinned digest | Size | Pulled by |
|---|---|---|---|
| `stellar/quickstart`, current: protocol 30, stellar-core v30.0.0 | `sha256:a61ce821add98adb4f3e3d6e757b2615dd169a2b39c7e30cd47cfa95074f97d9` | 2.17GB | `ltk generate`, `ltk check` |
| `stellar/quickstart`, protocol 28: stellar-core 28.0.1 | `sha256:057b31d5c23d90d58c4637c2888163f1c78afd6c08941339121f4feb9d78b087` | 2.19GB | `ltk generate` (for the protocol-28 fallback), `ltk repro wallet-backend-729` |
| `golang`, 1.25.9-bookworm | `sha256:298734aec230b5f3e8cee450ce6d7eccc39f1797ba548ee90d57e9803030c6c3` | 1.2GB | `ltk repro wallet-backend-729` |
| `timescale/timescaledb`, 2.28.2-pg17 | `sha256:909e2c7577c074517c8936b6d8799294b33f25fb5bad978a2199250665f1ce1a` | 1.81GB | `ltk repro wallet-backend-729` |
| `node`, 22-slim | `sha256:e9f565d32859f0cbfb52125eafe8be39390f24dac302dc3cdf566e8ba3ab21ee` | 349MB | `ltk repro js-sdk-1766` |

Beyond the images, `ltk generate` and `ltk check` fetch nothing. The two reproductions fetch source and packages:

- `ltk repro wallet-backend-729` clones wallet-backend from GitHub (<https://github.com/stellar/wallet-backend>) and
  downloads its Go modules from the Go module proxy (`proxy.golang.org`), verified against the Go checksum database
  (`sum.golang.org`);
- `ltk repro js-sdk-1766` installs `@stellar/stellar-sdk` 17.2.0 and 17.2.1, with their pinned dependencies, from the
  npm registry (`registry.npmjs.org`).

No command contacts a Stellar network other than the local one it starts. `ltk generate` and
`ltk repro wallet-backend-729` run for minutes without output: they print their report when they finish.

The continuous integration ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs on every push and every pull
request, with a token that may only read the repository. It builds the tool and runs its unit tests on Linux, macOS
and Windows, and in each of those jobs runs `ltk list` and `ltk compare` on the committed corpus
and on the comparison fixtures under `testdata/`. Only its Linux job runs `ltk check corpus/`. Its macOS and Windows
jobs install no container engine. The runs shown below for the commands that start containers were made on macOS
(arm64) with Docker Desktop's Linux containers.

## Conventions

- **Standard output carries data only**: a report as JSON, or the list as text. **Standard error** carries at most one
  line, a JSON object `{"code": …, "message": …}`, when the command fails, and nothing when it succeeds.
- **Exit codes:**

  | Exit | `code` on standard error | Meaning |
  |---|---|---|
  | `0` | none: nothing is written | Every check passed, the comparison found only ledger-dependent differences, or the reproduction showed the expected before and after |
  | `1` | `failure` | A check, a generation, a comparison or a reproduction failed |
  | `2` | `usage_error` | Invalid arguments, such as an unknown `--shape <entry id>` |
  | `3` | `environment_missing` | Something the command needs is missing, such as Docker with Linux containers or a pinned image that cannot be pulled |

- **How to read the runs.** A fenced block that starts with `$ ltk` is a run: the command as it was typed, then its
  standard output exactly as the program wrote it, then `[exit N]`, the exit code it gave. Standard error was empty in
  every run shown. Long outputs are folded; open them to read them in full. Where the list output has a TAB, the block
  holds a literal TAB character.
- **What is held by a test.** The runs of `ltk --help`, `ltk list corpus/` and `ltk list corpus/ --json` are run
  again by the unit tests (`cmd/ltk/readme_test.go`) and must match byte for byte; the run of `ltk check corpus/` is
  run again the same way by an integration test. The two `ltk repro` outputs are the committed files in `repro/`, byte
  for byte; a new run of `ltk repro wallet-backend-729` differs from its committed report in the run-specific fields
  its section names. The runs of `ltk generate` and `ltk compare` start a fresh network or read freshly generated
  folders, so their hashes and ledger-dependent values differ from run to run. In a comparison of two generations,
  which pointers differ, and how many, depends on whether the two generations land in the same ledgers; what every
  run shares is the outcome, `passed`.

## Synopses

`ltk --help` prints the invocation and the synopsis of every command; `ltk <command> --help` prints that command's
synopsis.

```text
$ ltk --help
ltk <command> [<subcommand>] [flags] [arguments]
ltk generate --all --out <folder>
ltk generate --shape <entry id> --out <folder>
ltk compare <folder A> <folder B>
ltk repro wallet-backend-729
ltk list <folder> [--json]
ltk check <folder> [--also-go]
ltk repro js-sdk-1766
[exit 0]
```

## ltk list

`ltk list <folder> [--json]` reads a corpus folder's catalogue and prints each entry's id, CAP, protocol version, XDR
type, length and synthetic flag: as tab-separated text, or as JSON with `--json`. It starts no container.

```text
$ ltk list corpus/
id	cap	protocolVersion	xdrType	length	synthetic
cap-0040-signed-payload-signer	CAP-0040	30	LedgerCloseMeta	1928	false
cap-0038-pool-share-trustline	CAP-0038	30	LedgerCloseMeta	2824	false
cap-0027-muxed-account	CAP-0027	30	LedgerCloseMeta	2244	false
[exit 0]
```

```text
$ ltk list corpus/ --json
[
  {
    "id": "cap-0040-signed-payload-signer",
    "cap": "CAP-0040",
    "protocolVersion": 30,
    "xdrType": "LedgerCloseMeta",
    "length": 1928,
    "synthetic": false
  },
  {
    "id": "cap-0038-pool-share-trustline",
    "cap": "CAP-0038",
    "protocolVersion": 30,
    "xdrType": "LedgerCloseMeta",
    "length": 2824,
    "synthetic": false
  },
  {
    "id": "cap-0027-muxed-account",
    "cap": "CAP-0027",
    "protocolVersion": 30,
    "xdrType": "LedgerCloseMeta",
    "length": 2244,
    "synthetic": false
  }
]
[exit 0]
```

## ltk check

`ltk check <folder> [--also-go]` runs the checks in order: the catalogue against the JSON Schema (and the folder's
copy of the schema against the source in `schemas/`), each entry's file against its recorded SHA-256 and length, each
entry through a decode and re-encode in Stellar's Rust XDR tool compared byte for byte, and each expected value read
again from the entry's decoded form. It writes nothing. The round trip runs the Rust XDR tool shipped in the pinned
quickstart image, in a container.

On the committed corpus:

```text
$ ltk check corpus/
{
  "folder": "corpus/",
  "catalogueSchema": {
    "check": "catalogue_schema",
    "passed": true,
    "failures": []
  },
  "entries": [
    {
      "id": "cap-0040-signed-payload-signer",
      "fileIntegrity": {
        "check": "file_integrity",
        "passed": true,
        "sha256": "de6ba37691b5a2b97f56805285c6b3a05b8c53995ea14118e3887125f9095ae0",
        "length": 1928,
        "recordedSha256": "de6ba37691b5a2b97f56805285c6b3a05b8c53995ea14118e3887125f9095ae0",
        "recordedLength": 1928
      },
      "roundTrip": {
        "check": "round_trip",
        "passed": true,
        "bytesCompared": 1928
      },
      "expectedValues": {
        "check": "expected_values",
        "passed": true,
        "mismatches": []
      },
      "goSdkRoundTrip": null
    },
    {
      "id": "cap-0038-pool-share-trustline",
      "fileIntegrity": {
        "check": "file_integrity",
        "passed": true,
        "sha256": "ba7dde01fa8e288aaf0ea75fbd3f416fbb85d67395f07480af228c6ebb10771b",
        "length": 2824,
        "recordedSha256": "ba7dde01fa8e288aaf0ea75fbd3f416fbb85d67395f07480af228c6ebb10771b",
        "recordedLength": 2824
      },
      "roundTrip": {
        "check": "round_trip",
        "passed": true,
        "bytesCompared": 2824
      },
      "expectedValues": {
        "check": "expected_values",
        "passed": true,
        "mismatches": []
      },
      "goSdkRoundTrip": null
    },
    {
      "id": "cap-0027-muxed-account",
      "fileIntegrity": {
        "check": "file_integrity",
        "passed": true,
        "sha256": "fdc7494f3693b7a28e7d284db23c5157d9ec2dbf482c7e48d0e5b3bcdaa8c255",
        "length": 2244,
        "recordedSha256": "fdc7494f3693b7a28e7d284db23c5157d9ec2dbf482c7e48d0e5b3bcdaa8c255",
        "recordedLength": 2244
      },
      "roundTrip": {
        "check": "round_trip",
        "passed": true,
        "bytesCompared": 2244
      },
      "expectedValues": {
        "check": "expected_values",
        "passed": true,
        "mismatches": []
      },
      "goSdkRoundTrip": null
    }
  ],
  "passed": true
}
[exit 0]
```

With `--also-go`, each entry is also decoded and re-encoded with the Go SDK's XDR code and compared with the file
(`goSdkRoundTrip`). This run checked `gen-a`, a folder freshly generated with `ltk generate --all --out gen-a` (flags
may come before or after the folder):

<details>
<summary><code>ltk check --also-go</code> on a fresh generation</summary>

```text
$ ltk check --also-go gen-a
{
  "folder": "gen-a",
  "catalogueSchema": {
    "check": "catalogue_schema",
    "passed": true,
    "failures": []
  },
  "entries": [
    {
      "id": "cap-0040-signed-payload-signer",
      "fileIntegrity": {
        "check": "file_integrity",
        "passed": true,
        "sha256": "1d8fa900043e774c60c514e64076269811ab708dfa77e6191bf15fc2f98739e2",
        "length": 1928,
        "recordedSha256": "1d8fa900043e774c60c514e64076269811ab708dfa77e6191bf15fc2f98739e2",
        "recordedLength": 1928
      },
      "roundTrip": {
        "check": "round_trip",
        "passed": true,
        "bytesCompared": 1928
      },
      "expectedValues": {
        "check": "expected_values",
        "passed": true,
        "mismatches": []
      },
      "goSdkRoundTrip": {
        "check": "go_sdk_round_trip",
        "passed": true,
        "bytesCompared": 1928
      }
    },
    {
      "id": "cap-0038-pool-share-trustline",
      "fileIntegrity": {
        "check": "file_integrity",
        "passed": true,
        "sha256": "f09412c61e71f89b47125c5d188ffe9b5a3e9e633330cd2579a9b6ba98b9e5dc",
        "length": 2824,
        "recordedSha256": "f09412c61e71f89b47125c5d188ffe9b5a3e9e633330cd2579a9b6ba98b9e5dc",
        "recordedLength": 2824
      },
      "roundTrip": {
        "check": "round_trip",
        "passed": true,
        "bytesCompared": 2824
      },
      "expectedValues": {
        "check": "expected_values",
        "passed": true,
        "mismatches": []
      },
      "goSdkRoundTrip": {
        "check": "go_sdk_round_trip",
        "passed": true,
        "bytesCompared": 2824
      }
    },
    {
      "id": "cap-0027-muxed-account",
      "fileIntegrity": {
        "check": "file_integrity",
        "passed": true,
        "sha256": "01670b7998feaec99c4d4118e56d408e37d8730af9e7cfd85085f1460787fee0",
        "length": 2244,
        "recordedSha256": "01670b7998feaec99c4d4118e56d408e37d8730af9e7cfd85085f1460787fee0",
        "recordedLength": 2244
      },
      "roundTrip": {
        "check": "round_trip",
        "passed": true,
        "bytesCompared": 2244
      },
      "expectedValues": {
        "check": "expected_values",
        "passed": true,
        "mismatches": []
      },
      "goSdkRoundTrip": {
        "check": "go_sdk_round_trip",
        "passed": true,
        "bytesCompared": 2244
      }
    }
  ],
  "passed": true
}
[exit 0]
```

</details>

## ltk compare

`ltk compare <folder A> <folder B>` reads each entry's decoded JSON in two corpus folders and compares them value by
value, by JSON pointer. Every difference is attributed to a ledger-dependent field, or to none. The command passes,
and exits `0`, only when every difference is ledger-dependent. It starts no container and never compares bytes.
`ltk compare` reads only each entry's `entry.json`: it does not hash or decode the XDR files, nor check the catalogue
against its schema, so run `ltk check` on a corpus before comparing it.

This run compared two folders generated one after the other with `ltk generate --all --out gen-a` and
`ltk generate --all --out gen-b`. Each entry's `differ` list holds only ledger-dependent fields, so `passed` is `true`.
In this run stellar-core emitted entry 2's changes in the same order in the two folders; had it not, they would be
matched by ledger key and change kind, and the reordering would be no difference. In a comparison of two generations,
which pointers differ, and how many, depends on whether the two generations land in the same ledgers: in this run the entries showed 12, 15 and 26
ledger-dependent differences, and two other generations can show more or fewer. The sample shows one pair of runs;
what holds in every run is `passed`.

<details>
<summary><code>ltk compare</code> of two fresh generations (one pair of runs)</summary>

```text
$ ltk compare gen-a gen-b
{
  "folderA": "gen-a",
  "folderB": "gen-b",
  "entries": [
    {
      "id": "cap-0040-signed-payload-signer",
      "equal": [
        "/v2/ext",
        "/v2/ledger_header/header/ledger_version",
        "/v2/ledger_header/header/scp_value/upgrades",
        "/v2/ledger_header/header/scp_value/ext/signed_ms/lc_value_signature/node_id",
        "/v2/ledger_header/header/tx_set_result_hash",
        "/v2/ledger_header/header/ledger_seq",
        "/v2/ledger_header/header/total_coins",
        "/v2/ledger_header/header/inflation_seq",
        "/v2/ledger_header/header/id_pool",
        "/v2/ledger_header/header/base_fee",
        "/v2/ledger_header/header/base_reserve",
        "/v2/ledger_header/header/max_tx_set_size",
        "/v2/ledger_header/header/skip_list/0",
        "/v2/ledger_header/header/skip_list/1",
        "/v2/ledger_header/header/skip_list/2",
        "/v2/ledger_header/header/skip_list/3",
        "/v2/ledger_header/header/ext",
        "/v2/ledger_header/ext",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/base_fee",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/source_account",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/fee",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/seq_num",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/cond/time/min_time",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/cond/time/max_time",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/memo",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/source_account",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/set_options/inflation_dest",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/set_options/clear_flags",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/set_options/set_flags",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/set_options/master_weight",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/set_options/low_threshold",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/set_options/med_threshold",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/set_options/high_threshold",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/set_options/home_domain",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/set_options/signer/key",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/set_options/signer/weight",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/ext",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/signatures/0/hint",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/signatures/0/signature",
        "/v2/tx_set/v1/phases/1/v1/base_fee",
        "/v2/tx_set/v1/phases/1/v1/execution_stages",
        "/v2/tx_processing/0/ext",
        "/v2/tx_processing/0/result/transaction_hash",
        "/v2/tx_processing/0/result/result/fee_charged",
        "/v2/tx_processing/0/result/result/result/tx_success/0/op_inner/set_options",
        "/v2/tx_processing/0/result/result/ext",
        "/v2/tx_processing/0/fee_processing/0/state/last_modified_ledger_seq",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/account_id",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/balance",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/seq_num",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/num_sub_entries",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/inflation_dest",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/flags",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/home_domain",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/thresholds",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/signers",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/ext",
        "/v2/tx_processing/0/fee_processing/0/state/ext",
        "/v2/tx_processing/0/fee_processing/1/updated/last_modified_ledger_seq",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/account_id",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/balance",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/seq_num",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/num_sub_entries",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/inflation_dest",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/flags",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/home_domain",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/thresholds",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/signers",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/ext",
        "/v2/tx_processing/0/fee_processing/1/updated/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/seq_num",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/num_sub_entries",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/inflation_dest",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/home_domain",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/thresholds",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/signers",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/seq_num",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/num_sub_entries",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/inflation_dest",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/home_domain",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/thresholds",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/signers",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/liabilities/buying",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/liabilities/selling",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/num_sponsored",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/num_sponsoring",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/signer_sponsoring_i_ds",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/ext/v3/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/ext/v3/seq_ledger",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/seq_num",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/num_sub_entries",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/inflation_dest",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/home_domain",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/thresholds",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/signers",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/liabilities/buying",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/liabilities/selling",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/ext/v2/num_sponsored",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/ext/v2/num_sponsoring",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/ext/v2/signer_sponsoring_i_ds",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/ext/v2/ext/v3/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/ext/v2/ext/v3/seq_ledger",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/seq_num",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/num_sub_entries",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/inflation_dest",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/home_domain",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/thresholds",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/signers/0/key",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/signers/0/weight",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/liabilities/buying",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/liabilities/selling",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/ext/v2/num_sponsored",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/ext/v2/num_sponsoring",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/ext/v2/signer_sponsoring_i_ds/0",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/ext/v2/ext/v3/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/ext/v2/ext/v3/seq_ledger",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/events",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_after",
        "/v2/tx_processing/0/tx_apply_processing/v4/soroban_meta",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/stage",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/contract_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/type",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/body/v0/topics/0/symbol",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/body/v0/topics/1/address",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/body/v0/data/i128",
        "/v2/tx_processing/0/tx_apply_processing/v4/diagnostic_events",
        "/v2/tx_processing/0/post_tx_apply_fee_processing",
        "/v2/upgrades_processing",
        "/v2/scp_info",
        "/v2/total_byte_size_of_live_soroban_state",
        "/v2/evicted_keys"
      ],
      "differ": [
        {
          "pointer": "/v2/ledger_header/hash",
          "ledgerDependentField": "ledger_hash",
          "valueA": "76e13f9d232649f4394ddbc26d31ae54020985439208cd8c4a85edfb43053e59",
          "valueB": "eecf5dafc0c028a76607aa118e8b2e38ae0cc5bf26718906f0cd25940e3f7c8e"
        },
        {
          "pointer": "/v2/ledger_header/header/previous_ledger_hash",
          "ledgerDependentField": "previous_ledger_hash",
          "valueA": "29fd357d2b2d6d658d873fbd0996debd27e6f6a9fbfdb831216b4f8109763acd",
          "valueB": "a6a4249ec98d57d96cf8f683b883d890da7a7e408dc7e7f5e9182b526acb1f23"
        },
        {
          "pointer": "/v2/ledger_header/header/scp_value/tx_set_hash",
          "ledgerDependentField": "transaction_set_hash",
          "valueA": "c470d872772270ea5f1a61fc43a4437c3c293a7a481b6e2f871e999881b86da6",
          "valueB": "0b9a3ed68c001a9d2eed62006c8c4e35e06a61bec76601883616a3314804bec9"
        },
        {
          "pointer": "/v2/ledger_header/header/scp_value/close_time",
          "ledgerDependentField": "close_time",
          "valueA": "1794873500",
          "valueB": "1794873530"
        },
        {
          "pointer": "/v2/ledger_header/header/scp_value/ext/signed_ms/close_time_ms",
          "ledgerDependentField": "close_time",
          "valueA": "1794873500030",
          "valueB": "1794873530057"
        },
        {
          "pointer": "/v2/ledger_header/header/scp_value/ext/signed_ms/lc_value_signature/signature",
          "ledgerDependentField": "consensus_signature",
          "valueA": "609f8773c3549d4116c5af3e7a375aee9f1e8e55004a2d4b9c2705c19ba0bddaf7b36d2030beaf55f261979c9d43b6e54b3cbca8e0d9a8e73bbac041c0d1cf0e",
          "valueB": "2fb5e6ad6addc0c15aa5f3d33559b893cccdcdfbdf3d8daf341a3e8a31437a72873766ec1424c6aa54e96e2156d5b3fa1f17c40bef1ba893f9abdb994a64d201"
        },
        {
          "pointer": "/v2/ledger_header/header/bucket_list_hash",
          "ledgerDependentField": "bucket_list_hash",
          "valueA": "8d2ab67e8993a479fd09ab26d78a74b4bc8a4e5999650fe0cd396411db5371a1",
          "valueB": "9cb683c56193693d8f2b70ada519577626d8c23f329dc880a03267e917f43cd0"
        },
        {
          "pointer": "/v2/ledger_header/header/fee_pool",
          "ledgerDependentField": "fee_pool",
          "valueA": "10505887",
          "valueB": "10498680"
        },
        {
          "pointer": "/v2/tx_set/v1/previous_ledger_hash",
          "ledgerDependentField": "previous_ledger_hash",
          "valueA": "29fd357d2b2d6d658d873fbd0996debd27e6f6a9fbfdb831216b4f8109763acd",
          "valueB": "a6a4249ec98d57d96cf8f683b883d890da7a7e408dc7e7f5e9182b526acb1f23"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/ext/v3/seq_time",
          "ledgerDependentField": "close_time",
          "valueA": "1794873500",
          "valueB": "1794873530"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/ext/v2/ext/v3/seq_time",
          "ledgerDependentField": "close_time",
          "valueA": "1794873500",
          "valueB": "1794873530"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/ext/v2/ext/v3/seq_time",
          "ledgerDependentField": "close_time",
          "valueA": "1794873500",
          "valueB": "1794873530"
        }
      ]
    },
    {
      "id": "cap-0038-pool-share-trustline",
      "equal": [
        "/v2/ext",
        "/v2/ledger_header/header/ledger_version",
        "/v2/ledger_header/header/scp_value/upgrades",
        "/v2/ledger_header/header/scp_value/ext/signed_ms/lc_value_signature/node_id",
        "/v2/ledger_header/header/tx_set_result_hash",
        "/v2/ledger_header/header/ledger_seq",
        "/v2/ledger_header/header/total_coins",
        "/v2/ledger_header/header/inflation_seq",
        "/v2/ledger_header/header/id_pool",
        "/v2/ledger_header/header/base_fee",
        "/v2/ledger_header/header/base_reserve",
        "/v2/ledger_header/header/max_tx_set_size",
        "/v2/ledger_header/header/skip_list/0",
        "/v2/ledger_header/header/skip_list/1",
        "/v2/ledger_header/header/skip_list/2",
        "/v2/ledger_header/header/skip_list/3",
        "/v2/ledger_header/header/ext",
        "/v2/ledger_header/ext",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/base_fee",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/source_account",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/fee",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/seq_num",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/cond/time/min_time",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/cond/time/max_time",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/memo",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/source_account",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/change_trust/line/pool_share/liquidity_pool_constant_product/asset_a/credit_alphanum4/asset_code",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/change_trust/line/pool_share/liquidity_pool_constant_product/asset_a/credit_alphanum4/issuer",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/change_trust/line/pool_share/liquidity_pool_constant_product/asset_b/credit_alphanum4/asset_code",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/change_trust/line/pool_share/liquidity_pool_constant_product/asset_b/credit_alphanum4/issuer",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/change_trust/line/pool_share/liquidity_pool_constant_product/fee",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/change_trust/limit",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/ext",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/signatures/0/hint",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/signatures/0/signature",
        "/v2/tx_set/v1/phases/1/v1/base_fee",
        "/v2/tx_set/v1/phases/1/v1/execution_stages",
        "/v2/tx_processing/0/ext",
        "/v2/tx_processing/0/result/transaction_hash",
        "/v2/tx_processing/0/result/result/fee_charged",
        "/v2/tx_processing/0/result/result/result/tx_success/0/op_inner/change_trust",
        "/v2/tx_processing/0/result/result/ext",
        "/v2/tx_processing/0/fee_processing/0/state/last_modified_ledger_seq",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/account_id",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/balance",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/seq_num",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/num_sub_entries",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/inflation_dest",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/flags",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/home_domain",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/thresholds",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/signers",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/ext/v1/liabilities/buying",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/ext/v1/liabilities/selling",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/ext/v1/ext/v2/num_sponsored",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/ext/v1/ext/v2/num_sponsoring",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/ext/v1/ext/v2/signer_sponsoring_i_ds",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/ext/v1/ext/v2/ext/v3/ext",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/ext/v1/ext/v2/ext/v3/seq_ledger",
        "/v2/tx_processing/0/fee_processing/0/state/ext",
        "/v2/tx_processing/0/fee_processing/1/updated/last_modified_ledger_seq",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/account_id",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/balance",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/seq_num",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/num_sub_entries",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/inflation_dest",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/flags",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/home_domain",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/thresholds",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/signers",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/ext/v1/liabilities/buying",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/ext/v1/liabilities/selling",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/ext/v1/ext/v2/num_sponsored",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/ext/v1/ext/v2/num_sponsoring",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/ext/v1/ext/v2/signer_sponsoring_i_ds",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/ext/v1/ext/v2/ext/v3/ext",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/ext/v1/ext/v2/ext/v3/seq_ledger",
        "/v2/tx_processing/0/fee_processing/1/updated/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/seq_num",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/num_sub_entries",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/inflation_dest",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/home_domain",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/thresholds",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/signers",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/ext/v1/liabilities/buying",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/ext/v1/liabilities/selling",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/ext/v1/ext/v2/num_sponsored",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/ext/v1/ext/v2/num_sponsoring",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/ext/v1/ext/v2/signer_sponsoring_i_ds",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/ext/v1/ext/v2/ext/v3/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/ext/v1/ext/v2/ext/v3/seq_ledger",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/seq_num",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/num_sub_entries",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/inflation_dest",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/home_domain",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/thresholds",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/signers",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/liabilities/buying",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/liabilities/selling",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/num_sponsored",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/num_sponsoring",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/signer_sponsoring_i_ds",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/ext/v3/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/ext/v3/seq_ledger",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/created/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/created/data/trustline/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/created/data/trustline/asset/pool_share",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/created/data/trustline/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/created/data/trustline/limit",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/created/data/trustline/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/created/data/trustline/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/created/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/seq_num",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/num_sub_entries",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/inflation_dest",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/home_domain",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/thresholds",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/signers",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/ext/v1/liabilities/buying",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/ext/v1/liabilities/selling",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/ext/v1/ext/v2/num_sponsored",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/ext/v1/ext/v2/num_sponsoring",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/ext/v1/ext/v2/signer_sponsoring_i_ds",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/ext/v1/ext/v2/ext/v3/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/ext/v1/ext/v2/ext/v3/seq_ledger",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/seq_num",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/num_sub_entries",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/inflation_dest",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/home_domain",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/thresholds",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/signers",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/ext/v1/liabilities/buying",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/ext/v1/liabilities/selling",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/ext/v1/ext/v2/num_sponsored",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/ext/v1/ext/v2/num_sponsoring",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/ext/v1/ext/v2/signer_sponsoring_i_ds",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/ext/v1/ext/v2/ext/v3/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/ext/v1/ext/v2/ext/v3/seq_ledger",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/state/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/state/data/trustline/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/state/data/trustline/asset/credit_alphanum4/asset_code",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/state/data/trustline/asset/credit_alphanum4/issuer",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/state/data/trustline/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/state/data/trustline/limit",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/state/data/trustline/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/state/data/trustline/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/state/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/4/updated/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/4/updated/data/trustline/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/4/updated/data/trustline/asset/credit_alphanum4/asset_code",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/4/updated/data/trustline/asset/credit_alphanum4/issuer",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/4/updated/data/trustline/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/4/updated/data/trustline/limit",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/4/updated/data/trustline/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/4/updated/data/trustline/ext/v1/liabilities/buying",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/4/updated/data/trustline/ext/v1/liabilities/selling",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/4/updated/data/trustline/ext/v1/ext/v2/liquidity_pool_use_count",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/4/updated/data/trustline/ext/v1/ext/v2/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/4/updated/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/5/state/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/5/state/data/trustline/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/5/state/data/trustline/asset/credit_alphanum4/asset_code",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/5/state/data/trustline/asset/credit_alphanum4/issuer",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/5/state/data/trustline/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/5/state/data/trustline/limit",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/5/state/data/trustline/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/5/state/data/trustline/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/5/state/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/6/updated/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/6/updated/data/trustline/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/6/updated/data/trustline/asset/credit_alphanum4/asset_code",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/6/updated/data/trustline/asset/credit_alphanum4/issuer",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/6/updated/data/trustline/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/6/updated/data/trustline/limit",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/6/updated/data/trustline/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/6/updated/data/trustline/ext/v1/liabilities/buying",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/6/updated/data/trustline/ext/v1/liabilities/selling",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/6/updated/data/trustline/ext/v1/ext/v2/liquidity_pool_use_count",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/6/updated/data/trustline/ext/v1/ext/v2/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/6/updated/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/7/created/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/7/created/data/liquidity_pool/liquidity_pool_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/7/created/data/liquidity_pool/body/liquidity_pool_constant_product/params/asset_a/credit_alphanum4/asset_code",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/7/created/data/liquidity_pool/body/liquidity_pool_constant_product/params/asset_a/credit_alphanum4/issuer",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/7/created/data/liquidity_pool/body/liquidity_pool_constant_product/params/asset_b/credit_alphanum4/asset_code",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/7/created/data/liquidity_pool/body/liquidity_pool_constant_product/params/asset_b/credit_alphanum4/issuer",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/7/created/data/liquidity_pool/body/liquidity_pool_constant_product/params/fee",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/7/created/data/liquidity_pool/body/liquidity_pool_constant_product/reserve_a",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/7/created/data/liquidity_pool/body/liquidity_pool_constant_product/reserve_b",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/7/created/data/liquidity_pool/body/liquidity_pool_constant_product/total_pool_shares",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/7/created/data/liquidity_pool/body/liquidity_pool_constant_product/pool_shares_trust_line_count",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/7/created/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/events",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_after",
        "/v2/tx_processing/0/tx_apply_processing/v4/soroban_meta",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/stage",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/contract_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/type",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/body/v0/topics/0/symbol",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/body/v0/topics/1/address",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/body/v0/data/i128",
        "/v2/tx_processing/0/tx_apply_processing/v4/diagnostic_events",
        "/v2/tx_processing/0/post_tx_apply_fee_processing",
        "/v2/upgrades_processing",
        "/v2/scp_info",
        "/v2/total_byte_size_of_live_soroban_state",
        "/v2/evicted_keys"
      ],
      "differ": [
        {
          "pointer": "/v2/ledger_header/hash",
          "ledgerDependentField": "ledger_hash",
          "valueA": "33fe63d7c6bc37ed9a3cbed602012e632bbc82210425dae4caacd35ff400fc26",
          "valueB": "25d97f231d370af3ed6e194eaf2688e3477937ef265ed5e4b8edca9e5b2457ca"
        },
        {
          "pointer": "/v2/ledger_header/header/previous_ledger_hash",
          "ledgerDependentField": "previous_ledger_hash",
          "valueA": "4fdddbc54e2a17e93ff359609903afc23ab8ccdc995a260ce80d1b72d108ce78",
          "valueB": "f83a88ca075e01d29b0e60bfe177441655ce1de3bc4122cd2ffd9eac46220c44"
        },
        {
          "pointer": "/v2/ledger_header/header/scp_value/tx_set_hash",
          "ledgerDependentField": "transaction_set_hash",
          "valueA": "1e0f569ca479b5cfeda7a092d7ca2a209eb7bac90af41051779a2f5c34329721",
          "valueB": "d3248dd0a4bbf01d1bafd7659a9b70c16fd972933d90beba0226ef8f13ca26c1"
        },
        {
          "pointer": "/v2/ledger_header/header/scp_value/close_time",
          "ledgerDependentField": "close_time",
          "valueA": "1794873507",
          "valueB": "1794873537"
        },
        {
          "pointer": "/v2/ledger_header/header/scp_value/ext/signed_ms/close_time_ms",
          "ledgerDependentField": "close_time",
          "valueA": "1794873507054",
          "valueB": "1794873537070"
        },
        {
          "pointer": "/v2/ledger_header/header/scp_value/ext/signed_ms/lc_value_signature/signature",
          "ledgerDependentField": "consensus_signature",
          "valueA": "dab16a22a4c1ffe567a2f8ef3d54e829a746cbf1bea9d3e707418633889c5eaa740aaa300c45a40454cf0aa29423c38a2755dff18bd6720e6bdfd4590ee5100b",
          "valueB": "e517f9ace1700ca31a954f084dbe410b9b9f8ba94a348b9a6be5354c2a09a7761490c61f214c6489e5227ed7da035cd52c972a7597432ea6a0c22e2dca2bf700"
        },
        {
          "pointer": "/v2/ledger_header/header/bucket_list_hash",
          "ledgerDependentField": "bucket_list_hash",
          "valueA": "87dece604779b85f6c11a71296df7bd21569b62d3440e27f2acf855079335bc4",
          "valueB": "8fb2e3b2f79847c4649b85f64ec6e15e90efed525b1521e61d7f17984c73295b"
        },
        {
          "pointer": "/v2/ledger_header/header/fee_pool",
          "ledgerDependentField": "fee_pool",
          "valueA": "10506487",
          "valueB": "10499280"
        },
        {
          "pointer": "/v2/tx_set/v1/previous_ledger_hash",
          "ledgerDependentField": "previous_ledger_hash",
          "valueA": "4fdddbc54e2a17e93ff359609903afc23ab8ccdc995a260ce80d1b72d108ce78",
          "valueB": "f83a88ca075e01d29b0e60bfe177441655ce1de3bc4122cd2ffd9eac46220c44"
        },
        {
          "pointer": "/v2/tx_processing/0/fee_processing/0/state/data/account/ext/v1/ext/v2/ext/v3/seq_time",
          "ledgerDependentField": "close_time",
          "valueA": "1794873506",
          "valueB": "1794873536"
        },
        {
          "pointer": "/v2/tx_processing/0/fee_processing/1/updated/data/account/ext/v1/ext/v2/ext/v3/seq_time",
          "ledgerDependentField": "close_time",
          "valueA": "1794873506",
          "valueB": "1794873536"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/ext/v1/ext/v2/ext/v3/seq_time",
          "ledgerDependentField": "close_time",
          "valueA": "1794873506",
          "valueB": "1794873536"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/ext/v3/seq_time",
          "ledgerDependentField": "close_time",
          "valueA": "1794873507",
          "valueB": "1794873537"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/state/data/account/ext/v1/ext/v2/ext/v3/seq_time",
          "ledgerDependentField": "close_time",
          "valueA": "1794873507",
          "valueB": "1794873537"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/updated/data/account/ext/v1/ext/v2/ext/v3/seq_time",
          "ledgerDependentField": "close_time",
          "valueA": "1794873507",
          "valueB": "1794873537"
        }
      ]
    },
    {
      "id": "cap-0027-muxed-account",
      "equal": [
        "/v2/ext",
        "/v2/ledger_header/header/ledger_version",
        "/v2/ledger_header/header/scp_value/upgrades",
        "/v2/ledger_header/header/scp_value/ext/signed_ms/lc_value_signature/node_id",
        "/v2/ledger_header/header/ledger_seq",
        "/v2/ledger_header/header/total_coins",
        "/v2/ledger_header/header/inflation_seq",
        "/v2/ledger_header/header/id_pool",
        "/v2/ledger_header/header/base_fee",
        "/v2/ledger_header/header/base_reserve",
        "/v2/ledger_header/header/max_tx_set_size",
        "/v2/ledger_header/header/skip_list/0",
        "/v2/ledger_header/header/skip_list/1",
        "/v2/ledger_header/header/skip_list/2",
        "/v2/ledger_header/header/skip_list/3",
        "/v2/ledger_header/header/ext",
        "/v2/ledger_header/ext",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/base_fee",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/source_account",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/fee",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/cond/time/min_time",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/cond/time/max_time",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/memo",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/source_account",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/payment/destination",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/payment/asset",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/payment/amount",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/ext",
        "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/signatures/0/hint",
        "/v2/tx_set/v1/phases/1/v1/base_fee",
        "/v2/tx_set/v1/phases/1/v1/execution_stages",
        "/v2/tx_processing/0/ext",
        "/v2/tx_processing/0/result/result/fee_charged",
        "/v2/tx_processing/0/result/result/result/tx_success/0/op_inner/payment",
        "/v2/tx_processing/0/result/result/ext",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/account_id",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/balance",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/num_sub_entries",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/inflation_dest",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/flags",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/home_domain",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/thresholds",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/signers",
        "/v2/tx_processing/0/fee_processing/0/state/data/account/ext",
        "/v2/tx_processing/0/fee_processing/0/state/ext",
        "/v2/tx_processing/0/fee_processing/1/updated/last_modified_ledger_seq",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/account_id",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/balance",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/num_sub_entries",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/inflation_dest",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/flags",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/home_domain",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/thresholds",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/signers",
        "/v2/tx_processing/0/fee_processing/1/updated/data/account/ext",
        "/v2/tx_processing/0/fee_processing/1/updated/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/num_sub_entries",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/inflation_dest",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/home_domain",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/thresholds",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/signers",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/num_sub_entries",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/inflation_dest",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/home_domain",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/thresholds",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/signers",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/liabilities/buying",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/liabilities/selling",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/num_sponsored",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/num_sponsoring",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/signer_sponsoring_i_ds",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/ext/v3/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/ext/v3/seq_ledger",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/num_sub_entries",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/inflation_dest",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/home_domain",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/thresholds",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/signers",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/liabilities/buying",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/liabilities/selling",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/ext/v2/num_sponsored",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/ext/v2/num_sponsoring",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/ext/v2/signer_sponsoring_i_ds",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/ext/v2/ext/v3/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/ext/v2/ext/v3/seq_ledger",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/num_sub_entries",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/inflation_dest",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/home_domain",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/thresholds",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/signers",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/liabilities/buying",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/liabilities/selling",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/ext/v2/num_sponsored",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/ext/v2/num_sponsoring",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/ext/v2/signer_sponsoring_i_ds",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/ext/v2/ext/v3/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/ext/v2/ext/v3/seq_ledger",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/state/data/account/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/state/data/account/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/state/data/account/num_sub_entries",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/state/data/account/inflation_dest",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/state/data/account/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/state/data/account/home_domain",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/state/data/account/thresholds",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/state/data/account/signers",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/state/data/account/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/state/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/updated/last_modified_ledger_seq",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/updated/data/account/account_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/updated/data/account/balance",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/updated/data/account/num_sub_entries",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/updated/data/account/inflation_dest",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/updated/data/account/flags",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/updated/data/account/home_domain",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/updated/data/account/thresholds",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/updated/data/account/signers",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/updated/data/account/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/updated/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/events/0/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/events/0/contract_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/events/0/type",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/events/0/body/v0/topics/0/symbol",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/events/0/body/v0/topics/1/address",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/events/0/body/v0/topics/2/address",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/events/0/body/v0/topics/3/string",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/events/0/body/v0/data/map/0/key/symbol",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/events/0/body/v0/data/map/0/val/i128",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/events/0/body/v0/data/map/1/key/symbol",
        "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/events/0/body/v0/data/map/1/val/u64",
        "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_after",
        "/v2/tx_processing/0/tx_apply_processing/v4/soroban_meta",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/stage",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/ext",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/contract_id",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/type",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/body/v0/topics/0/symbol",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/body/v0/topics/1/address",
        "/v2/tx_processing/0/tx_apply_processing/v4/events/0/event/body/v0/data/i128",
        "/v2/tx_processing/0/tx_apply_processing/v4/diagnostic_events",
        "/v2/tx_processing/0/post_tx_apply_fee_processing",
        "/v2/upgrades_processing",
        "/v2/scp_info",
        "/v2/total_byte_size_of_live_soroban_state",
        "/v2/evicted_keys"
      ],
      "differ": [
        {
          "pointer": "/v2/ledger_header/hash",
          "ledgerDependentField": "ledger_hash",
          "valueA": "c05edb4eac22ea758c71a343a6bcd3dcc76f8da4e6e12c3ba14465bbc6952f95",
          "valueB": "2dc31498b6fdd80d0a4a8fc1c6697b46df9ebc7d0374b8d67c0f22d647f042e5"
        },
        {
          "pointer": "/v2/ledger_header/header/previous_ledger_hash",
          "ledgerDependentField": "previous_ledger_hash",
          "valueA": "1f49fe3245c91b7b1b05b4f1098d377b612651fc0312b37b8ec608db81771f08",
          "valueB": "81e21f923dcb2fb51bc4d9d9265bc809c933ca010f867c0d0505193b0a3cf505"
        },
        {
          "pointer": "/v2/ledger_header/header/scp_value/tx_set_hash",
          "ledgerDependentField": "transaction_set_hash",
          "valueA": "3b8f32b0ce255be9cea96b9a1ed2b5d3ad5353641216cb0375cf14f6f4afb407",
          "valueB": "a27f4547a9c984f90f0bd7bfc34382112409793cadc7a97325415bebf3b7a571"
        },
        {
          "pointer": "/v2/ledger_header/header/scp_value/close_time",
          "ledgerDependentField": "close_time",
          "valueA": "1794873511",
          "valueB": "1794873541"
        },
        {
          "pointer": "/v2/ledger_header/header/scp_value/ext/signed_ms/close_time_ms",
          "ledgerDependentField": "close_time",
          "valueA": "1794873511064",
          "valueB": "1794873541085"
        },
        {
          "pointer": "/v2/ledger_header/header/scp_value/ext/signed_ms/lc_value_signature/signature",
          "ledgerDependentField": "consensus_signature",
          "valueA": "e3fe8daa0ccac025ea0b0dfaa83d168548ae71212fc0ce8871ea5e1883bd7f64a1f61c3bd06974812e8c8c5d5d82d0ac1ec5479c232abe4e18f27733ed19ed00",
          "valueB": "dc122401c227d80b65d6ab7a7981268b7c2b5dbd010c2b0f6141966862b7210d2729b78d2d21e77820ffb6376281f6a3b55968f54cb38f65e9fbce6bebba1d05"
        },
        {
          "pointer": "/v2/ledger_header/header/tx_set_result_hash",
          "ledgerDependentField": "transaction_result_set_hash",
          "valueA": "1ad411956a3de2c0e580c8a8619d02a999e5c603304282b0316708840d092822",
          "valueB": "87b3a85628f5bb1036835ed4cd481640961597e97af4bd639e1be4b3f175a772"
        },
        {
          "pointer": "/v2/ledger_header/header/bucket_list_hash",
          "ledgerDependentField": "bucket_list_hash",
          "valueA": "af85012b9dc6606b4a18bbb3ad48fe4af2714540e4fcf56099ab3f6476ae15ca",
          "valueB": "5c7ee6af8091b0f38fc5d35e4b982e7866a9486e9f654adea9cf7904d57c9668"
        },
        {
          "pointer": "/v2/ledger_header/header/fee_pool",
          "ledgerDependentField": "fee_pool",
          "valueA": "10506787",
          "valueB": "10499580"
        },
        {
          "pointer": "/v2/tx_set/v1/previous_ledger_hash",
          "ledgerDependentField": "previous_ledger_hash",
          "valueA": "1f49fe3245c91b7b1b05b4f1098d377b612651fc0312b37b8ec608db81771f08",
          "valueB": "81e21f923dcb2fb51bc4d9d9265bc809c933ca010f867c0d0505193b0a3cf505"
        },
        {
          "pointer": "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/seq_num",
          "ledgerDependentField": "source_sequence_number",
          "valueA": "107374182401",
          "valueB": "103079215105"
        },
        {
          "pointer": "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/signatures/0/signature",
          "ledgerDependentField": "transaction_signature",
          "valueA": "5ca8d94f19920533f7d1f30fc6501d129d2ecb31812ff8aa2510ca12ec40e35f8cd142a4d4b236e62a6a23d79c7add211a29227999e35cf7582797bd93306c05",
          "valueB": "abf57c84d1ad214775d80f45b75843666634c8eee8989c975b713280f63fda3c870eeaf1b21a4d8b151caf14dcf51f8815f5ea25be188ee61c03d8a0c7df2305"
        },
        {
          "pointer": "/v2/tx_processing/0/result/transaction_hash",
          "ledgerDependentField": "transaction_hash",
          "valueA": "ecdd8743ff85bafe6a49744efc70806b42ae738b8a6d4583bbc4adaaa3731142",
          "valueB": "5e3dad5060984ea34837cdeec9390fb680a55d1817e037857974a29ba61d7032"
        },
        {
          "pointer": "/v2/tx_processing/0/fee_processing/0/state/last_modified_ledger_seq",
          "ledgerDependentField": "last_modified_ledger",
          "valueA": 25,
          "valueB": 24
        },
        {
          "pointer": "/v2/tx_processing/0/fee_processing/0/state/data/account/seq_num",
          "ledgerDependentField": "source_sequence_number",
          "valueA": "107374182400",
          "valueB": "103079215104"
        },
        {
          "pointer": "/v2/tx_processing/0/fee_processing/1/updated/data/account/seq_num",
          "ledgerDependentField": "source_sequence_number",
          "valueA": "107374182400",
          "valueB": "103079215104"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/seq_num",
          "ledgerDependentField": "source_sequence_number",
          "valueA": "107374182400",
          "valueB": "103079215104"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/seq_num",
          "ledgerDependentField": "source_sequence_number",
          "valueA": "107374182401",
          "valueB": "103079215105"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/1/updated/data/account/ext/v1/ext/v2/ext/v3/seq_time",
          "ledgerDependentField": "close_time",
          "valueA": "1794873511",
          "valueB": "1794873541"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/seq_num",
          "ledgerDependentField": "source_sequence_number",
          "valueA": "107374182401",
          "valueB": "103079215105"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/data/account/ext/v1/ext/v2/ext/v3/seq_time",
          "ledgerDependentField": "close_time",
          "valueA": "1794873511",
          "valueB": "1794873541"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/seq_num",
          "ledgerDependentField": "source_sequence_number",
          "valueA": "107374182401",
          "valueB": "103079215105"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/ext/v1/ext/v2/ext/v3/seq_time",
          "ledgerDependentField": "close_time",
          "valueA": "1794873511",
          "valueB": "1794873541"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/state/last_modified_ledger_seq",
          "ledgerDependentField": "last_modified_ledger",
          "valueA": 26,
          "valueB": 25
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/2/state/data/account/seq_num",
          "ledgerDependentField": "source_sequence_number",
          "valueA": "111669149696",
          "valueB": "107374182400"
        },
        {
          "pointer": "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/3/updated/data/account/seq_num",
          "ledgerDependentField": "source_sequence_number",
          "valueA": "111669149696",
          "valueB": "107374182400"
        }
      ]
    }
  ],
  "passed": true
}
[exit 0]
```

</details>

## ltk generate

`ltk generate --all --out <folder>` starts a local stellar-core network (Stellar's quickstart image, core and RPC
only, on a Docker network of its own, every published port bound to 127.0.0.1), funds fixed-seed accounts through
its friendbot, submits each shape's transactions and captures each ledger. It writes each entry's XDR file and decoded
JSON, the catalogue and its copy of the schema into the folder, removes every container, network and volume it
started, then runs the checks of `ltk check` and prints a report of both. It needs no network keys.

`ltk generate --shape <entry id> --out <folder>` generates one entry and writes only that entry and its catalogue
record.

The committed corpus is this run's output, from the repository's root into an empty `corpus/`:

<details>
<summary><code>ltk generate --all --out corpus</code></summary>

```text
$ ltk generate --all --out corpus
{
  "out": "corpus",
  "shapes": [
    {
      "id": "cap-0040-signed-payload-signer",
      "protocolVersion": 30,
      "protocolFallback": false,
      "generated": true,
      "error": null
    },
    {
      "id": "cap-0038-pool-share-trustline",
      "protocolVersion": 30,
      "protocolFallback": false,
      "generated": true,
      "error": null
    },
    {
      "id": "cap-0027-muxed-account",
      "protocolVersion": 30,
      "protocolFallback": false,
      "generated": true,
      "error": null
    }
  ],
  "check": {
    "folder": "corpus",
    "catalogueSchema": {
      "check": "catalogue_schema",
      "passed": true,
      "failures": []
    },
    "entries": [
      {
        "id": "cap-0040-signed-payload-signer",
        "fileIntegrity": {
          "check": "file_integrity",
          "passed": true,
          "sha256": "de6ba37691b5a2b97f56805285c6b3a05b8c53995ea14118e3887125f9095ae0",
          "length": 1928,
          "recordedSha256": "de6ba37691b5a2b97f56805285c6b3a05b8c53995ea14118e3887125f9095ae0",
          "recordedLength": 1928
        },
        "roundTrip": {
          "check": "round_trip",
          "passed": true,
          "bytesCompared": 1928
        },
        "expectedValues": {
          "check": "expected_values",
          "passed": true,
          "mismatches": []
        },
        "goSdkRoundTrip": null
      },
      {
        "id": "cap-0038-pool-share-trustline",
        "fileIntegrity": {
          "check": "file_integrity",
          "passed": true,
          "sha256": "ba7dde01fa8e288aaf0ea75fbd3f416fbb85d67395f07480af228c6ebb10771b",
          "length": 2824,
          "recordedSha256": "ba7dde01fa8e288aaf0ea75fbd3f416fbb85d67395f07480af228c6ebb10771b",
          "recordedLength": 2824
        },
        "roundTrip": {
          "check": "round_trip",
          "passed": true,
          "bytesCompared": 2824
        },
        "expectedValues": {
          "check": "expected_values",
          "passed": true,
          "mismatches": []
        },
        "goSdkRoundTrip": null
      },
      {
        "id": "cap-0027-muxed-account",
        "fileIntegrity": {
          "check": "file_integrity",
          "passed": true,
          "sha256": "fdc7494f3693b7a28e7d284db23c5157d9ec2dbf482c7e48d0e5b3bcdaa8c255",
          "length": 2244,
          "recordedSha256": "fdc7494f3693b7a28e7d284db23c5157d9ec2dbf482c7e48d0e5b3bcdaa8c255",
          "recordedLength": 2244
        },
        "roundTrip": {
          "check": "round_trip",
          "passed": true,
          "bytesCompared": 2244
        },
        "expectedValues": {
          "check": "expected_values",
          "passed": true,
          "mismatches": []
        },
        "goSdkRoundTrip": null
      }
    ],
    "passed": true
  }
}
[exit 0]
```

</details>

This run generated entry 3 alone, into `gen-b`, which `ltk generate --all` had already generated and the compare above
read: it replaced entry 3's files and record, then checked the whole folder.

<details>
<summary><code>ltk generate --shape cap-0027-muxed-account --out &lt;folder&gt;</code></summary>

```text
$ ltk generate --shape cap-0027-muxed-account --out gen-b
{
  "out": "gen-b",
  "shapes": [
    {
      "id": "cap-0027-muxed-account",
      "protocolVersion": 30,
      "protocolFallback": false,
      "generated": true,
      "error": null
    }
  ],
  "check": {
    "folder": "gen-b",
    "catalogueSchema": {
      "check": "catalogue_schema",
      "passed": true,
      "failures": []
    },
    "entries": [
      {
        "id": "cap-0040-signed-payload-signer",
        "fileIntegrity": {
          "check": "file_integrity",
          "passed": true,
          "sha256": "78b7708d18625f61e24b8ae00a7fb80f6c845970a0f1950f15374135656a65f5",
          "length": 1928,
          "recordedSha256": "78b7708d18625f61e24b8ae00a7fb80f6c845970a0f1950f15374135656a65f5",
          "recordedLength": 1928
        },
        "roundTrip": {
          "check": "round_trip",
          "passed": true,
          "bytesCompared": 1928
        },
        "expectedValues": {
          "check": "expected_values",
          "passed": true,
          "mismatches": []
        },
        "goSdkRoundTrip": null
      },
      {
        "id": "cap-0038-pool-share-trustline",
        "fileIntegrity": {
          "check": "file_integrity",
          "passed": true,
          "sha256": "dd41249743dec5a61e3496d6ab8eee3d67065dd8540523e5dc2d535499cfeea2",
          "length": 2824,
          "recordedSha256": "dd41249743dec5a61e3496d6ab8eee3d67065dd8540523e5dc2d535499cfeea2",
          "recordedLength": 2824
        },
        "roundTrip": {
          "check": "round_trip",
          "passed": true,
          "bytesCompared": 2824
        },
        "expectedValues": {
          "check": "expected_values",
          "passed": true,
          "mismatches": []
        },
        "goSdkRoundTrip": null
      },
      {
        "id": "cap-0027-muxed-account",
        "fileIntegrity": {
          "check": "file_integrity",
          "passed": true,
          "sha256": "9dacb045884d24a540c75ad7a1ce23db82aa9d4cbce251323ef0b0ebeaca88ed",
          "length": 2244,
          "recordedSha256": "9dacb045884d24a540c75ad7a1ce23db82aa9d4cbce251323ef0b0ebeaca88ed",
          "recordedLength": 2244
        },
        "roundTrip": {
          "check": "round_trip",
          "passed": true,
          "bytesCompared": 2244
        },
        "expectedValues": {
          "check": "expected_values",
          "passed": true,
          "mismatches": []
        },
        "goSdkRoundTrip": null
      }
    ],
    "passed": true
  }
}
[exit 0]
```

</details>

## ltk repro wallet-backend-729

`ltk repro wallet-backend-729` starts its own local network at protocol 28, the protocol of the commits it builds.
It builds wallet-backend from source at `2a0a84ea` and at `2c873353` in a Go container, with its modules verified
against the Go checksum database, starts each against a fresh TimescaleDB database of its own, and submits entry 1's
transaction once both are running. The earlier commit is judged by its own log: it fails on the ledger five times and
exits, and does the same after one restart. Each run of a commit is also given a safety deadline of 15 minutes; a run
that reaches it without showing that failure is not reproduced, and the command exits `1`. The report is written to
`repro/wallet-backend-729.json` and printed; the logs and store dumps stay in a working folder outside the repository,
`ltk-<run id>/` under the system's temporary folder (this run was made with `TMPDIR=/tmp`).

A second run's report differs from this one only in its run-specific fields: each commit's `ledger`, and the ledger
number inside the earlier commit's `error`; the stored row's `ledger_number`, `to_id` and `operation_id`, which are
derived from the ledger, and its `ingested_at` and `ledger_created_at` timestamps; and each commit's `logPath` and
`storeStatePath`, which name the run's working folder.

<details>
<summary><code>ltk repro wallet-backend-729</code></summary>

```text
$ ltk repro wallet-backend-729
{
  "target": "wallet-backend-729",
  "protocolVersion": 28,
  "commits": [
    {
      "commit": "2a0a84ea",
      "role": "before_fix",
      "ledger": 21,
      "ingested": false,
      "error": "Error ingesting data for ledger 21 (attempt 1/5): persisting ledger data for ledger 21: running atomic function in RunInTransaction: inserting processed data into db for ledger 21: batch inserting state changes: pgx CopyFrom state_changes: ERROR: COPY from stdin failed: converting signer_account_id: converting address to bytes: stellar address PDAOG5GD5ETFKS5GE34T3LRNNGNGK3T5LOOCOL6JRS4MUBIMOZ252AAAAAOWCZLM7IOUJEVHGW4A2BBZO6FO5BUPYKBE57STREZE5CZEYUAAAAHSLA has a 68-byte key payload; the account_id column stores only 32-byte account/contract keys (SQLSTATE 57014)",
      "storedRow": null,
      "retries": 5,
      "restarts": 1,
      "logPath": "/tmp/ltk-e3c9e4be/wallet-backend-729/2a0a84ea/wallet-backend.log",
      "storeStatePath": "/tmp/ltk-e3c9e4be/wallet-backend-729/2a0a84ea/store-state.txt"
    },
    {
      "commit": "2c873353",
      "role": "fix",
      "ledger": 21,
      "ingested": true,
      "error": null,
      "storedRow": {
        "to_id": 90194317312,
        "operation_id": 90194317313,
        "state_change_id": 268435457,
        "state_change_category": "SIGNER",
        "state_change_reason": "ADD",
        "ingested_at": "2026-11-16T23:57:25.745955+00:00",
        "ledger_number": 21,
        "account_id": "\\x30741cf3f8f2aaeaadb4cc94381f3aab3596cad64b5279bc05cdd5af3742ab245e",
        "token_id": null,
        "amount": null,
        "signer_account_id": "\\x78c0e374c3e926554ba626f93dae2d699a656e7d5b9c272fc98cb8ca050c7675dd0000001d61656cfa1d4492a735b80d0439778aee868fc2824efe5389324e8b24c5000000",
        "spender_account_id": null,
        "creator_account_id": null,
        "destination_account_id": null,
        "liquidity_pool_id": null,
        "signer_weight_old": null,
        "signer_weight_new": 1,
        "threshold": null,
        "threshold_old": null,
        "threshold_new": null,
        "trustline_limit_old": null,
        "trustline_limit_new": null,
        "flags": null,
        "data_entry_name": null,
        "key_value": null,
        "to_muxed_id": null,
        "ledger_created_at": "2026-11-16T23:57:24+00:00"
      },
      "retries": 0,
      "restarts": 0,
      "logPath": "/tmp/ltk-e3c9e4be/wallet-backend-729/2c873353/wallet-backend.log",
      "storeStatePath": "/tmp/ltk-e3c9e4be/wallet-backend-729/2c873353/store-state.txt"
    }
  ],
  "passed": true
}
[exit 0]
```

</details>

## ltk repro js-sdk-1766

`ltk repro js-sdk-1766` runs one script in a Node container against `@stellar/stellar-sdk` 17.2.0 and 17.2.1. The
script builds entry 2's pool-share trustline with entry 2's issuer pair, which it takes from the tool's own seed
definitions (it reads no corpus file), then decodes it and re-encodes it. The report is written to
`repro/js-sdk-1766.json` and printed.

```text
$ ltk repro js-sdk-1766
{
  "target": "js-sdk-1766",
  "versions": [
    {
      "version": "17.2.0",
      "role": "before_fix",
      "steps": [
        {
          "step": "build",
          "passed": false,
          "error": "Assets are not in lexicographic order"
        }
      ],
      "roundTripIdentical": false
    },
    {
      "version": "17.2.1",
      "role": "fix",
      "steps": [
        {
          "step": "build",
          "passed": true,
          "error": null
        },
        {
          "step": "decode",
          "passed": true,
          "error": null
        },
        {
          "step": "re_encode",
          "passed": true,
          "error": null
        }
      ],
      "roundTripIdentical": true
    }
  ],
  "passed": true
}
[exit 0]
```

## The corpus without the tool

A corpus folder is readable from any language:

| File | What it is |
|---|---|
| `catalogue.json` | The catalogue: for each entry its `id`, `cap`, `protocolVersion`, `protocolFallback`, `source`, `synthetic`, `expectedDecodedValues`, `filePath`, `xdrType`, `sha256`, `length`, and on entry 2 `networkRefusalEvidence` |
| `catalogue.schema.json` | A copy of the catalogue's JSON Schema; its one source is [`schemas/catalogue.schema.json`](schemas/catalogue.schema.json), and a test holds the two equal |
| `<entry id>/entry.xdr` | The captured bytes, at the catalogue's `filePath` |
| `<entry id>/entry.json` | The same bytes decoded to JSON by Stellar's Rust XDR tool |

Any hashing tool confirms a file's `sha256` and `length`. Each expected value is a JSON pointer into the entry's
decoded JSON and the value found there. The values are in two groups: `reproducedByEveryRun`, the shape's own values
(the operation, the signer and its payload, the assets and their order, the result), and `ledgerDependent`, the values
that differ from run to run, each with the ledger-dependent field it belongs to.

## Development

The unit tests need no Docker and no network, and run on Linux, macOS and Windows:

```sh
go test ./...
```

They hold the README to the committed reports in `repro/`. `ltk repro <target>` writes `repro/<target>.json` in the
folder it is run from, so a run in a clone overwrites the committed report and these tests fail until it is restored
with `git checkout repro/`.

The integration tests need Docker with Linux containers and network access to the image registry, GitHub, the Go
module proxy and checksum database, and the npm registry. They are selected by the `integration` build tag. On a
Linux or macOS machine with Go and Docker:

```sh
go test -tags integration -p 1 -timeout 120m ./cmd/ltk/ ./internal/orchestrator/ ./internal/orchestration/ ./internal/generate/... ./internal/repro/...
```

## What is in this repository

| Path | What it holds |
|---|---|
| `cmd/`, `internal/` | The Go source of `ltk` and its tests. The two reproductions are under `internal/repro/`, with the SDK reproduction's script and the package files it needs; the shape recipes are under `internal/generate/recipes/` |
| `schemas/` | The catalogue's JSON Schema, and the Go file that embeds it |
| `corpus/` | The three entries, the catalogue and its copy of the schema, as `ltk generate --all --out corpus` wrote them: committed data, not source |
| `repro/` | The two reproductions' last reports, and nothing else |
| `testdata/` | Small corpora for the tests, some of them broken on purpose, and the comparison fixtures |
| `docs/` | The acceptance run's document: what a separate agent session did with this README, what it found and what changed |
| `.github/` | The continuous integration workflow |
| `README.md`, `LICENSE`, `go.mod`, `go.sum` | This file, the licence, and the Go module files |
