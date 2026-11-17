# Recipe: `cap-0038-pool-share-trustline`

Entry 2 of the seed corpus. The code is `pool_share.go` beside this file;
the steps every recipe shares are in `submit.go`, the pointers in `intended.go`.

| | |
|---|---|
| Entry id | `cap-0038-pool-share-trustline` |
| CAP | `CAP-0038` (automated market makers: liquidity pools and pool-share trustlines) |
| Shape | A pool-share `ChangeTrust` whose two issuers sort one way as strkey text and the other way as key bytes, with the network's refusal of the text order recorded as evidence |
| XDR type of the file | `LedgerCloseMeta`: the ledger that holds the shape's transaction |
| Synthetic | No: captured from a local stellar-core network |
| Protocol it declares | The most recent stable protocol: `CurrentProtocol` (30), on the current quickstart image pinned in `internal/orchestration/images.go`. If the recipe cannot work there, the protocol selector (`internal/generate/protocol.go`) generates it at protocol 28 and the record says `protocolFallback: true` |

The catalogue's `protocolVersion` is read from the captured ledger header (`/v2/ledger_header/header/ledger_version`),
never from the protocol that was asked for.

## Accounts

Every key is `keypair.FromRawSeed(sha256("ltk/cap-0038-pool-share-trustline/<role>"))` (`internal/model/seeds.go`).
These keys are throwaway keys of the local network, derived from public text; they are the only keys the tool holds.

| Role | Public key | Use |
|---|---|---|
| `source` | `GB5W4EM3JCA56WVTR4ZTLMXEF3RUNAQSWLTP3E3MSXTMZEURRF6GKTKB` | The holder of the trustlines; the transactions' source |
| `issuer-6` | `GCG2JCL34VF4QCBY7EDPFQY54QAQ7QK62SB564FXQDIAL25LE5KCPEX4` | Issuer of asset A: lower as key bytes, higher as strkey text |
| `issuer-3` | `GC7P7N7UYLS63YRQTZDAWBGZJ4F5EIWCBIJADQT4RSCXO6DZOORVFUZC` | Issuer of asset B: higher as key bytes, lower as strkey text |

The two issuer roles are the first pair of the deterministic search over `issuer-<n>` whose keys sort in opposite
orders as text and as bytes. `internal/model` publishes the pair. The SDK reproduction `repro.mjs` builds its pool
from the same pair. At start-up the recipe asserts the opposite orders, and that its keys are the published pair;
generation fails otherwise.

Both assets have the code `USD`. The pool is the constant-product pool of the two assets in key-byte order, with fee
30 basis points, the only fee the protocol allows.

## What it submits

1. **Funding.** The local friendbot creates and funds `issuer-6`, `issuer-3` and `source`, in that order. After each,
   the recipe reads the account with RPC `getLedgerEntries` until RPC serves it.
2. **Trusting both assets.** One transaction from `source` with two `ChangeTrust` operations, `USD:issuer-6` and
   `USD:issuer-3`. A pool-share trustline requires them. It must succeed.
