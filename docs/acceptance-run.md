# The acceptance run

Every `ltk` command the README shows ran in a fresh clone of this repository's first commit (`064c5fe`), exited `0`
and gave the output the README describes. The run also found problems in the README and one unreliable unit test;
the changes that followed are in commit `4db4509`. Other findings are known and remain open; they are listed below.

The run was made by a separate AI agent session that was given only the README. The reader was an AI agent session,
not a human tester: no person performed these steps.

## What was done

- A fresh clone, and every command the README shows, run as the README shows it.
- The mistakes a user makes: missing and wrong arguments, unknown commands and flags, paths that do not exist or are
  not a corpus, and no Docker.
- Eight tampered copies of the corpus, each checked with `ltk check` and compared with `ltk compare`.
- An `ltk generate` interrupted while its local network was up.
- The tool's Docker containers, networks and volumes listed before and after each command.

It ran on macOS (arm64) with Docker Desktop, with the pinned images already present. The binary was built with Go in a
container.

## What it showed

- Every `ltk` command the README shows ran and exited `0` with the output the README describes. `ltk --help`,
  `ltk list corpus/`, `ltk list corpus/ --json` and `ltk check corpus/` matched the README byte for byte, and so did
  the js-stellar-sdk report.
- The wallet-backend report differed from the committed one only in the run-specific fields the README names.
- Both reproductions showed the bug before the fix and its absence after it.
- The README's unit-test command, `go test ./...`, passed on a clean clone. Run after `ltk repro wallet-backend-729`
  had rewritten the committed report, it failed: the first finding below.
- `ltk check` caught all eight tampered copies, and named the entry and the problem each time.
- After every command, the interrupted one included, no container, network or volume of the tool was left.
- Decoding the committed files confirmed the first page's description of the corpus's three entries: their ids,
  protocol, type and lengths, and the property each one demonstrates. The upstream facts the page cites were not
  checked (see below).

## What it found, and what changed

Each of these was changed in commit `4db4509`:

- Running `ltk repro` in a clone rewrites the committed report, after which the README's tests fail. The README now
  says so, and how to restore the file.
- The compare sample read as a fixed set of differences. The README now says that which pointers differ, and how
  many, depends on the ledgers the two runs land in.
- The README's exit-code names differed from what the tool prints. They are now the tool's own, and a test holds them.
- The labels the tool puts on its Docker objects were not documented. They are now named under Platforms.
- The opening called the entries "ledger entries", and the order of a pool's assets was stated too simply. Both are
  corrected.
- One unit test failed once under load: its fake clock let the outcome depend on scheduling. It is fixed.

## What it found that this seed does not change

These are known and open:

- `ltk compare` passes on a corpus whose XDR file is damaged or whose catalogue fails its schema, because it reads
  only each entry's `entry.json`. Run `ltk check` first, as the README now says.
- The same problem gives different exit codes from different commands.
- `ltk generate` writes into a folder that already holds files, without a warning.
- The check report on standard output does not carry the reason a round trip failed; the error line does.
- `--help` gives the synopses only.
- `ltk check` needs Docker even for its checks that need no container.
- The long commands print nothing until they finish.

## What it did not cover

- a host Go toolchain;
- first-time image pulls;
- hands-on use on Linux and Windows (the continuous integration builds and tests there, and runs `ltk check corpus/`
  on Linux);
- Intel Macs, and container engines other than Docker Desktop;
- the integration-test command;
- the protocol-28 fallback path, which was never triggered;
- the reproductions without network access;
- the upstream facts: no web request was made.
