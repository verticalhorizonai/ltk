package model

// The Seed entry definitions.

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"slices"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/strkey"
)

// TestSeeds: the three seeds, in seed order: the entry ids, CAPs CAP-0040, CAP-0038, CAP-0027, XDR type
// LedgerCloseMeta, synthetic false; entry 2 alone records the network's refusal.
func TestSeeds(t *testing.T) {
	want := []struct{ id, cap string }{
		{"cap-0040-signed-payload-signer", "CAP-0040"},
		{"cap-0038-pool-share-trustline", "CAP-0038"},
		{"cap-0027-muxed-account", "CAP-0027"},
	}
	got := Seeds()
	if len(got) != 3 {
		t.Fatalf("%d seeds", len(got))
	}
	for i, w := range want {
		s := got[i]
		if s.ID != w.id || s.Cap != w.cap || s.XdrType != "LedgerCloseMeta" || s.Synthetic || s.Shape == "" || len(s.Roles) == 0 {
			t.Errorf("seed %d: %+v, want %s %s LedgerCloseMeta synthetic false", i+1, s, w.id, w.cap)
		}
		if s.RecordsNetworkRefusal != (i == 1) {
			t.Errorf("seed %d records the refusal: %v", i+1, s.RecordsNetworkRefusal)
		}
		if b, ok := SeedByID(w.id); !ok || b.ID != w.id {
			t.Errorf("SeedByID(%s)", w.id)
		}
	}
	if _, ok := SeedByID("cap-0000-nothing"); ok {
		t.Error("SeedByID of an unknown id")
	}
	got[0].Roles[0] = "changed"
	if Seeds()[0].Roles[0] == "changed" {
		t.Error("Seeds must return copies")
	}
	if SignedPayloadLength != 29 || PoolAssetCode != "USD" {
		t.Errorf("entry 1's payload is 29 bytes and entry 2's asset code USD: %d %q", SignedPayloadLength, PoolAssetCode)
	}
}

// TestAccountKey: every key is keypair.FromRawSeed(sha256("ltk/<entry id>/<role>")), the same from call to call.
func TestAccountKey(t *testing.T) {
	for _, s := range Seeds() {
		for _, role := range s.Roles {
			k, err := AccountKey(s.ID, role)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := keypair.FromRawSeed(sha256.Sum256([]byte("ltk/" + s.ID + "/" + role)))
			again, _ := AccountKey(s.ID, role)
			if k.Address() != want.Address() || k.Seed() != want.Seed() || again.Address() != k.Address() {
				t.Errorf("%s/%s: %s, want %s", s.ID, role, k.Address(), want.Address())
			}
		}
	}
	a, _ := AccountKey(SeedSignedPayloadSigner, "source")
	b, _ := AccountKey(SeedMuxedAccount, "source")
	if a.Address() == b.Address() {
		t.Error("the same role of two entries must be two accounts")
	}
}

// orderDisagrees reports whether two account addresses sort one way as strkey text and the other way as key bytes,
// and which is lower as key bytes.
func orderDisagrees(t *testing.T, a, b string) (bool, string) {
	t.Helper()
	ra, err := strkey.Decode(strkey.VersionByteAccountID, a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := strkey.Decode(strkey.VersionByteAccountID, b)
	if err != nil {
		t.Fatal(err)
	}
	byteOrder := bytes.Compare(ra, rb)
	textOrder := -1
	if a > b {
		textOrder = 1
	}
	lower := a
	if byteOrder > 0 {
		lower = b
	}
	return byteOrder != 0 && byteOrder != textOrder, lower
}

// TestPoolIssuerPair: the search of the deciding test (the feasibility run made before this tool) over the roles
// issuer-<n> of entry 2, pairs (i, j) with j < i in order, finds the two roles fixed as constants; their keys sort one
// way as strkey text and the other way as key bytes; the exported public keys are theirs, in key-byte order.
func TestPoolIssuerPair(t *testing.T) {
	addr := func(role string) string {
		k, err := AccountKey(SeedPoolShareTrustline, role)
		if err != nil {
			t.Fatal(err)
		}
		return k.Address()
	}
	var foundLower, foundHigher string
search:
	for i := 0; i < 100; i++ {
		for j := 0; j < i; j++ {
			ri, rj := fmt.Sprintf("issuer-%d", i), fmt.Sprintf("issuer-%d", j)
			if disagree, lower := orderDisagrees(t, addr(ri), addr(rj)); disagree {
				foundLower, foundHigher = ri, rj
				if lower == addr(rj) {
					foundLower, foundHigher = rj, ri
				}
				break search
			}
		}
	}
	if foundLower != PoolIssuerLowerBytesRole || foundHigher != PoolIssuerHigherBytesRole {
		t.Fatalf("the search finds %s (lower bytes) and %s; the constants are %s and %s", foundLower, foundHigher,
			PoolIssuerLowerBytesRole, PoolIssuerHigherBytesRole)
	}
	lo, hi := addr(PoolIssuerLowerBytesRole), addr(PoolIssuerHigherBytesRole)
	if lo != PoolIssuerLowerBytes || hi != PoolIssuerHigherBytes {
		t.Errorf("exported keys %s %s, derived %s %s", PoolIssuerLowerBytes, PoolIssuerHigherBytes, lo, hi)
	}
	rlo, _ := strkey.Decode(strkey.VersionByteAccountID, lo)
	rhi, _ := strkey.Decode(strkey.VersionByteAccountID, hi)
	if bytes.Compare(rlo, rhi) >= 0 {
		t.Error("PoolIssuerLowerBytes must be lower as key bytes")
	}
	if !(hi < lo) {
		t.Error("as strkey text the order is the other way: PoolIssuerHigherBytes sorts first")
	}
	if s, _ := SeedByID(SeedPoolShareTrustline); !slices.Contains(s.Roles, PoolIssuerLowerBytesRole) || !slices.Contains(s.Roles, PoolIssuerHigherBytesRole) {
		t.Errorf("entry 2's roles %v hold both issuers", s.Roles)
	}
}
