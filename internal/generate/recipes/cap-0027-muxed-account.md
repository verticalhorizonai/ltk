# Recipe: `cap-0027-muxed-account`

Entry 3 of the seed corpus. The code is `muxed.go` beside this file; the steps every recipe shares are in `submit.go`,
the pointers in `intended.go`. Unlike entries 1 and 2, this shape was not run before the tool was written. Its first
runs were the recipe's own, on a local network; the pointers below are those of their decodings, and of the runs of
`ltk generate` that followed.

| | |
|---|---|
| Entry id | `cap-0027-muxed-account` |
| CAP | `CAP-0027` (muxed accounts) |
| Shape | A payment from a muxed source account to a muxed destination |
| XDR type of the file | `LedgerCloseMeta`: the ledger that holds the shape's transaction |
| Synthetic | No: captured from a local stellar-core network |
| Protocol it declares | The most recent stable protocol: `CurrentProtocol` (30), on the current quickstart image pinned in `internal/orchestration/images.go`. If the recipe cannot work there, the protocol selector (`internal/generate/protocol.go`) generates it at protocol 28 and the record says `protocolFallback: true` |

The catalogue's `protocolVersion` is read from the captured ledger header (`/v2/ledger_header/header/ledger_version`),
never from the protocol that was asked for.

## Accounts

Every key is `keypair.FromRawSeed(sha256("ltk/cap-0027-muxed-account/<role>"))` (`internal/model/seeds.go`). These
keys are throwaway keys of the local network, derived from public text; they are the only keys the tool holds.

| Role | Underlying account | Muxed id | Muxed address (`M…`) |
|---|---|---|---|
| `source` | `GCKOWD5UOOQSUQVZET77IP257VIHREOKEYYDT2YTICIX7QOVPJYZW4UQ` | 2^63 + 716 = 9223372036854776524 | `MCKOWD5UOOQSUQVZET77IP257VIHREOKEYYDT2YTICIX7QOVPJYZXAAAAAAAAAACZRJWK` |
| `destination` | `GD2K4NVPX57MX3R2XXODFNMRMOUUTSL5MEBDAANMVF5RNF6X4FOBWZDZ` | 716 | `MD2K4NVPX57MX3R2XXODFNMRMOUUTSL5MEBDAANMVF5RNF6X4FOBWAAAAAAAAAACZS6CO` |

The source's muxed id has its top bit set, so a decoder that reads the 64-bit id as signed, or truncates it, shows
another value. 716 is the number of the wallet-backend issue the shape is tied to.

## What it submits

1. **Funding.** The local friendbot creates and funds the two underlying accounts, `source` then `destination`. After
   each, the recipe reads the account with RPC `getLedgerEntries` until RPC serves it. The source's sequence number is
   read from its underlying `G…` account.
2. **The shape's transaction**, in a later ledger, so it is alone in its ledger:
   - transaction source: the muxed `source` address; next sequence number; base fee 100 stroops; no time bounds; no
     memo;
   - one `Payment` of 10 XLM (100000000 stroops), native asset, to the muxed `destination` address;
   - signed by the `source` key, submitted with RPC `sendTransaction`; `getTransaction` is polled until it is no longer
     `NOT_FOUND`, and it must be `SUCCESS`.
3. **The capture.** RPC `getLedgers` of that ledger, limit 1. Its `metadataXdr`, base64-decoded, is the entry's bytes.
   The recipe checks that it is that ledger and that the ledger holds exactly one transaction, the shape's.

A refused transaction or a `getTransaction` other than `SUCCESS` is a shape that "cannot work" at that protocol.
The selector then tries protocol 28.

## How the expected values are read back

The pipeline decodes the captured bytes with Stellar's Rust XDR tool, shipped in the quickstart image:
`stellar-xdr decode --type LedgerCloseMeta --input single --output json-formatted`. That JSON is written beside the XDR
file as `entry.json`. Each expected value is the value at its JSON pointer in that document. The grouping table
(`internal/model/grouping.go`) decides which group a pointer belongs to (`catalogue.SplitExpectedValues`). At
generation, the reproduced values must equal the recipe's `Intended()` exactly, pointer for pointer. `ltk check` reads
every value back the same way.

The Rust tool writes a muxed account as its `M…` strkey. It writes a 64-bit amount as a decimal string.

Every pointer below starts with the transaction set's first transaction,
`T = /v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx`, or with its processing result,
`P = /v2/tx_processing/0`.

### Reproduced by every run (`reproducedByEveryRun`)

| JSON pointer | Value | What it is |
|---|---|---|
| `T/tx/source_account` | `"MCKOWD5UOOQSUQVZET77IP257VIHREOKEYYDT2YTICIX7QOVPJYZXAAAAAAAAAACZRJWK"` | The muxed source |
| `T/tx/operations/0/body/payment/destination` | `"MD2K4NVPX57MX3R2XXODFNMRMOUUTSL5MEBDAANMVF5RNF6X4FOBWAAAAAAAAAACZS6CO"` | The muxed destination |
| `T/tx/operations/0/body/payment/asset` | `"native"` | The asset |
| `T/tx/operations/0/body/payment/amount` | `"100000000"` | The amount in stroops |
| `P/result/result/result/tx_success/0/op_inner/payment` | `"success"` | The operation's result |

### Dependent on the ledger (`ledgerDependent`)

These values differ from run to run. They are recorded with their field, and `ltk compare` never fails on them:

- the header's `ledger_seq` (`ledger_number`), `close_time` and, from protocol 30, `close_time_ms` (`close_time`),
  `previous_ledger_hash`, `bucket_list_hash`, `scp_value/tx_set_hash` (`transaction_set_hash`), the consensus signature
  (`consensus_signature`), `fee_pool`, the header's own `hash` (`ledger_hash`) and `tx_set_result_hash`
  (`transaction_result_set_hash`); the transaction set's `previous_ledger_hash`;
- each of the four elements of the header's `skip_list` (`bucket_list_hash`): stellar-core writes the bucket-list hash
  of an earlier ledger into them from ledger 50 on, and before that they are zero;
- the envelope's `seq_num` and every account change's `seq_num` (`source_sequence_number`), the envelope's signature
  (`transaction_signature`), the result's `transaction_hash`;
- every change's `last_modified_ledger_seq` (`last_modified_ledger`), and an account's `seq_ledger` and `seq_time`
  (`ledger_number`, `close_time`).

The operation's four changes (each account's `state`, then its `updated`, source first) came in one order in every run
so far.

## As generated

The runs of `ltk generate --all` and of `ltk generate --shape cap-0027-muxed-account` on a local network, made while
the tool was built, generated this entry at protocol 30, with
`protocolFallback: false`. The file is a `LedgerCloseMeta` v2 of 2244 bytes, whose transaction meta is v4.
