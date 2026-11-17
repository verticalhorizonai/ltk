package recipes

// What every recipe does with the network, as the deciding test, the feasibility run made before this tool, did: fund
// fixed-seed accounts through the friendbot, read a source account's sequence number with getLedgerEntries, build and
// sign a transaction with txnbuild, submit it with sendTransaction, poll getTransaction until it is applied, and
// capture its ledger with getLedgers (limit 1). A session is one Run's view of its Network; it is created per call,
// so a recipe holds nothing between calls.

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/verticalhorizonai/ltk/internal/generate/xdrcodec"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// session is one Run against a Network.
type session struct {
	net     Network
	poll    time.Duration
	timeout time.Duration
}

// newSession checks the network and fills in the default waits. The passphrase must be the local standalone
// network's: the recipes never sign for a public network.
func newSession(net Network) (*session, error) {
	if net.RPC == nil || net.Friendbot == nil {
		return nil, errors.New("the recipe's network has no RPC or no friendbot")
	}
	if net.Passphrase != orchestration.StandalonePassphrase {
		return nil, fmt.Errorf("the network's passphrase is %q, not the local standalone network's %q: the recipes run only against a local network",
			net.Passphrase, orchestration.StandalonePassphrase)
	}
	s := &session{net: net, poll: net.Poll, timeout: net.Timeout}
	if s.poll <= 0 {
		s.poll = PollInterval
	}
	if s.timeout <= 0 {
		s.timeout = WaitTimeout
	}
	return s, nil
}

// wait calls try every poll until it reports done, returns an error, or the timeout passes; what names the wait in
// the timeout's error, and last is the last reason try gave for not being done.
func (s *session) wait(ctx context.Context, what string, try func() (done bool, notYet string, err error)) error {
	deadline := time.Now().Add(s.timeout)
	for {
		done, notYet, err := try()
		if err != nil || done {
			return err
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%s: not done after %s (%s)", what, s.timeout, notYet)
		}
		t := time.NewTimer(s.poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return fmt.Errorf("%s: %w", what, ctx.Err())
		case <-t.C:
		}
	}
}

// key is the deterministic key of an entry's role, keypair.FromRawSeed(sha256("ltk/<entry id>/<role>"))
// (internal/model.AccountKey).
func key(entryID, role string) *keypair.Full {
	kp, err := model.AccountKey(entryID, role)
	if err != nil {
		// FromRawSeed fails only on a seed of the wrong length; sha256 is always 32 bytes.
		panic(err)
	}
	return kp
}

// fund funds each account through the friendbot and waits until RPC serves it. The friendbot answers once its
// transaction is in a ledger, so a transaction submitted afterwards lands in a later ledger: the shape's transaction
// is alone in its captured ledger.
func (s *session) fund(ctx context.Context, addresses ...string) error {
	for _, a := range addresses {
		if err := s.net.Friendbot.Fund(ctx, a); err != nil {
			return err
		}
		if _, err := s.sequence(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

// sequence reads the sequence number of the account address (a G... strkey) with getLedgerEntries, polling until RPC
// serves the account.
func (s *session) sequence(ctx context.Context, address string) (int64, error) {
	var lk xdr.LedgerKey
	if err := lk.SetAccount(xdr.MustAddress(address)); err != nil {
		return 0, fmt.Errorf("the ledger key of %s: %w", address, err)
	}
	raw, err := lk.MarshalBinary()
	if err != nil {
		return 0, fmt.Errorf("the ledger key of %s: %w", address, err)
	}
	var seq int64
	err = s.wait(ctx, "reading account "+address+" with getLedgerEntries", func() (bool, string, error) {
		got, err := s.net.RPC.GetLedgerEntries(ctx, []string{xdrcodec.ToBase64(raw)})
		if err != nil {
			return false, err.Error(), nil
		}
		if len(got.Entries) != 1 {
			return false, "the account is not in the ledger yet", nil
		}
		var data xdr.LedgerEntryData
		if err := xdr.SafeUnmarshalBase64(got.Entries[0].XDR, &data); err != nil {
			return false, "", fmt.Errorf("account %s: getLedgerEntries' entry is not a LedgerEntryData: %w", address, err)
		}
		acc, ok := data.GetAccount()
		if !ok {
			return false, "", fmt.Errorf("account %s: getLedgerEntries returned a %s entry", address, data.Type)
		}
		seq = int64(acc.SeqNum)
		return true, "", nil
	})
	return seq, err
}

// account returns the transaction source of address (a G... or M... strkey) at the sequence number of its underlying
// G account. IncrementSequenceNum on each built transaction moves it on.
func (s *session) account(ctx context.Context, address string, underlying string) (*txnbuild.SimpleAccount, error) {
	seq, err := s.sequence(ctx, underlying)
	if err != nil {
		return nil, err
	}
	return &txnbuild.SimpleAccount{AccountID: address, Sequence: seq}, nil
}

// build builds the next transaction of source with ops, the minimum base fee and no time bound (the deciding test's
// parameters), signed by signer.
func (s *session) build(source *txnbuild.SimpleAccount, signer *keypair.Full, ops ...txnbuild.Operation) (*txnbuild.Transaction, error) {
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        source,
		IncrementSequenceNum: true,
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewInfiniteTimeout()},
		Operations:           ops,
	})
	if err != nil {
		return nil, fmt.Errorf("building the transaction: %w", err)
	}
	tx, err = tx.Sign(s.net.Passphrase, signer)
	if err != nil {
		return nil, fmt.Errorf("signing the transaction: %w", err)
	}
	return tx, nil
}