3. **The shape's transaction**, in a later ledger, so it is alone in its ledger:
   - source `source`, next sequence number, base fee 100 stroops, no time bounds, no memo;
   - one `ChangeTrust` whose line is the pool share of `{ asset_a: USD:issuer-6, asset_b: USD:issuer-3, fee: 30 }`,
     the assets in key-byte order (the protocol's order), with the default (maximum) limit;
   - signed by `source`, submitted with RPC `sendTransaction`; `getTransaction` is polled until it is no longer
     `NOT_FOUND`, and it must be `SUCCESS`.
4. **The capture.** RPC `getLedgers` of that ledger, limit 1. Its `metadataXdr`, base64-decoded, is the entry's bytes.
   The recipe checks that it is that ledger and that the ledger holds exactly one transaction, the shape's.
5. **The refusal (the entry's evidence).** The same pool with its assets in strkey-text order: `asset_a` is
   `USD:issuer-3`, `asset_b` is `USD:issuer-6`. Go's SDK refuses to build that order, so the recipe builds the
   key-byte form with the next sequence number, swaps the two assets in the envelope's XDR, then hashes and signs it
   again with `source`. It submits it with `sendTransaction`. The network refuses it, at `sendTransaction` (status
   `ERROR`, with its `errorResultXdr`, as in the deciding test, the feasibility run made before this tool, and in every
   run of the recipe so far) or at `getTransaction`
   (status `FAILED`). The envelope and the network's `TransactionResult` go to the Refusal evidence recorder
   (`internal/catalogue/refusal.go`). The recorder keeps the envelope verbatim. It accepts only a failed transaction
   whose one `ChangeTrust` result is `CHANGE_TRUST_MALFORMED`. The catalogue record carries
   `networkRefusalEvidence: { submittedEnvelopeXdr, resultCode: "CHANGE_TRUST_MALFORMED" }`.

"Cannot work" at a protocol, so that the selector tries protocol 28:
- the trust transaction or the shape's transaction is refused, or not `SUCCESS`;
- a reproduced value differs from `Intended()`;
- the text-order form is accepted, or refused with another result. The refusal's result code is a value the entry
  records, so in either case the intended value is not produced.

## How the expected values are read back

The pipeline decodes the captured bytes with Stellar's Rust XDR tool, shipped in the quickstart image:
`stellar-xdr decode --type LedgerCloseMeta --input single --output json-formatted`. That JSON is written beside the XDR
file as `entry.json`. Each expected value is the value at its JSON pointer in that document. The grouping table
(`internal/model/grouping.go`) decides which group a pointer belongs to (`catalogue.SplitExpectedValues`). At
generation, the reproduced values must equal the recipe's `Intended()` exactly, pointer for pointer. `ltk check` reads
every value back the same way.

Every pointer below starts with the transaction set's first transaction,
`T = /v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx`, or with its processing result,
`P = /v2/tx_processing/0`. Write `L = T/tx/operations/0/body/change_trust/line/pool_share/liquidity_pool_constant_product`.

### Reproduced by every run (`reproducedByEveryRun`)

| JSON pointer | Value | What it is |
|---|---|---|
| `T/tx/source_account` | `"GB5W4EM3JCA56WVTR4ZTLMXEF3RUNAQSWLTP3E3MSXTMZEURRF6GKTKB"` | The holder |
| `L/asset_a/credit_alphanum4/asset_code` | `"USD"` | Asset A's code |
| `L/asset_a/credit_alphanum4/issuer` | `"GCG2JCL34VF4QCBY7EDPFQY54QAQ7QK62SB564FXQDIAL25LE5KCPEX4"` | Asset A's issuer: first in key-byte order |
| `L/asset_b/credit_alphanum4/asset_code` | `"USD"` | Asset B's code |
| `L/asset_b/credit_alphanum4/issuer` | `"GC7P7N7UYLS63YRQTZDAWBGZJ4F5EIWCBIJADQT4RSCXO6DZOORVFUZC"` | Asset B's issuer: second in key-byte order |
| `L/fee` | `30` | The pool's fee |
| `P/result/result/result/tx_success/0/op_inner/change_trust` | `"success"` | The operation's result |

The pool id of the created trustline is not an expected value. It follows from the two assets and the fee, which are
expected values. The refusal evidence is recorded in its own field of the record, not as a pointer into the capture;
`ltk check` validates it, and holds that only this entry carries it.

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

## Observation: the order of the operation's changes

The shape's operation produces eight ledger-entry changes:
- the pool-share trustline created;
- the holder's account, as `state` then `updated`;
- each `USD` trustline, as `state` then `updated`;
- the liquidity pool created.

Across runs of the same transactions, stellar-core emitted these eight changes in different orders. This is recorded
as an observation, with the run counts. It is not a defect in stellar-core.

| Runs | Protocol | Order of `P/tx_apply_processing/v4/operations/0/changes` (c created, s state, u updated) |
|---|---|---|
| 7 | 28 (1), 30 (6) | c trustline:pool, s/u account, s/u trustline:GCG2, s/u trustline:GC7P, c liquidity_pool |
| 1 | 30 | s/u trustline:GCG2, s/u trustline:GC7P, c trustline:pool, s/u account, c liquidity_pool |
| 1 | 30 | s/u account, s/u trustline:GCG2, s/u trustline:GC7P, c trustline:pool, c liquidity_pool |
| 1 | 30 | c trustline:pool, s/u trustline:GCG2, s/u trustline:GC7P, s/u account, c liquidity_pool |
| 1 | 30 | s/u account, c trustline:pool, s/u trustline:GCG2, s/u trustline:GC7P, c liquidity_pool |
| 1 | 30 | c trustline:pool, s/u account, s/u trustline:GCG2, c liquidity_pool, s/u trustline:GC7P |
| 1 | 30 | c trustline:pool, s/u trustline:GCG2, s/u account, s/u trustline:GC7P, c liquidity_pool |

The counts cover 13 runs, with seven different orders:
- seven runs of the recipe alone on a local network, made while the tool was built: four orders;
- two later runs of `ltk generate --all`: the first had the first order, and the second the fifth;
- the run that wrote an earlier corpus: the sixth order, in which the liquidity pool is created between the two `USD`
  trustlines and not last;
- the two runs of `ltk generate --all` whose folders the README compares (`gen-a` and `gen-b`): the first order, both;
- the run that wrote the committed `corpus/`: the seventh order, in which the account's changes come between the two
  `USD` trustlines'. The committed `corpus/cap-0038-pool-share-trustline/entry.json` shows it.

Entries 1 and 3 kept one order in every run.

Because of this, no expected value of this entry points into the operation's changes. `ltk compare` matches the
elements of each change list by ledger key and change kind, as a multiset, so a reordering alone is no difference.

## As generated

The runs of `ltk generate --all` on a local network, made while the tool was built, generated this entry at
protocol 30, with `protocolFallback: false`. The file is a `LedgerCloseMeta` v2 of 2824 bytes, whose transaction meta
is v4. The network refused the text-order form at `sendTransaction`, with `CHANGE_TRUST_MALFORMED`.
