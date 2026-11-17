# Recipe: `cap-0040-signed-payload-signer`

Entry 1 of the seed corpus. The code is `signed_payload.go` beside this file; the
steps every recipe shares are in `submit.go`, the pointers in `intended.go`.

| | |
|---|---|
| Entry id | `cap-0040-signed-payload-signer` |
| CAP | `CAP-0040` (signed-payload signers) |
| Shape | A `SetOptions` that adds a signed-payload signer with a 29-byte payload, weight 1, so that the XDR carries padding |
| XDR type of the file | `LedgerCloseMeta`: the ledger that holds the shape's transaction |
| Synthetic | No: captured from a local stellar-core network |
| Protocol it declares | The most recent stable protocol: `CurrentProtocol` (30), on the current quickstart image pinned in `internal/orchestration/images.go`. If the recipe cannot work there, the protocol selector (`internal/generate/protocol.go`) generates it at protocol 28 and the record says `protocolFallback: true` |

The catalogue's `protocolVersion` is read from the captured ledger header (`/v2/ledger_header/header/ledger_version`),
never from the protocol that was asked for.

## Accounts

Every key is `keypair.FromRawSeed(sha256("ltk/cap-0040-signed-payload-signer/<role>"))` (`internal/model/seeds.go`).
These keys are throwaway keys of the local network, derived from public text; they are the only keys the tool holds.

| Role | Public key | Use |
|---|---|---|
| `source` | `GB2BZ47Y6KVOVLNUZSKDQHZ2VM2ZNSWWJNJHTPAFZXK26N2CVMSF4HNJ` | The transaction's source; the account that gains the signer |
| `signer` | the ed25519 key inside the signer below | The key the signed payload belongs to; never funded |

The payload is the first 29 bytes of `sha256("ltk/cap-0040-signed-payload-signer/payload")`. The signer is the CAP-40
`P…` strkey of the `signer` role's key with that payload:
`PDAOG5GD5ETFKS5GE34T3LRNNGNGK3T5LOOCOL6JRS4MUBIMOZ252AAAAAOWCZLM7IOUJEVHGW4A2BBZO6FO5BUPYKBE57STREZE5CZEYUAAAAHSLA`.

## What it submits

1. **Funding.** The local friendbot creates and funds `source` (`GET <friendbot>?addr=…`). The recipe then reads the
   account with RPC `getLedgerEntries` until RPC serves it, and takes its sequence number from it.
2. **The shape's transaction**, in a later ledger than the funding, so it is alone in its ledger:
   - source `source`, sequence number the account's plus one, base fee 100 stroops, no time bounds, no memo;
   - one operation, `SetOptions` with `signer = { key: <the P… signer above>, weight: 1 }`;
   - signed by `source`, submitted with RPC `sendTransaction`, then `getTransaction` is polled until it is no longer
     `NOT_FOUND`. It must be `SUCCESS`.
3. **The capture.** RPC `getLedgers` with `startLedger` the transaction's ledger and limit 1. Its `metadataXdr`,
   base64-decoded, is the entry's bytes. The recipe checks that it is that ledger and that the ledger holds exactly
   one transaction, the shape's.

A refused transaction or a `getTransaction` other than `SUCCESS` is a shape that "cannot work" at that protocol; the
selector then tries protocol 28.

## How the expected values are read back

The pipeline decodes the captured bytes with Stellar's Rust XDR tool, shipped in the quickstart image:
`stellar-xdr decode --type LedgerCloseMeta --input single --output json-formatted`. That JSON is written beside the XDR
file as `entry.json`. Each expected value is the value at its JSON pointer in that document. The grouping table
(`internal/model/grouping.go`) decides which group a pointer belongs to (`catalogue.SplitExpectedValues`). At
generation, the reproduced values must equal the recipe's `Intended()` exactly, pointer for pointer; otherwise the
shape cannot work at that protocol. `ltk check` reads every value back the same way.

Every pointer below starts with the transaction set's first transaction,
`T = /v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx`, or with its processing result,
`P = /v2/tx_processing/0`.

### Reproduced by every run (`reproducedByEveryRun`)

| JSON pointer | Value | What it is |
|---|---|---|
| `T/tx/source_account` | `"GB2BZ47Y6KVOVLNUZSKDQHZ2VM2ZNSWWJNJHTPAFZXK26N2CVMSF4HNJ"` | The transaction's source |
| `T/tx/operations/0/body/set_options/signer/key` | `"PDAOG5GD…HSLA"` (the signer above) | The signer key and its 29-byte payload |
| `T/tx/operations/0/body/set_options/signer/weight` | `1` | The signer's weight |
| `P/result/result/result/tx_success/0/op_inner/set_options` | `"success"` | The operation's result |
| `P/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/signers/0/key` | the signer above | The signer in the account's new state |

The last pointer names the operation's second change, the account's `updated` state. In every capture so far the
operation's two changes came in one order: the account's `state`, then its `updated`.

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

## As generated

The runs of `ltk generate --all` on a local network, made while the tool was built, generated this entry at
protocol 30, with `protocolFallback: false`. The file is a `LedgerCloseMeta` v2 of 1928 bytes, whose transaction meta
is v4. Its sha256 differs from run to run, because the ledger-dependent values do.