// signed is a built transaction's envelope and hash.
type signed struct {
	envelope string // base64 TransactionEnvelope
	hash     string // hex
}

// encode returns tx's envelope and hash.
func (s *session) encode(tx *txnbuild.Transaction) (signed, error) {
	env, err := tx.Base64()
	if err != nil {
		return signed{}, fmt.Errorf("encoding the transaction: %w", err)
	}
	h, err := tx.HashHex(s.net.Passphrase)
	if err != nil {
		return signed{}, fmt.Errorf("hashing the transaction: %w", err)
	}
	return signed{envelope: env, hash: h}, nil
}

// outcome is what the network did with a submitted transaction.
type outcome struct {
	// stage and status: "sendTransaction" with ERROR, or "getTransaction" with SUCCESS or FAILED.
	stage, status string
	// ledger is the ledger that applied it (getTransaction).
	ledger uint32
	// resultXdr is its TransactionResult, base64.
	resultXdr string
}

// accepted reports whether the network applied the transaction successfully.
func (o outcome) accepted() bool { return o.stage == "getTransaction" && o.status == "SUCCESS" }

// submit sends tx with sendTransaction and, unless it is refused at once (ERROR), polls getTransaction until it is no
// longer NOT_FOUND. TRY_AGAIN_LATER, or an answer for another hash, is an error.
func (s *session) submit(ctx context.Context, tx signed) (outcome, error) {
	sent, err := s.net.RPC.SendTransaction(ctx, tx.envelope)
	if err != nil {
		return outcome{}, err
	}
	switch sent.Status {
	case "ERROR":
		return outcome{stage: "sendTransaction", status: sent.Status, resultXdr: sent.ErrorResultXdr}, nil
	case "PENDING", "DUPLICATE":
	default:
		return outcome{}, fmt.Errorf("sendTransaction of %s answered status %s", tx.hash, sent.Status)
	}
	if sent.Hash != "" && sent.Hash != tx.hash {
		return outcome{}, fmt.Errorf("sendTransaction answered hash %s for transaction %s", sent.Hash, tx.hash)
	}
	var out outcome
	err = s.wait(ctx, "getTransaction "+tx.hash, func() (bool, string, error) {
		got, err := s.net.RPC.GetTransaction(ctx, tx.hash)
		if err != nil {
			return false, "", err
		}
		switch got.Status {
		case "SUCCESS", "FAILED":
			out = outcome{stage: "getTransaction", status: got.Status, ledger: got.Ledger, resultXdr: got.ResultXdr}
			return true, "", nil
		case "NOT_FOUND":
			return false, "status NOT_FOUND", nil
		}
		return false, "", fmt.Errorf("getTransaction of %s answered status %s", tx.hash, got.Status)
	})
	return out, err
}

// NotAppliedError is a transaction of a recipe that the network did not apply: refused at sendTransaction (status
// ERROR), or reported FAILED by getTransaction. These are the first two conditions of a shape that cannot work at a
// protocol; the Protocol selector (internal/generate/protocol.go) retries the shape at protocol 28.
type NotAppliedError struct {
	// What names the transaction in the recipe.
	What string
	// TxHash is its hash, hex.
	TxHash string
	// Stage is "sendTransaction" or "getTransaction"; Status is what the network answered there.
	Stage, Status string
	// ResultXdr is the network's TransactionResult, base64 XDR, when it sent one.
	ResultXdr string
}

// Error names the transaction, the stage, the status and the result.
func (e *NotAppliedError) Error() string {
	return fmt.Sprintf("%s: transaction %s was not applied: %s answered %s (result %s)", e.What, e.TxHash, e.Stage, e.Status, e.ResultXdr)
}

// apply submits tx and requires it to succeed; the ledger that applied it is returned. A transaction refused or not
// successful is a *NotAppliedError: a shape that cannot work at this protocol.
func (s *session) apply(ctx context.Context, what string, tx signed) (uint32, error) {
	o, err := s.submit(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", what, err)
	}
	if !o.accepted() {
		return 0, &NotAppliedError{What: what, TxHash: tx.hash, Stage: o.stage, Status: o.status, ResultXdr: o.resultXdr}
	}
	return o.ledger, nil
}

// capture fetches the ledger that holds the shape's transaction with getLedgers (limit 1) and checks that it is that
// ledger and that the shape's transaction is the only one in it. The protocol is read from the captured header, never
// from the protocol asked of the network.
func (s *session) capture(ctx context.Context, entryID string, ledger uint32, txHash string) (Capture, error) {
	var b64 string
	err := s.wait(ctx, fmt.Sprintf("getLedgers of ledger %d", ledger), func() (bool, string, error) {
		got, err := s.net.RPC.GetLedgers(ctx, ledger, 1)
		if err != nil {
			return false, err.Error(), nil
		}
		if len(got.Ledgers) == 0 || got.Ledgers[0].Sequence != ledger || got.Ledgers[0].MetadataXdr == "" {
			return false, fmt.Sprintf("RPC does not serve ledger %d yet (latest %d)", ledger, got.LatestLedger), nil
		}
		b64 = got.Ledgers[0].MetadataXdr
		return true, "", nil
	})
	if err != nil {
		return Capture{}, err
	}
	raw, err := xdrcodec.FromBase64(b64)
	if err != nil {
		return Capture{}, fmt.Errorf("ledger %d's metadataXdr: %w", ledger, err)
	}
	seed := seedOf(entryID)
	v, err := xdrcodec.Decode(seed.XdrType, raw)
	if err != nil {
		return Capture{}, fmt.Errorf("ledger %d's metadataXdr: %w", ledger, err)
	}
	lcm := v.(*xdr.LedgerCloseMeta)
	if got := lcm.LedgerSequence(); got != ledger {
		return Capture{}, fmt.Errorf("getLedgers served ledger %d for ledger %d", got, ledger)
	}
	if n := lcm.CountTransactions(); n != 1 {
		return Capture{}, fmt.Errorf("ledger %d holds %d transactions; the shape's %s must be alone in it", ledger, n, txHash)
	}
	if h := lcm.TransactionHash(0); hex.EncodeToString(h[:]) != txHash {
		return Capture{}, fmt.Errorf("ledger %d holds transaction %x, not the shape's %s", ledger, h[:], txHash)
	}
	return Capture{
		EntryID:  entryID,
		XdrType:  seed.XdrType,
		XDR:      raw,
		Ledger:   ledger,
		Protocol: int(lcm.ProtocolVersion()),
		TxHash:   txHash,
	}, nil
}
